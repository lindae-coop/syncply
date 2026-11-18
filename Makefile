COMPOSE  ?= podman-compose
NODES    := st-server st-client1 st-client2 st-client3
SERVICES := sftpgo $(NODES) pairsrv
ARGS     ?= status
RUN      := $(COMPOSE) run --rm tools

.PHONY: help up down restart logs proto demo status test clean

help:
	@echo "make up       - arranca SFTPGo, os nodos Syncthing e pairsrv"
	@echo "make demo     - parte de cero e valida o fluxo completo"
	@echo "make status   - estado de dispositivos, carpetas e conexións"
	@echo "make test     - tests unitarios"
	@echo "make proto ARGS='...' - executa cmd/stproto con argumentos"
	@echo "make restart  - reinicia pairsrv (recompila tras cambios)"
	@echo "make logs     - logs dos servizos"
	@echo "make down     - para os contedores"
	@echo "make clean    - para e borra ./.dev (estado dos nodos)"

up:
	@mkdir -p $(addprefix .dev/,$(NODES) pairsrv sftpgo/lib sftpgo/data)
	$(COMPOSE) up -d $(SERVICES)

down:
	-$(COMPOSE) down 2>/dev/null

restart:
	$(COMPOSE) restart pairsrv

logs:
	$(COMPOSE) logs -f $(SERVICES)

proto:
	$(RUN) go run ./cmd/stproto $(ARGS)

demo:
	$(MAKE) clean
	$(MAKE) up
	$(RUN) go run ./cmd/stproto demo

status:
	$(RUN) go run ./cmd/stproto status

test:
	$(RUN) sh -c 'go vet ./... && go test ./...'

clean: down
	rm -rf .dev
