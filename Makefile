COMPOSE  ?= podman-compose
NODES    := st-server st-client1 st-client2 st-client3
CLIENTS  := st-client1 st-client2 st-client3
SERVICES := sftpgo $(NODES) pairsrv
CLIENT_IMAGE := localhost/cld-client-dev:latest
ARGS     ?= status
C        ?= 1
RUN      := $(COMPOSE) run --rm tools

.PHONY: help up down restart logs proto demo fresh ui web otp blob dist dist-windows installtest status test clean

# Paquete de Linux: cliente xenérico, o mesmo para todos os servidores (o
# servidor indícao o texto de vinculación).
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
SYNCTHING_IMAGE := docker.io/syncthing/syncthing:2.1.7
DIST       := dist/cld-linux-amd64

# Windows: cld.exe autoinstalable con Syncthing dentro. O zip oficial
# verifícase co SHA-256 publicado en sha256sum.txt.asc da versión.
ST_VERSION := 2.1.7
ST_WIN_ZIP := syncthing-windows-amd64-v$(ST_VERSION).zip
ST_WIN_SHA := 3ce7da770f546a8d02b65b2e7ee31f2a3b8b7c3f990ede045590cca4172ecfe1
DIST_WIN   := dist/cld-windows-amd64.exe

help:
	@echo "make up       - arranca SFTPGo, o Syncthing do servidor, pairsrv e os clientes (cld + o seu Syncthing)"
	@echo "make demo     - parte de cero e valida o fluxo completo"
	@echo "make fresh    - parte de cero con SFTPGo preparado e sen emparellar (para probar a web)"
	@echo "make ui C=1   - abre no navegador a web do cliente 1 (como o acceso directo)"
	@echo "make web      - abre a web de pairsrv (entrar como usuario de SFTPGo e ver o código de vinculación)"
	@echo "make otp U=alice - código de vinculación actual dun usuario de proba (o mesmo que mostra a web)"
	@echo "make blob U=alice - texto de vinculación dun usuario de proba (o mesmo que mostra a web)"
	@echo "make dist     - paquete de Linux (dist/cld-linux-amd64.tar.gz) co instalador"
	@echo "make dist-windows - instalador de Windows (dist/cld-windows-amd64.exe, autoinstalable)"
	@echo "make installtest - proba o paquete nun Debian con systemd (require make up e SFTPGo preparado)"
	@echo "make status   - estado de dispositivos, carpetas e conexións"
	@echo "make test     - tests unitarios"
	@echo "make proto ARGS='...' - executa cmd/stproto con argumentos"
	@echo "make restart  - reinicia pairsrv e os clientes cld (recompila tras cambios)"
	@echo "make logs     - logs dos servizos"
	@echo "make down     - para os contedores"
	@echo "make clean    - para e borra ./.dev (estado dos nodos)"

up:
	@mkdir -p dist $(addprefix .dev/,$(NODES) cld-client1 cld-client2 cld-client3 pairsrv sftpgo/lib sftpgo/data)
	@podman image exists $(CLIENT_IMAGE) || $(COMPOSE) build st-client1
	$(COMPOSE) up -d $(SERVICES)

down:
	-$(COMPOSE) down 2>/dev/null

restart:
	$(COMPOSE) restart pairsrv $(CLIENTS)

logs:
	$(COMPOSE) logs -f $(SERVICES)

proto:
	$(RUN) go run ./cmd/stproto $(ARGS)

demo:
	$(MAKE) clean
	$(MAKE) up
	$(RUN) go run ./cmd/stproto demo

fresh:
	$(MAKE) clean
	$(MAKE) up
	$(RUN) sh -c 'go run ./cmd/stproto init && go run ./cmd/stproto sftpgo-setup'

# Igual que `cld open` no acceso directo: pídelle ao daemon unha URL dun só
# uso e ábrea no navegador do equipo.
ui:
	@url=$$($(COMPOSE) exec -T st-client$(C) go run ./cmd/cld open -dir /cfg -print 2>/dev/null | tail -1); \
	  case "$$url" in http*) echo "$$url"; xdg-open "$$url" >/dev/null 2>&1 || true;; \
	  *) echo "st-client$(C) non responde (make up?)"; exit 1;; esac

web:
	@echo http://127.0.0.1:8090/; xdg-open http://127.0.0.1:8090/ >/dev/null 2>&1 || true

U ?= alice
otp:
	@$(RUN) go run ./cmd/stproto otp $(U) 2>/dev/null | tail -1

blob:
	@$(RUN) go run ./cmd/stproto blob $(U) 2>/dev/null | tail -1

dist:
	@mkdir -p $(DIST)
	$(RUN) sh -c 'GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST)/cld ./cmd/cld'
	podman run --rm --entrypoint cat $(SYNCTHING_IMAGE) /bin/syncthing > $(DIST)/syncthing
	rm -f $(DIST)/cld.json
	cp packaging/linux/install.sh packaging/linux/LICENZAS.txt $(DIST)/
	chmod 755 $(DIST)/cld $(DIST)/syncthing $(DIST)/install.sh
	tar -C dist -czf $(DIST).tar.gz cld-linux-amd64
	@echo "Paquete: $(DIST).tar.gz ($(VERSION))"

dist-windows:
	@mkdir -p dist/cache cmd/cld/embed
	$(RUN) sh -ec '\
	  z=dist/cache/$(ST_WIN_ZIP); \
	  if [ ! -f $$z ]; then wget -q -O $$z.part https://github.com/syncthing/syncthing/releases/download/v$(ST_VERSION)/$(ST_WIN_ZIP); mv $$z.part $$z; fi; \
	  echo "$(ST_WIN_SHA)  $$z" | sha256sum -c -; \
	  unzip -p $$z syncthing-windows-amd64-v$(ST_VERSION)/syncthing.exe | gzip -9 > cmd/cld/embed/syncthing.exe.gz; \
	  sed "s/$$/\r/" packaging/linux/LICENZAS.txt > cmd/cld/embed/LICENZAS.txt; \
	  GOOS=windows GOARCH=amd64 go build -tags embedsyncthing -trimpath \
	    -ldflags "-s -w -H windowsgui -X main.version=$(VERSION)" -o $(DIST_WIN) ./cmd/cld'
	@echo "Instalador: $(DIST_WIN) ($(VERSION))"

installtest: dist
	dev/installtest.sh

status:
	$(RUN) go run ./cmd/stproto status

test:
	$(RUN) sh -c 'go vet ./... && go test ./...'

clean: down
	rm -rf .dev
