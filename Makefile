COMPOSE  ?= podman-compose
NODES    := st-server st-client1 st-client2 st-client3
CLIENTS  := st-client1 st-client2 st-client3
SERVICES := sftpgo $(NODES) pairsrv
CLIENT_IMAGE := localhost/syncply-client-dev:latest
ARGS     ?= status
C        ?= 1
RUN      := $(COMPOSE) run --rm tools

.PHONY: help up down restart logs proto demo fresh ui web otp blob dist dist-windows installtest updatetest update-key winvm-install wintest winupdatetest status test clean

# Paquete de Linux: cliente xenérico, o mesmo para todos os servidores (o
# servidor indícao o texto de vinculación).
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
SYNCTHING_IMAGE := docker.io/syncthing/syncthing:2.1.7
DIST       := dist/syncply-linux-amd64
DIST_OLD   := dist/syncply-linux-amd64-anterior.tar.gz

# Windows: syncply.exe autoinstalable con Syncthing dentro. O zip oficial
# verifícase co SHA-256 publicado en sha256sum.txt.asc da versión.
ST_VERSION := 2.1.7
ST_WIN_ZIP := syncthing-windows-amd64-v$(ST_VERSION).zip
ST_WIN_SHA := 3ce7da770f546a8d02b65b2e7ee31f2a3b8b7c3f990ede045590cca4172ecfe1
DIST_WIN   := dist/syncply-windows-amd64.exe

help:
	@echo "make up       - arranca SFTPGo, o Syncthing do servidor, pairsrv e os clientes (syncply + o seu Syncthing)"
	@echo "make demo     - parte de cero e valida o fluxo completo"
	@echo "make fresh    - parte de cero con SFTPGo preparado e sen emparellar (para probar a web)"
	@echo "make ui C=1   - abre no navegador a web do cliente 1 (como o acceso directo)"
	@echo "make web      - abre a web de pairsrv (entrar como usuario de SFTPGo e ver o código de vinculación)"
	@echo "make otp U=alice - código de vinculación actual dun usuario de proba (o mesmo que mostra a web)"
	@echo "make blob U=alice - texto de vinculación dun usuario de proba (o mesmo que mostra a web)"
	@echo "make dist     - paquete de Linux (dist/syncply-linux-amd64.tar.gz: syncply autoinstalable)"
	@echo "make dist-windows - instalador de Windows (dist/syncply-windows-amd64.exe, autoinstalable)"
	@echo "make installtest - proba o paquete nun Debian con systemd (require make up e SFTPGo preparado)"
	@echo "make updatetest  - proba a actualización automática do cliente de Linux (v0.0.1 → v0.0.2) nun Debian"
	@echo "make update-key  - xera a clave do proxecto para asinar as actualizacións (.keys/, unha vez)"
	@echo "make winvm-install WIN_ISO=... - instala Windows 11 (desatendido) na VM de probas (.winvm)"
	@echo "make wintest  - proba o instalador de Windows na VM (require make up, SFTPGo preparado e make dist-windows)"
	@echo "make winupdatetest - proba a actualización automática do cliente de Windows na VM (v0.0.1 → v0.0.2)"
	@echo "make status   - estado de dispositivos, carpetas e conexións"
	@echo "make test     - tests unitarios"
	@echo "make proto ARGS='...' - executa cmd/stproto con argumentos"
	@echo "make restart  - reinicia pairsrv e os clientes syncply (recompila tras cambios)"
	@echo "make logs     - logs dos servizos"
	@echo "make down     - para os contedores"
	@echo "make clean    - para e borra ./.dev (estado dos nodos)"

up:
	@mkdir -p dist $(addprefix .dev/,$(NODES) syncply-client1 syncply-client2 syncply-client3 pairsrv sftpgo/lib sftpgo/data)
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

# Igual que `syncply open` no acceso directo: pídelle ao daemon unha URL dun só
# uso e ábrea no navegador do equipo.
ui:
	@url=$$($(COMPOSE) exec -T st-client$(C) go run ./cmd/syncply open -dir /cfg -print 2>/dev/null | tail -1); \
	  case "$$url" in http*) echo "$$url"; xdg-open "$$url" >/dev/null 2>&1 || true;; \
	  *) echo "st-client$(C) non responde (make up?)"; exit 1;; esac

web:
	@echo http://127.0.0.1:8090/; xdg-open http://127.0.0.1:8090/ >/dev/null 2>&1 || true

U ?= alice
otp:
	@$(RUN) go run ./cmd/stproto otp $(U) 2>/dev/null | tail -1

blob:
	@$(RUN) go run ./cmd/stproto blob $(U) 2>/dev/null | tail -1

# Linux: syncply autoinstalable con Syncthing dentro (etiqueta
# embedsyncthing), nun tar para conservar o permiso de execución. v0.5.0
# publica ademais o formato anterior (syncply-linux-amd64/{syncply,syncthing}),
# o único que saben instalar os clientes ata v0.4.1 ao actualizarse.
dist:
	@mkdir -p dist/linux cmd/syncply/embed/linux
	podman run --rm --entrypoint cat $(SYNCTHING_IMAGE) /bin/syncthing > dist/linux/syncthing
	$(RUN) sh -ec '\
	  gzip -9 -c dist/linux/syncthing > cmd/syncply/embed/linux/syncthing.gz; \
	  sh packaging/licenzas.sh packaging/linux/LICENZAS.txt ./cmd/syncply linux > cmd/syncply/embed/linux/LICENZAS.txt; \
	  GOOS=linux GOARCH=amd64 go build -tags embedsyncthing -trimpath \
	    -ldflags "-s -w -X main.version=$(VERSION)" -o dist/linux/syncply ./cmd/syncply'
	chmod 755 dist/linux/syncply dist/linux/syncthing
	tar -C dist/linux --owner=0 --group=0 --numeric-owner -czf $(DIST).tar.gz syncply
	rm -rf $(DIST) && mkdir -p $(DIST) && cp -p dist/linux/syncply dist/linux/syncthing $(DIST)/
	tar -C dist --owner=0 --group=0 --numeric-owner -czf $(DIST_OLD) syncply-linux-amd64
	rm -rf $(DIST)
	@echo "Paquete: $(DIST).tar.gz ($(VERSION)); para os clientes anteriores: $(DIST_OLD)"

dist-windows:
	@mkdir -p dist/cache cmd/syncply/embed/windows
	$(RUN) sh -ec '\
	  z=dist/cache/$(ST_WIN_ZIP); \
	  if [ ! -f $$z ]; then wget -q -O $$z.part https://github.com/syncthing/syncthing/releases/download/v$(ST_VERSION)/$(ST_WIN_ZIP); mv $$z.part $$z; fi; \
	  echo "$(ST_WIN_SHA)  $$z" | sha256sum -c -; \
	  unzip -p $$z syncthing-windows-amd64-v$(ST_VERSION)/syncthing.exe | gzip -9 > cmd/syncply/embed/windows/syncthing.exe.gz; \
	  sh packaging/licenzas.sh packaging/linux/LICENZAS.txt ./cmd/syncply windows | sed "s/$$/\r/" > cmd/syncply/embed/windows/LICENZAS.txt; \
	  GOOS=windows GOARCH=amd64 go build -tags embedsyncthing -trimpath \
	    -ldflags "-s -w -H windowsgui -X main.version=$(VERSION)" -o $(DIST_WIN) ./cmd/syncply'
	@echo "Instalador: $(DIST_WIN) ($(VERSION))"

installtest: dist
	dev/installtest.sh

updatetest:
	dev/updatetest.sh

# Clave privada coa que a CI asina as actualizacións dos clientes (segredo
# SYNCPLY_UPDATE_KEY). A pública vai en internal/update/keys.go.
update-key:
	@test ! -e .keys/syncply-update.key || { echo ".keys/syncply-update.key xa existe"; exit 1; }
	@$(RUN) go run ./cmd/relsign keygen -out .keys/syncply-update.key 2>/dev/null | tail -1 | \
	  sed 's/^/Clave pública (engádea a internal\/update\/keys.go): /'

# VM de Windows (dev/winvm): QEMU/KVM nun contedor. A ISO non se distribúe:
# indica a túa (p. ex. Windows 11 Enterprise de avaliación).
WIN_ISO ?=
WINVM_IMAGE := localhost/syncply-winvm:latest
winvm-install:
	@[ -f "$(WIN_ISO)" ] || { echo "indica a ISO de Windows: make winvm-install WIN_ISO=/ruta/windows.iso"; exit 1; }
	@mkdir -p .winvm
	podman build -q -t $(WINVM_IMAGE) dev/winvm >/dev/null
	-podman rm -f syncply-winvm 2>/dev/null
	@echo "Instalando Windows (20-40 min; podes velo por VNC en 127.0.0.1:5900)"
	podman run --rm --name syncply-winvm --device /dev/kvm -v ./.winvm:/work \
	  -v "$(WIN_ISO):/iso/windows.iso:ro" -p 127.0.0.1:5900:5900 $(WINVM_IMAGE) install

wintest:
	dev/wintest.sh

winupdatetest:
	dev/winupdatetest.sh

status:
	$(RUN) go run ./cmd/stproto status

test:
	$(RUN) sh -c 'go vet ./... && go test ./...'

clean: down
	rm -rf .dev
