COMPOSE  ?= podman-compose
NODES    := st-server st-client1 st-client2 st-client3
CLIENTS  := st-client1 st-client2 st-client3
SERVICES := sftpgo $(NODES) pairsrv
CLIENT_IMAGE := localhost/cld-client-dev:latest
ARGS     ?= status
C        ?= 1
RUN      := $(COMPOSE) run --rm tools

.PHONY: help up down restart logs proto demo fresh ui otp dist installtest status test clean

# Paquete de Linux. CLD_CONFIG é o cld.json que leva (no futuro, xerado por
# SFTPGo para cada servidor).
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CLD_CONFIG ?= dev/cld.json
SYNCTHING_IMAGE := docker.io/syncthing/syncthing:2.1.7
DIST       := dist/cld-linux-amd64

help:
	@echo "make up       - arranca SFTPGo, o Syncthing do servidor, pairsrv e os clientes (cld + o seu Syncthing)"
	@echo "make demo     - parte de cero e valida o fluxo completo"
	@echo "make fresh    - parte de cero con SFTPGo preparado e sen emparellar (para probar a web)"
	@echo "make ui C=1   - abre no navegador a web do cliente 1 (como o acceso directo)"
	@echo "make otp U=alice - código OTP actual dun usuario de proba"
	@echo "make dist     - paquete de Linux (dist/cld-linux-amd64.tar.gz) co instalador"
	@echo "make installtest - proba o paquete nun Debian con systemd (require make up e SFTPGo preparado)"
	@echo "make status   - estado de dispositivos, carpetas e conexións"
	@echo "make test     - tests unitarios"
	@echo "make proto ARGS='...' - executa cmd/stproto con argumentos"
	@echo "make restart  - reinicia pairsrv e os clientes cld (recompila tras cambios)"
	@echo "make logs     - logs dos servizos"
	@echo "make down     - para os contedores"
	@echo "make clean    - para e borra ./.dev (estado dos nodos)"

up:
	@mkdir -p $(addprefix .dev/,$(NODES) cld-client1 cld-client2 cld-client3 pairsrv sftpgo/lib sftpgo/data)
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

U ?= alice
otp:
	@$(RUN) go run ./cmd/stproto otp $(U) 2>/dev/null | tail -1

dist:
	@mkdir -p $(DIST)
	$(RUN) sh -c 'GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST)/cld ./cmd/cld'
	podman run --rm --entrypoint cat $(SYNCTHING_IMAGE) /bin/syncthing > $(DIST)/syncthing
	cp $(CLD_CONFIG) $(DIST)/cld.json
	cp packaging/linux/install.sh packaging/linux/LICENZAS.txt $(DIST)/
	chmod 755 $(DIST)/cld $(DIST)/syncthing $(DIST)/install.sh
	tar -C dist -czf $(DIST).tar.gz cld-linux-amd64
	@echo "Paquete: $(DIST).tar.gz ($(VERSION))"

installtest: dist
	dev/installtest.sh

status:
	$(RUN) go run ./cmd/stproto status

test:
	$(RUN) sh -c 'go vet ./... && go test ./...'

clean: down
	rm -rf .dev
