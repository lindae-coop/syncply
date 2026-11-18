COMPOSE  ?= podman-compose
NODES    := st-server st-client1 st-client2 st-client3
CLIENTS  := cld-client1 cld-client2 cld-client3
SERVICES := sftpgo $(NODES) pairsrv $(CLIENTS)
ARGS     ?= status
C        ?= 1
RUN      := $(COMPOSE) run --rm tools

.PHONY: help up down restart logs proto demo fresh ui otp status test clean

help:
	@echo "make up       - arranca SFTPGo, os nodos Syncthing, pairsrv e os clientes cld"
	@echo "make demo     - parte de cero e valida o fluxo completo"
	@echo "make fresh    - parte de cero con SFTPGo preparado e sen emparellar (para probar a web)"
	@echo "make ui C=1   - abre no navegador a web do cliente 1 (como o acceso directo)"
	@echo "make otp U=alice - código OTP actual dun usuario de proba"
	@echo "make status   - estado de dispositivos, carpetas e conexións"
	@echo "make test     - tests unitarios"
	@echo "make proto ARGS='...' - executa cmd/stproto con argumentos"
	@echo "make restart  - reinicia pairsrv e os clientes cld (recompila tras cambios)"
	@echo "make logs     - logs dos servizos"
	@echo "make down     - para os contedores"
	@echo "make clean    - para e borra ./.dev (estado dos nodos)"

up:
	@mkdir -p $(addprefix .dev/,$(NODES) $(CLIENTS) pairsrv sftpgo/lib sftpgo/data)
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
	@url=$$($(COMPOSE) exec -T cld-client$(C) go run ./cmd/cld open -dir /cfg -print 2>/dev/null | tail -1); \
	  case "$$url" in http*) echo "$$url"; xdg-open "$$url" >/dev/null 2>&1 || true;; \
	  *) echo "cld-client$(C) non responde (make up?)"; exit 1;; esac

U ?= alice
otp:
	@$(RUN) go run ./cmd/stproto otp $(U) 2>/dev/null | tail -1

status:
	$(RUN) go run ./cmd/stproto status

test:
	$(RUN) sh -c 'go vet ./... && go test ./...'

clean: down
	rm -rf .dev
