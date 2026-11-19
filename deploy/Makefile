# Servidor cld en produción. Ver README.md.
COMPOSE ?= podman-compose
-include .env

.PHONY: help up down logs setup-sftpgo build ps

help:
	@echo "./init.sh DOMINIO_CLD DOMINIO_SFTPGO - crea .env e pairsrv.json (unha vez)"
	@echo "make up            - compila pairsrv e arranca todo"
	@echo "make setup-sftpgo  - crea en SFTPGo o grupo syncthing e o aviso a pairsrv (unha vez, con make up feito)"
	@echo "make logs | ps | down"

up:
	@test -f .env -a -f pairsrv.json || { echo "falta .env ou pairsrv.json: executa ./init.sh"; exit 1; }
	@ls ../dist/cld-linux-amd64.tar.gz >/dev/null 2>&1 || echo "aviso: ../dist sen paquetes do cliente (make dist / make dist-windows na raíz)"
	$(COMPOSE) up -d --build

build:
	$(COMPOSE) build pairsrv

setup-sftpgo:
	$(COMPOSE) run --rm -e SFTPGO_ADMIN_USER=$(SFTPGO_ADMIN_USER) -e SFTPGO_ADMIN_PASSWORD=$(SFTPGO_ADMIN_PASSWORD) \
	  pairsrv setup-sftpgo -config /etc/pairsrv.json

logs:
	$(COMPOSE) logs -f

ps:
	$(COMPOSE) ps

down:
	$(COMPOSE) down
