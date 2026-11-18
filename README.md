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
| `st-client1` | Cliente de proba (alice)                                 |
| `st-client2` | Cliente de proba (bob)                                   |
| `st-client3` | Cliente de proba (alice, segundo equipo)                 |
| `pairsrv`    | Servizo de emparellamento con TOTP (`cmd/pairsrv`)       |
| `tools`      | Go 1.27 para executar `cmd/stproto` e os tests           |

- Non se publica **ningún porto** no host. A API REST só é accesible desde a
  rede interna `stnet` e con API key (`ST_APIKEY`, cun valor por defecto só
  para desenvolvemento).
- O estado de cada nodo garda en `./.dev/<nodo>` (ignorado por git). Os
  datos sincronizados están en `./.dev/<nodo>/data/`.
- `PUID=0`: en Podman rootless o root do contedor é o teu usuario, así que os
  ficheiros de `./.dev` son teus. (Con Docker sería root de verdade.)

## pairsrv

Emparella equipos co Syncthing do servidor mediante o OTP (TOTP) do usuario e
mantén a configuración do servidor de acordo co rexistro `usuario → equipos`
e cos permisos sobre as carpetas (reconcilia ao cambiar e cada minuto).
Usuarios, segredos e carpetas veñen de [dev/pairsrv.json](dev/pairsrv.json)
(segredos públicos, só para desenvolvemento); no futuro sairán de SFTPGo.

| Método e ruta                | Autenticación | Uso                                   |
|------------------------------|---------------|---------------------------------------|
| `POST /api/v1/pair`          | usuario + OTP | `{user, otp, deviceID, deviceName}` → `{serverDeviceID, serverAddresses, token}` |
| `GET /api/v1/devices`        | Bearer token  | equipos do usuario (conectado, última conexión, cal é o actual) |
| `DELETE /api/v1/devices/{id}`| Bearer token + `{otp}` | revoga un equipo do usuario    |

- Un código OTP non se pode reutilizar; 5 fallos seguidos bloquean o usuario 5 min.
- Límite de equipos por usuario (`maxDevicesPerUser`).
- Un equipo vinculado a un usuario non pode vinculalo outro.
- O estado garda en `./.dev/pairsrv/state.json` (só o hash dos tokens).
- Novo segredo: `podman-compose run --rm tools go run ./cmd/pairsrv newsecret <usuario>`.

## Pasos solos

```sh
make up
make proto ARGS="init"
make proto ARGS="pair st-client1 alice"      # calcula o OTP con dev/pairsrv.json
make proto ARGS="pending st-client1"
make proto ARGS="accept st-client1 comun"
make proto ARGS="devices st-client1"
make proto ARGS="revoke st-client1 st-client3 alice"
```
