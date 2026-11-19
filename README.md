# cld — nube de ficheiros lixeira (SFTPGo + Syncthing)

Ver [CLAUDE.md](CLAUDE.md) para o contexto e as decisións do proxecto.

## Contorno de desenvolvemento

Só fai falta **Podman** (rootless), **podman-compose** e **make**. Nin Go nin
Syncthing se instalan no equipo: todo corre en contedores.

```sh
make demo     # parte de cero e valida o fluxo completo
make fresh    # parte de cero con SFTPGo preparado e sen emparellar
make ui C=3   # abre a web do cliente 3 no navegador (como o acceso directo)
make web          # abre a web de pairsrv (código de vinculación e descarga)
make otp U=alice  # código de vinculación actual dun usuario de proba
make blob U=alice # texto de vinculación dun usuario de proba
make dist         # paquete de Linux co instalador
make installtest  # proba o paquete nun Debian con systemd
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
| `pairsrv`    | Servizo de emparellamento (`cmd/pairsrv`); web dos usuarios en `127.0.0.1:8090` |
| `tools`      | Go 1.27 para executar `cmd/stproto` e os tests           |

- A API REST de Syncthing só é accesible desde a rede interna `stnet` e con
  API key (`ST_APIKEY`, cun valor por defecto só para desenvolvemento). O
  único porto publicado é a web dos clientes `cld`, e só en `127.0.0.1`.
- O estado de cada nodo garda en `./.dev/<nodo>` (ignorado por git). Os
  datos sincronizados están en `./.dev/<nodo>/data/`.
- `PUID=0`: en Podman rootless o root do contedor é o teu usuario, así que os
  ficheiros de `./.dev` son teus. (Con Docker sería root de verdade.)

## pairsrv

Emparella equipos co Syncthing do servidor mediante un **código de
vinculación** que o usuario ve na **web de pairsrv**, e mantén a configuración do servidor de acordo co rexistro
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

### HTTPS e modo de desenvolvemento

pairsrv escoita en HTTP e vai **detrás dun proxy inverso con HTTPS** (que
conserve a cabeceira `Host`). Os clientes validan o certificado coas CA do
sistema, como un navegador: as renovacións do proxy non lles afectan. (Cun
certificado dunha CA propia, hai que instalala nos equipos.)

`web.publicURL` é a URL do proxy e ten que ser `https://`: pairsrv non
arranca con `http://`, e o cliente rexeita textos de vinculación con
`http://`. A única excepción é o **modo de desenvolvemento**, `CLD_DEV=1`
(en `compose.yaml` para todos os contedores; en `make installtest`, como
drop-in do servizo): permite `http` e as cookies van sen `Secure`.

### Web dos usuarios e código de vinculación

Na raíz de pairsrv (`/`; en desenvolvemento `make web`,
`http://127.0.0.1:8090`) o usuario entra co **usuario e contrasinal de
SFTPGo**, que se comproban coa API de SFTPGo (`GET /api/v2/user/token`;
`sftpgo.apiURL`). Se ten activado o segundo factor en SFTPGo, indica tamén
ese código. Dentro ve:

- o **código de vinculación**: 6 díxitos que cambian cada 30 s, cunha barra
  co tempo restante (a páxina recárgase soa; sen JavaScript). É un TOTP
  cun segredo por usuario xerado e gardado por pairsrv (no seu estado), non
  o 2FA de SFTPGo: non fai falta ningunha app autenticadora. Se o código
  actual xa se usou, mostra o seguinte, que xa se acepta;
- as **descargas** do cliente (`web.downloads`; pairsrv serve os ficheiros
  dun directorio en `/descargas/` con `-downloads`; en desenvolvemento,
  `./dist`). O cliente é **xenérico**: o mesmo binario para todos os
  servidores;
- o **texto de vinculación** (`cld1:` + base64url dun JSON
  `{"v":1,"url","user","server"}`), cun botón **Copiar** (único script da
  web, `/static/copy.js`; sen el cópiase a man). Indica ao cliente a URL de
  pairsrv (`web.publicURL`, obrigatoria), o usuario e o Device ID do
  Syncthing do servidor (o cliente rexeita o emparellamento se pairsrv
  responde con outro). **Non leva segredos**: sen o código non serve para
  vincular;
- os recursos que se lle ofrecen e os seus equipos (cantos de cantos).

Sesión en memoria (15 min sen actividade) con cookie `HttpOnly`,
`SameSite=Strict` e `Secure` (agás en desenvolvemento); POST só da mesma orixe; `Cache-Control: no-store`; 5 contrasinais
incorrectos seguidos bloquean o usuario 5 min nesta web.

| Método e ruta                | Autenticación | Uso                                   |
|------------------------------|---------------|---------------------------------------|
| `POST /api/v1/pair`          | usuario + código | `{user, otp, deviceID, deviceName}` → `{serverDeviceID, serverAddresses, token, syncthingOptions}` |
| `GET /api/v1/devices`        | Bearer token  | equipos do usuario (conectado, última conexión, cal é o actual) |
| `DELETE /api/v1/devices/{id}`| Bearer token + `{otp}` | revoga un equipo do usuario    |

- Un código non se pode reutilizar; 5 fallos seguidos bloquean o usuario 5 min.
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

[dev/sftpgo-demo.json](dev/sftpgo-demo.json) (contrasinais
públicos, só para desenvolvemento). `make proto ARGS=sftpgo-setup` créaos en
SFTPGo e crea a regra de eventos. Todos teñen o grupo primario `syncthing`:

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
    - Cada vez que Syncthing arranca aplica os **valores seguros por
    defecto** compilados (`client.DefaultSyncthingOptions`: sen
    descubrimento global, relays, NAT, informes de uso nin de erros, nin
    autoactualización) e, por riba, as `syncthingOptions` que deu pairsrv
    ao vincular (`syncthingOptions` na súa configuración).
  - Nesa web o usuario:
  - vincula o equipo en dous pasos: pega o **texto de vinculación** (valídase
    e compróbase que o servidor responde), confirma «Vas vincular con
    <host> como <usuario>» e introduce o **código de vinculación**. Se o
    código falla, volve á confirmación sen pegar de novo o texto. A URL de
    pairsrv, o usuario e o Device ID do servidor gárdanse no estado
    (`state.json`);
  - se o servidor cambia de enderezo, **actualiza a conexión** desde "Os
    meus equipos" pegando o texto novo e o código (só do mesmo servidor e
    usuario): cambia a URL e o token, sen tocar recursos, carpetas nin a
    pausa;
  - ve os recursos que sincroniza (estado, tamaño) e os que ofrece o
    servidor, escolle a carpeta local de cada un e acepta, ou deixa de
    sincronizar (os ficheiros locais quedan);
  - pausa 1 h / 8 h (retómase só), ou **detén** (para o proceso de
    Syncthing; lémbrase entre reinicios) e arranca;
  - ve os seus equipos e revoga outros (cun código de vinculación novo).
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

O cliente non ten configuración de servidor: todo vén do texto de
vinculación e da resposta de pairsrv. (As instalacións anteriores con
`cld.json` aparecen sen vincular; volver vincular co mesmo usuario renova o
token do equipo.)

No contorno, os contedores `st-clientN` usan a imaxe
[dev/Containerfile.client](dev/Containerfile.client) (Go + binario de
Syncthing 2.1.7). A API de Syncthing vai en TCP interno (`-st-gui
0.0.0.0:8384`, `ST_APIKEY`) para que `stproto` siga chegando a ela.
En desenvolvemento, `web.publicURL` de pairsrv é `http://pairsrv:8080` (a
URL coa que chegan os contedores), aínda que a web se abra en
`127.0.0.1:8090`.

Probar a web en desenvolvemento: `make fresh`, `make web` (entrar como
`alice` co contrasinal de [dev/sftpgo-demo.json](dev/sftpgo-demo.json)),
`make ui C=1` e vincular co texto e o código que mostra a web de pairsrv.
`make blob U=alice` e `make otp U=alice` dan o mesmo texto e código sen
abrir o navegador (entran na web co contrasinal de proba).

## Instalador de Linux

`make dist` xera `dist/cld-linux-amd64.tar.gz`: `cld` (estático),
`syncthing` 2.1.7 (o binario da imaxe oficial fixada), `install.sh` e
[LICENZAS.txt](packaging/linux/LICENZAS.txt) (Syncthing é MPL-2.0). De
momento só amd64. **Non leva configuración de ningún servidor**: o mesmo
paquete vale para todos.

[install.sh](packaging/linux/install.sh) instala **só para o usuario, sen
root**:

- binarios en `~/.local/share/cld/bin`, estado en `~/.config/cld`
  (`0700`; borra o `cld.json` das versións anteriores, que xa non se usa);
- `cld daemon` como **servizo de systemd de usuario** (`cld.service`); sen
  systemd de usuario, arranque automático XDG (`~/.config/autostart`) e
  arráncao xa;
- acceso directo `cld` no menú e, se hai sesión gráfica, abre a web para
  vincular (`--no-open` para non abrila);
- `install.sh --uninstall` desinstala e conserva a configuración (identidade
  do equipo) e as carpetas sincronizadas.

`make installtest` ([dev/installtest.sh](dev/installtest.sh)) próbao nun
Debian 13 con systemd e un usuario normal, na rede do contorno: instalar,
servizo activo, vincular como alice desde a web (texto de vinculación,
confirmación e código), recibir ficheiros de
`/comun`, reiniciar o equipo, revogar o equipo e desinstalar. Require o
contorno en marcha con SFTPGo preparado (`make demo` ou `make fresh`).

## Pasos solos

```sh
make up
make proto ARGS="init"
make proto ARGS="sftpgo-setup"
make proto ARGS="pair st-client1 alice"      # le o código na web de pairsrv
make proto ARGS="pending st-client1"
make proto ARGS="accept st-client1 sftpgo-folder-comun"
make proto ARGS="devices st-client1"
make proto ARGS="revoke st-client1 st-client3 alice"
```
