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
| `sftpgo`     | SFTPGo 2.7.6 (BD SQLite), datos en `/srv/sftpgo/data`    |
| `st-server`  | Syncthing do servidor; ve os datos de SFTPGo na mesma ruta |
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

Emparella equipos co Syncthing do servidor mediante o **OTP (TOTP) da conta
de SFTPGo** e mantén a configuración do servidor de acordo co rexistro
`usuario → equipos` e cos permisos de SFTPGo. Reconcilia:

- en cada emparellamento e revogación;
- cando **SFTPGo avisa** dun cambio en usuarios ou grupos: unha regra do
  xestor de eventos (`pairsrv-sync`) chama a `POST /internal/v1/sync` nun
  listener separado (`-hook-listen`, `:8081`) cun token compartido
  (`PAIRSRV_HOOK_TOKEN`). Aplícase en ~1 s;
- periodicamente (`-reconcile`; 5 min en desenvolvemento, para que a demo
  probe os avisos). Cobre os cambios que non xeran eventos en SFTPGo
  (cartafoles), un Syncthing reiniciado ou avisos perdidos.

Le **directamente a BD de SFTPGo** (só lectura) — ver
[internal/sftpgo](internal/sftpgo/directory.go):

- **TOTP:** SFTPGo non expón o segredo pola API (sae `Redacted`) nin ten un
  endpoint para validar o código dun usuario, así que se descifra o segredo
  como fai o seu KMS local (secretbox; con clave mestra se SFTPGo a usa:
  `sftpgo.masterKeyFile`). Outros KMS (Vault, nubes) non se admiten.
- **Usuarios:** activos, sen caducar e non borrados. Se un usuario se
  desactiva, os seus equipos saen do servidor e os seus tokens deixan de
  valer; ao reactivalo, volven sen emparellar de novo.
- **Recursos:** o directorio persoal e os cartafoles virtuais (propios e dos
  grupos, aplicados como fai SFTPGo) en **disco local** e con **permiso de
  escritura completo**: `*`, ou `list`, `download`, `upload`, `overwrite`,
  `create_dirs`, borrar e renomear. Esíxese na raíz do recurso e en calquera
  ruta con permisos propios dentro del, porque Syncthing escribe sen pasar
  por SFTPGo.
- IDs en Syncthing: `sftpgo-home-<usuario>` e `sftpgo-folder-<cartafol>`.
- Configuración: [dev/pairsrv.json](dev/pairsrv.json).

| Método e ruta                | Autenticación | Uso                                   |
|------------------------------|---------------|---------------------------------------|
| `POST /api/v1/pair`          | usuario + OTP | `{user, otp, deviceID, deviceName}` → `{serverDeviceID, serverAddresses, token}` |
| `GET /api/v1/devices`        | Bearer token  | equipos do usuario (conectado, última conexión, cal é o actual) |
| `DELETE /api/v1/devices/{id}`| Bearer token + `{otp}` | revoga un equipo do usuario    |

- Un código OTP non se pode reutilizar en pairsrv; 5 fallos seguidos bloquean o usuario 5 min.
- Límite de equipos por usuario (`maxDevicesPerUser`).
- Un equipo vinculado a un usuario non pode vinculalo outro.
- O estado garda en `./.dev/pairsrv/state.json` (só o hash dos tokens).

## Ficheiros de Syncthing en SFTPGo

Un grupo **primario** `syncthing` oculta en SFTPGo (`deny_policy` 1) os
ficheiros internos de Syncthing: `.stfolder`, `.stversions`, `.stignore`,
`~syncthing~*` e `.syncthing.*.tmp`. Así os usuarios non os ven nin os poden
borrar (borrar `.stfolder` para a sincronización da carpeta). Os conflitos
(`*.sync-conflict-*`) si se ven: son contido do usuario. Os patróns de `/`
só se herdan dun grupo primario; ao integrar en SFTPGo isto debería ir no
código.

## Datos de proba

[dev/sftpgo-demo.json](dev/sftpgo-demo.json) (contrasinais e segredos
públicos, só para desenvolvemento). `make proto ARGS=sftpgo-setup` créaos en
SFTPGo, crea a regra de eventos e activa o TOTP de cada usuario como faría
el no WebClient. Todos teñen o grupo primario `syncthing`:

| Usuario | Directorio persoal | `/comun` (grupo `equipo`) | `/arquivo` (grupo) |
|---------|--------------------|---------------------------|--------------------|
| alice   | escritura → **sincroniza** | escritura → **sincroniza** | só lectura → non |
| bob     | só lectura → non   | escritura → **sincroniza** | só lectura → non   |

## Pasos solos

```sh
make up
make proto ARGS="init"
make proto ARGS="sftpgo-setup"
make proto ARGS="pair st-client1 alice"      # calcula o OTP con dev/sftpgo-demo.json
make proto ARGS="pending st-client1"
make proto ARGS="accept st-client1 sftpgo-folder-comun"
make proto ARGS="devices st-client1"
make proto ARGS="revoke st-client1 st-client3 alice"
```
