# cld — nube de ficheiros lixeira (SFTPGo + Syncthing)

Ver [CLAUDE.md](CLAUDE.md) para o contexto e as decisións do proxecto.

## Contorno de desenvolvemento

Só fai falta **Podman** (rootless), **podman-compose** e **make**. Nin Go nin
Syncthing se instalan no equipo: todo corre en contedores.

```sh
make demo     # parte de cero e valida o fluxo completo
make status   # estado de dispositivos, carpetas e conexións
make help     # resto de ordes
make clean    # para os contedores e borra o estado (./.dev)
```

Que levanta `compose.yaml`:

| Servizo      | Papel                                                    |
|--------------|----------------------------------------------------------|
| `st-server`  | Syncthing do servidor (no futuro, xunto a SFTPGo)        |
| `st-client1` | Cliente de proba                                         |
| `st-client2` | Cliente de proba                                         |
| `tools`      | Go 1.27 para executar `cmd/stproto` contra a API REST    |

- Non se publica **ningún porto** no host. A API REST só é accesible desde a
  rede interna `stnet` e con API key (`ST_APIKEY`, cun valor por defecto só
  para desenvolvemento).
- O estado de cada nodo garda en `./.dev/<nodo>` (ignorado por git). Os
  datos sincronizados están en `./.dev/<nodo>/data/`.
- `PUID=0`: en Podman rootless o root do contedor é o teu usuario, así que os
  ficheiros de `./.dev` son teus. (Con Docker sería root de verdade.)

Pasos solos, por exemplo:

```sh
make up
make proto ARGS="init"
make proto ARGS="share proba Proba"
make proto ARGS="pair st-client1 proba"
make proto ARGS="pending st-client1"
make proto ARGS="accept st-client1 proba"
```
