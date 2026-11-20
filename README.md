# Syncply — nube de ficheiros lixeira para grupos de confianza (SFTPGo + Syncthing)

Syncply é unha nube de ficheiros propia para o teu grupo: as carpetas
compartidas viven nun servidor voso e aparecen en todos os vosos ordenadores
coma unha carpeta máis. Cando alguén garda ou modifica un documento, o cambio
chega só aos equipos de quen ten acceso a esa carpeta, sen enviar ficheiros
por correo nin preocuparse de cal é a última versión. Os cambios envíanse á
vez ao servidor e aos ordenadores dos compañeiros que estean conectados; se
estades na mesma oficina, pasan directamente dun equipo a outro, máis rápido
e reducindo o emprego de Internet. Tamén se pode entrar desde o
navegador para consultar ou subir ficheiros desde calquera sitio.

Por dentro, Syncply combina [SFTPGo](https://github.com/drakkan/sftpgo), un
servidor de ficheiros con acceso por SFTP e web que xestiona usuarios e
permisos, co núcleo de [Syncthing](https://syncthing.net/), que sincroniza as
carpetas cos equipos. Os usuarios instalan un cliente (`syncply`) que se
vincula co servidor e mantén os seus recursos sincronizados nunha carpeta
local.

**Para quen é.** Grupos pequenos de persoas que se coñecen e confían entre si
(colectivos, cooperativas, familias, equipos de traballo pequenos) e que queren
aloxar os seus propios ficheiros nun servidor seu, sen depender dun provedor
de nube nin manter unha plataforma pesada.

**Avantaxes.**

- **Os teus ficheiros, no teu servidor.** Ninguén alleo ao grupo garda nin
  le os vosos documentos, non pagades unha subscrición por persoa e non
  dependedes dunha gran empresa.
- **Traballas coma sempre.** Os ficheiros están nunha carpeta normal do teu
  ordenador e funcionan sen conexión; cando volves ter rede, os cambios
  envíanse sós.
- **Fácil de poñer en marcha.** Descargas o programa, pegas un texto e
  escribes un código que ves na web do servidor. Non hai que configurar nada
  máis.
- **Cada quen ve só o que lle toca.** Os permisos que xa tedes no servidor
  deciden que carpetas recibe cada persoa, e os cambios aplícanse axiña.
- **Todos os teus equipos, baixo control.** Podes usalo no portátil e no
  ordenador de casa, ver que equipos tes vinculados e retirar o acceso a un
  que perdiches ou xa non usas.
- **Rápido e económico.** Os equipos do grupo intercambian os ficheiros
  directamente entre eles, mesmo na mesma oficina, sen pasar sempre polo
  servidor: as copias chegan antes e o servidor pode ser pequeno e barato.

**Estado.** Prototipo funcional en desenvolvemento. O fluxo completo
(vincular, recibir recursos, sincronizar nos dous sentidos, revogar) está
verificado en contedores. O despregamento de produción
([deploy/](deploy/README.md)) está probado en local, aínda non nun servidor
real. Os instaladores de Linux e Windows están probados (Windows 11 nunha VM,
aínda sen asinar), e o de macOS está pendente. O plan a medio prazo
é integrar Syncply no propio SFTPGo ([PLAN-SFTPGO.md](PLAN-SFTPGO.md)).

**Limitacións.**

- Só recursos en **disco local** con **permiso de escritura completo** (non
  S3, nin cifrado de SFTPGo, nin SFTP remoto; tampouco recursos de só lectura).
- O que escribe Syncthing non pasa por SFTPGo: **non se aplican cotas, filtros
  de ficheiros nin hooks**.
- A **revogación non é instantánea** para os pares que non estean conectados
  ao servidor, e cada equipo ve os identificadores e nomes dos outros equipos
  cos que comparte recursos.
- O servidor non pode impoñer só lectura a un par, e un servidor comprometido
  podería introducir equipos alleos: o modelo presupón **confianza** no
  servidor e entre os membros do grupo.
- Pensado para **poucos usuarios e equipos**: todos conectan con todos.

Ver [CLAUDE.md](CLAUDE.md) para o contexto e as decisións do proxecto.

**Servidor en produción:** ver [deploy/README.md](deploy/README.md)
(Caddy + SFTPGo + Syncthing + pairsrv, `pairsrv setup-sftpgo`).

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
make dist-windows # instalador de Windows (syncply.exe autoinstalable)
make installtest  # proba o paquete nun Debian con systemd
make winvm-install WIN_ISO=... # instala Windows 11 na VM de probas (unha vez)
make wintest      # proba o instalador de Windows na VM
make status   # estado de dispositivos, carpetas e conexións
make help     # resto de ordes
make clean    # para os contedores e borra o estado (./.dev)
```

Que levanta `compose.yaml`:

| Servizo      | Papel                                                    |
|--------------|----------------------------------------------------------|
| `sftpgo`     | SFTPGo 2.7.6 (BD SQLite), datos en `/srv/sftpgo/data`    |
| `st-server`  | Syncthing do servidor; ve os datos de SFTPGo na mesma ruta |
| `st-client1` | Cliente de proba (alice): `syncply daemon` + o seu Syncthing; web en `127.0.0.1:8411` |
| `st-client2` | Cliente de proba (bob); web en `127.0.0.1:8412`          |
| `st-client3` | Cliente de proba (alice, segundo equipo); web en `127.0.0.1:8413` |
| `pairsrv`    | Servizo de emparellamento (`cmd/pairsrv`); web dos usuarios en `127.0.0.1:8090` |
| `tools`      | Go 1.27 para executar `cmd/stproto` e os tests           |

- A API REST de Syncthing só é accesible desde a rede interna `stnet` e con
  API key (`ST_APIKEY`, cun valor por defecto só para desenvolvemento). O
  único porto publicado é a web dos clientes `syncply`, e só en `127.0.0.1`.
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
`http://`. A única excepción é o **modo de desenvolvemento**, `SYNCPLY_DEV=1`
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
- o **texto de vinculación** (`syncply1:` + base64url dun JSON
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

## Cliente `syncply`

Un só binario ([cmd/syncply](cmd/syncply/main.go), [internal/client](internal/client)):

- `syncply daemon`: servizo de usuario. **Arranca e vixía o seu propio
  Syncthing** (proceso fillo con `--no-restart`: reinícioo ao momento se
  Syncthing pide reiniciarse e con espera crecente se falla) e serve a web
  de configuración só en `127.0.0.1` (porto libre).
  - Syncthing vai en `<dir>/syncthing` (`0700`), coa **API nun socket Unix**
    (`gui.sock`; en Windows, porto libre en loopback) e unha API key
    aleatoria gardada no estado. Busca o binario `syncthing` xunto a `syncply`
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
- `syncply open`: o que executa o **acceso directo**. Le `ui.json` (porto e
  clave de control, `0600`, só do usuario), pídelle ao daemon unha URL de
  acceso dun só uso (60 s) e ábrea no navegador. O usuario non ten que saber
  porto, enderezo nin contrasinal.
- `syncply shortcut`: crea o acceso directo `syncply` no menú de aplicacións (Linux,
  `~/.local/share/applications/syncply.desktop`). En Windows faino `syncply
  install` (ver [Instalador de Windows](#instalador-de-windows)).

Seguridade da web: só loopback; URL de acceso dun só uso → cookie de sesión
`HttpOnly`, `SameSite=Strict`; comprobación de `Host` (DNS rebinding) e de
`Origin`/`Sec-Fetch-Site` nos POST (CSRF); CSP sen JavaScript.

O cliente non ten configuración de servidor: todo vén do texto de
vinculación e da resposta de pairsrv. (As instalacións anteriores con
`syncply.json` aparecen sen vincular; volver vincular co mesmo usuario renova o
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

`make dist` xera `dist/syncply-linux-amd64.tar.gz`: `syncply` (estático),
`syncthing` 2.1.7 (o binario da imaxe oficial fixada), `install.sh` e
[LICENZAS.txt](packaging/linux/LICENZAS.txt) (Syncthing é MPL-2.0). De
momento só amd64. **Non leva configuración de ningún servidor**: o mesmo
paquete vale para todos.

[install.sh](packaging/linux/install.sh) instala **só para o usuario, sen
root**:

- binarios en `~/.local/share/syncply/bin`, estado en `~/.config/syncply`
  (`0700`; borra o `syncply.json` das versións anteriores, que xa non se usa);
- `syncply daemon` como **servizo de systemd de usuario** (`syncply.service`); sen
  systemd de usuario, arranque automático XDG (`~/.config/autostart`) e
  arráncao xa;
- acceso directo `syncply` no menú e, se hai sesión gráfica, abre a web para
  vincular (`--no-open` para non abrila);
- `install.sh --uninstall` desinstala e conserva a configuración (identidade
  do equipo) e as carpetas sincronizadas.

`make installtest` ([dev/installtest.sh](dev/installtest.sh)) próbao nun
Debian 13 con systemd e un usuario normal, na rede do contorno: instalar,
servizo activo, vincular como alice desde a web (texto de vinculación,
confirmación e código), recibir ficheiros de
`/comun`, reiniciar o equipo, revogar o equipo e desinstalar. Require o
contorno en marcha con SFTPGo preparado (`make demo` ou `make fresh`).

## Instalador de Windows

`make dist-windows` xera `dist/syncply-windows-amd64.exe`: un só ficheiro,
**autoinstalable**, co `syncthing.exe` 2.1.7 oficial dentro (comprimido con
`go:embed`, etiqueta `embedsyncthing`) e as licenzas. O zip de Syncthing
descárgase das publicacións de GitHub e verifícase co SHA-256 fixado no
[Makefile](Makefile) (o publicado en `sha256sum.txt.asc`; a sinatura GPG non
se comproba). Compílase con `-H windowsgui` (sen xanela de consola); desde
unha consola, `syncply` engánchase a ela para mostrar a saída.

Dobre clic no `.exe` descargado = `syncply install`. **Só para o usuario, sen
administrador** ([install_windows.go](cmd/syncply/install_windows.go)):

- binarios en `%LOCALAPPDATA%\Programs\syncply`, estado en `%LOCALAPPDATA%\syncply`
  (non en `Roaming`: a identidade do equipo non debe viaxar co perfil),
  rexistro do daemon e de Syncthing en `%LOCALAPPDATA%\syncply\syncply.log`;
- arranque ao iniciar sesión con `HKCU\…\Run`; acceso directo `syncply` no menú
  Inicio (`syncply open`, creado con PowerShell/`WScript.Shell`); entrada en
  «Aplicacións instaladas» (`HKCU\…\Uninstall\syncply`) que executa `syncply
  uninstall`;
- arranca o daemon e abre a web para vincular. Se xa había un daemon,
  pídelle que se peche (`POST /_control/shutdown` coa clave de `ui.json`) e
  agarda antes de substituír os binarios: serve tamén para actualizar;
- o daemon é **único por estado** (mutex con nome); `Syncthing` arráncase sen
  xanela (`CREATE_NO_WINDOW`), coa API en loopback (porto libre + API key);
- o acceso directo volve arrancar o daemon se non está en marcha; os erros
  sen consola móstranse nunha caixa de diálogo;
- `syncply uninstall` (pregunta; `-quiet` non) quita todo isto e conserva o
  estado e as carpetas. O `syncply.exe` en execución bórrao un `cmd` ao rematar.

Ao rematar Syncthing (pechar o daemon, desinstalar, actualizar), syncply mata
se fai falta **toda a árbore** de procesos: Syncthing 2.x lanza un proceso
fillo e en Windows matar o pai deixábao orfo, sincronizando e bloqueando
`syncthing.exe`. En Windows vai nun *Job Object* con `KILL_ON_JOB_CLOSE` (se o
daemon morre, Syncthing tamén); en Linux, nun grupo de procesos.

### Probas en Windows (`make wintest`)

Probado nunha VM con Windows 11 Enterprise (avaliación, 26H2, es-ES) como
usuaria **sen administrador**. A VM ([dev/winvm/](dev/winvm/)) é QEMU/KVM
dentro dun contedor Podman conectado á rede do contorno (chega a `pairsrv` e
`st-server` polos seus nomes); o disco queda en `.winvm/` (~20 GB).

```sh
make winvm-install WIN_ISO=/ruta/Win11_Enterprise_Eval.iso   # unha vez, 30-40 min, desatendida
make fresh && make dist-windows && make wintest                # ~5 min
```

- `winvm-install`: instalación desatendida ([autounattend.xml](dev/winvm/autounattend.xml):
  sen TPM nin arranque seguro, usuarios `admin` e `ana`, contrasinal
  `syncply`, `ana` entra soa) e un axente PowerShell ([agent.ps1](dev/winvm/agent.ps1))
  que executa na sesión de `ana` as ordes de [winvm.py](dev/winvm/winvm.py).
- `wintest` ([dev/wintest.sh](dev/wintest.sh)) parte sempre dunha copia nova
  do Windows instalado: descargar de pairsrv, instalar, vincular como alice
  (texto + código), recibir e enviar ficheiros, reiniciar Windows, revogar,
  matar o daemon (sen Syncthing orfo; `syncply open` volve arrancalo) e
  desinstalar. `KEEP=1` deixa a VM acesa ao rematar.
- Durante a proba: VNC en `127.0.0.1:5900`; capturas e teclado/rato con
  `podman exec syncply-winvm python3 /winvm/winvm.py shot /work/x.png | key alt-tab | click X Y`.

Visto a man co dobre clic (ficheiro marcado como descargado de Internet):

- **SmartScreen** bloquea o `.exe` sen asinar («Windows protegió su PC»,
  «Editor desconocido»); hai que premer «Más información» → «Ejecutar de todas
  formas».
- Ao arrancar Syncthing, o **Firewall de Windows** pregunta por «Syncthing -
  Open Source Continuous File Synchronization» (non por Syncply). «Permitir»
  pide o contrasinal dun administrador; se se cancela, as conexións saíntes
  (servidor, relay) funcionan, pero non as entrantes nin o descubrimento local.
- Logo a mensaxe «Syncply instalado» e, ao aceptala, Edge coa páxina
  «Vincular este equipo». (Antes a mensaxe quedaba agochada detrás do
  navegador ou do aviso do Firewall; agora vai primeiro e `MB_TOPMOST`.)
- Reinstalar nun equipo revogado: a web mostra a conta e os recursos de antes
  con «Sen conexión co servidor», sen dicir que foi revogado.

Pendente: asinar o `.exe`; icona e metadatos (recurso `.syso`); arm64;
decidir que facer co aviso do Firewall; avisar na web dun equipo revogado.

## Pasos solos

```sh
make up
make proto ARGS="init"
make proto ARGS="sftpgo-setup"               # o mesmo que `pairsrv setup-sftpgo` + datos de proba
make proto ARGS="pair st-client1 alice"      # le o código na web de pairsrv
make proto ARGS="pending st-client1"
make proto ARGS="accept st-client1 sftpgo-folder-comun"
make proto ARGS="devices st-client1"
make proto ARGS="revoke st-client1 st-client3 alice"
```
