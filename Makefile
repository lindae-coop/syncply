COMPOSE ?= podman-compose
NODES   := st-server st-client1 st-client2
ARGS    ?= status

.PHONY: help up down logs proto demo status clean

help:
	@echo "make up       - arranca os nodos Syncthing (servidor + 2 clientes)"
	@echo "make demo     - fluxo completo: compartir, emparellar, aceptar, introducer, revogar"
	@echo "make status   - estado de dispositivos, carpetas e conexións"
	@echo "make proto ARGS='...' - executa cmd/stproto con argumentos"
	@echo "make logs     - logs dos nodos"
	@echo "make down     - para os contedores"
	@echo "make clean    - para e borra ./.dev (estado dos nodos)"

up:
	@mkdir -p $(addprefix .dev/,$(NODES))
	$(COMPOSE) up -d $(NODES)

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f $(NODES)

proto:
	$(COMPOSE) run --rm tools $(ARGS)

demo:
	$(MAKE) clean
	$(MAKE) up
	$(COMPOSE) run --rm tools demo

status:
	$(COMPOSE) run --rm tools status

clean: down
	rm -rf .dev
