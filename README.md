# cld — nube de ficheiros lixeira (SFTPGo + Syncthing)

Ver [CLAUDE.md](CLAUDE.md) para o contexto e as decisións do proxecto.

## Contorno de desenvolvemento

Só fai falta **Podman** (rootless), **podman-compose** e **make**. Nin Go nin
Syncthing se instalan no equipo: todo corre en contedores.

```sh
make demo     # parte de cero e valida o fluxo completo
make fresh    # parte de cero con SFTPGo preparado e sen emparellar
make ui C=3   # abre a web do cliente 3 no navegador (como o acceso directo)
make otp U=alice  # código OTP actual dun usuario de proba
make status   # estado de dispositivos, carpetas e conexións
make help     # resto de ordes
make clean    # para os contedores e borra o estado (./.dev)
```

Que levanta `compose.yaml`:

| Servizo      | Papel                                                    |
|--------------|----------------------------------------------------------|
| `sftpgo`     | SFTPGo 2.7.6 (BD SQLite), datos en `/srv/sftpgo/data`    |
| `st-server`  | Syncthing do servidor; ve os datos de SFTPGo na mesma ruta |
| `st-client1` | Cliente de proba (alice): `cld daemon` + o seu Syncthing; web en `127.0.0.1:8411` |
| `st-client2` | Cliente de proba (bob); web en `127.0.0.1:8412`          |
| `st-client3` | Cliente de proba (alice, segundo equipo); web en `127.0.0.1:8413` |
| `pairsrv`    | Servizo de emparellamento con TOTP (`cmd/pairsrv`)       |
| `tools`      | Go 1.27 para executar `cmd/stproto` e os tests           |

- A API REST de Syncthing só é accesible desde a rede interna `stnet` e con
  API key (`ST_APIKEY`, cun valor por defecto só para desenvolvemento). O
  único porto publicado é a web dos clientes `cld`, e só en `127.0.0.1`.
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

## Cliente `cld`

Un só binario ([cmd/cld](cmd/cld/main.go), [internal/client](internal/client)):

- `cld daemon`: servizo de usuario. **Arranca e vixía o seu propio
  Syncthing** (proceso fillo con `--no-restart`: reinícioo ao momento se
  Syncthing pide reiniciarse e con espera crecente se falla) e serve a web
  de configuración só en `127.0.0.1` (porto libre).
  - Syncthing vai en `<dir>/syncthing` (`0700`), coa **API nun socket Unix**
    (`gui.sock`; en Windows, porto libre en loopback) e unha API key
    aleatoria gardada no estado. Busca o binario `syncthing` xunto a `cld`
    ou no PATH (`-syncthing`). Con `-st-url` usa un Syncthing externo.
  - Aplica `syncthingOptions` de `cld.json` cada vez que Syncthing arranca.
  - Nesa web o usuario:
  - vincula o equipo co seu usuario e o código OTP;
  - ve os recursos que sincroniza (estado, tamaño) e os que ofrece o
    servidor, escolle a carpeta local de cada un e acepta, ou deixa de
    sincronizar (os ficheiros locais quedan);
  - pausa 1 h / 8 h (retómase só), ou **detén** (para o proceso de
    Syncthing; lémbrase entre reinicios) e arranca;
  - ve os seus equipos e revoga outros (cun código OTP novo).
- `cld open`: o que executa o **acceso directo**. Le `ui.json` (porto e
  clave de control, `0600`, só do usuario), pídelle ao daemon unha URL de
  acceso dun só uso (60 s) e ábrea no navegador. O usuario non ten que saber
  porto, enderezo nin contrasinal.
- `cld shortcut`: crea o acceso directo `cld` no menú de aplicacións (Linux,
  `~/.local/share/applications/cld.desktop`). En Windows e macOS farao o
  instalador.

Seguridade da web: só loopback; URL de acceso dun só uso → cookie de sesión
`HttpOnly`, `SameSite=Strict`; comprobación de `Host` (DNS rebinding) e de
`Origin`/`Sec-Fetch-Site` nos POST (CSRF); CSP sen JavaScript.

Configuración do instalador (`cld.json`): `pairsrvURL`, opcionalmente
`serverDeviceID` (se se indica, o emparellamento só se acepta con ese
servidor) e `syncthingOptions` (descubrimento, relays, informes de uso…).

No contorno, os contedores `st-clientN` usan a imaxe
[dev/Containerfile.client](dev/Containerfile.client) (Go + binario de
Syncthing 2.1.7). A API de Syncthing vai en TCP interno (`-st-gui
0.0.0.0:8384`, `ST_APIKEY`) para que `stproto` siga chegando a ela. Exemplo de desenvolvemento: [dev/cld.json](dev/cld.json).

Probar a web en desenvolvemento: `make fresh`, `make ui C=1` e vincular co
usuario `alice` e o código que dá `make otp U=alice` (válido uns 30-60 s).
Tamén se pode dar de alta o segredo `totpSecret` de
[dev/sftpgo-demo.json](dev/sftpgo-demo.json) nunha app autenticadora
(entrada manual, tipo TOTP). O contrasinal só serve para entrar en SFTPGo.

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
