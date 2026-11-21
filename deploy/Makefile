# Servidor syncply en produción. Ver README.md.
COMPOSE ?= podman-compose
-include .env
# Versión dos clientes que leva a imaxe de pairsrv.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help up down logs setup-sftpgo build ps

help:
	@echo "./init.sh DOMINIO_SYNCPLY DOMINIO_SFTPGO - crea .env e pairsrv.json (unha vez)"
	@echo "make up            - compila pairsrv (cos paquetes do cliente) e arranca todo"
	@echo "make setup-sftpgo  - crea en SFTPGo o grupo syncthing e o aviso a pairsrv (unha vez, con make up feito)"
	@echo "make logs | ps | down"

up:
	@test -f .env -a -f pairsrv.json || { echo "falta .env ou pairsrv.json: executa ./init.sh"; exit 1; }
	SYNCPLY_VERSION=$(VERSION) $(COMPOSE) up -d --build

build:
	SYNCPLY_VERSION=$(VERSION) $(COMPOSE) build pairsrv

setup-sftpgo:
	$(COMPOSE) run --rm -e SFTPGO_ADMIN_USER=$(SFTPGO_ADMIN_USER) -e SFTPGO_ADMIN_PASSWORD=$(SFTPGO_ADMIN_PASSWORD) \
	  pairsrv setup-sftpgo -config /etc/pairsrv.json

logs:
	$(COMPOSE) logs -f

ps:
	$(COMPOSE) ps

down:
	$(COMPOSE) down
