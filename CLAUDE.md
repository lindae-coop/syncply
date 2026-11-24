# Proxecto Syncply: nube de ficheiros lixeira con SFTPGo + Syncthing

Contexto para continuar en Claude Code. Idioma de traballo co usuario: galego.

Repositorio: https://github.com/lindae-coop/syncply (antes `cld`; o binario do cliente chámase `syncply`).

## Obxectivo

Engadir a SFTPGo capacidades de sincronización con clientes usando o núcleo de Syncthing (sen a súa interface), para ter unha nube de ficheiros lixeira para **grupos pequenos de usuarios de confianza**.

## Requisitos (ditos polo usuario)

### Servidor
- SFTPGo incorpora o núcleo de Syncthing (idea orixinal: usar o seu código; sen GUI).
- SFTPGo comparte mediante Syncthing as carpetas segundo os **permisos dos usuarios**.
- **Decisión actual:** por agora só se ofrecen recursos nos que o usuario ten **permisos de escritura**.
- **Decisión (integración en SFTPGo):** os usuarios que non poden entrar no WebClient (HTTP denegado, tamén por grupo, ou sen método de acceso web) quedan **excluídos** de Syncthing, porque non poden ver o código de vinculación.
- Cada usuario ten un **OTP baseado en tempo (TOTP)** na súa conta. **Aclaración do usuario:** o código vese **na propia aplicación do servidor** (unha páxina co código e un contador), non nunha app autenticadora externa. Non ten nada que ver co 2FA de SFTPGo.

### Cliente
- Os usuarios que accedan a SFTPGo atopan un **cliente para descargar** coas claves do servidor preinstaladas, e un contador/OTP.
- Ao instalar o cliente, o usuario introduce o **código OTP da súa conta** para vincularse.
- Despois da instalación, hai unha **app de configuración** onde o usuario pode: pausar temporalmente a sincronización, detela ou arrancala; ver a **lista de recursos ofrecidos polo servidor**; decidir en que **carpeta local** se sincroniza cada un.
- Un usuario pode instalar o cliente en **varios equipos**. En cada cliente debería ver **cantos equipos ten validados** e, idealmente, poder **revogar outros equipos**.
- **Requisito importante:** sincronización **entre pares (P2P)** e uso do **introducer** de Syncthing: os clientes deben recibir de SFTPGo a lista de pares autorizados en cada recurso e seguir os seus cambios (altas e baixas).

## Ideas e conclusións da conversa (suxestións, non decisións fixadas)

Verificar sempre contra a versión concreta de Syncthing/SFTPGo que se use.

### Enfoque recomendado para empezar
Prototipo con **Syncthing como proceso separado** controlado pola súa REST API desde SFTPGo (hooks/código), antes de fusionar o núcleo no binario de SFTPGo. Menos acoplamento e menos mantemento ao actualizar ambos. Se o prototipo non chega, entón embeber a lib de Syncthing (ambos son Go).

Licenzas: Syncthing é MPL-2.0 e SFTPGo é AGPL-3.0; compatibles, pero o resultado combinado debería ir en AGPL (revisar).

### Interface de Syncthing
- A API REST e a UI web comparten o listener `gui`: `gui.enabled=false` quita ambas. Non serve para o enfoque por API.
- Opcións: ligar o GUI a un **socket Unix** (`unix:///.../syncthing.sock`) ou a `127.0.0.1`, **API key obrigatoria**, non expor o porto (8384), non usar `insecureAdminAccess`. Opcionalmente compilar con `-tags noassets` (verificar que segue vixente).
- No cliente, igual: Syncthing como servizo/daemon con GUI só en loopback ou socket, e a app de configuración como cliente da API local.

### Cliente
- Daemon de Syncthing (servizo de usuario: systemd user, LaunchAgent, tarefa de inicio en Windows) + app de configuración (nativa mínima en Go con Fyne/Wails ou Tauri, ou icona de bandexa tipo Syncthing-Tray, ou páxina web local en loopback).
- Pausar/retomar vía `/rest/system/pause` e `/rest/system/resume` (ou por dispositivo/carpeta); cambiar `path` das carpetas vía `/rest/config/folders`.
- Desactivar o actualizador automático de Syncthing para controlar a versión.

### Lista de recursos
Non fai falta un endpoint propio: cando o servidor comparte unha carpeta cun dispositivo, o cliente a ve como **carpeta pendente** (`GET /rest/cluster/pending/folders`) con ID e etiqueta. A app lista esas ofertas, o usuario elixe directorio local e acepta creando a carpeta co mesmo ID (`/rest/config/folders`). Require que o dispositivo do cliente xa estea engadido e conectado ao servidor (paso do OTP). A oferta non leva información de permisos.

### Emparellamento con OTP
O usuario entra na web do servidor co seu usuario e contrasinal e ve alí o código (cambia cada 30 s). O cliente envía ese código + o seu Device ID; o servidor valida o OTP, autoriza ese dispositivo para as carpetas do usuario e devolve o seu propio Device ID. O OTP só vale para vincular; despois a identidade é o certificado do dispositivo.

### Varios equipos e revogación
- Syncthing non sabe que dous Device IDs son do mesmo usuario: o rexistro `usuario → dispositivos` (Device ID, nome, data de alta, última conexión) vive no servidor. Aquí si fai falta un endpoint propio ("os meus equipos"), autenticado co token do emparellamento ou co certificado do dispositivo.
- Revogar: o servidor elimina o dispositivo da configuración de Syncthing (`DELETE /rest/config/devices/{id}`) e da súa táboa. Suxestión: pedir confirmación (novo OTP) e permitir límite de equipos por usuario.

### Introducer / P2P
- O cliente engade o servidor con `introducer: true`. O servidor comparte cada carpeta con todos os dispositivos dos usuarios autorizados; ao aceptar a carpeta, o cliente recibe os pares que a comparten xa dados de alta. Os cambios propáganse cando o cliente se conecta ao servidor. As baixas aplícanse mentres non se active `skipIntroductionRemovals`. Os dispositivos introducidos levan `introducedBy`.
- Riscos/puntos a deseñar:
  - **Revogación non instantánea:** un equipo revogado segue sendo coñecido polos clientes ata que estes se conecten ao servidor.
  - **Descubrimento/relays:** pares introducidos usan enderezo "dynamic"; para sistema pechado, considerar `stdiscosrv` e `strelaysrv` propios ou desactivar o descubrimento público.
  - **Privacidade:** cada cliente ve Device IDs e nomes dos equipos con recursos en común.
  - **Confianza transitiva:** un servidor comprometido pode introducir calquera dispositivo.
  - **Permisos:** o tipo de carpeta (sendreceive/receiveonly) defínese en cada extremo; o servidor non pode impoñer só-lectura a un par.
  - **Escala:** conexións cruzadas entre todos; ben para grupos pequenos.

### Convivencia SFTPGo + Syncthing sobre o mesmo disco
- Syncthing detecta os cambios feitos por SFTPGo (watcher + reescaneo). Latencia por `fsWatcherDelayS` (10 s por defecto) e `rescanIntervalS` (1 h); opcionalmente un hook de SFTPGo pode forzar `POST /rest/db/scan?folder=...`.
- Activar subidas atómicas (subir a temporal e renomear) en SFTPGo para evitar rehash de ficheiros a medias.
- Mesmo UID/GID ou permisos compartidos (especialmente en Docker).
- Só vale con **disco local** (non S3, nin cifrado de SFTPGo, nin SFTP remoto).
- O que escribe Syncthing non pasa por SFTPGo: non se aplican cotas nin hooks, e as estatísticas de cota quedan desfasadas ata recalcular.
- Os usuarios verán por SFTP `.stfolder`, temporais e `.sync-conflict-*`; filtrar en SFTPGo.

## Migracións entre versións (obrigatorio desde v0.1.0)

Hai instancias en produción (decisión do usuario, 2026-11-21): **cada versión ten que incluír as migracións necesarias** para que unha instalación da versión anterior siga funcionando ao actualizar, sen pasos manuais nin perder datos. Aplícase a todo o que persiste: estado de pairsrv (equipos, segredos TOTP…), `pairsrv.json`, estado do cliente `syncply` nos equipos dos usuarios, configuración que se aplica aos Syncthing, obxectos creados en SFTPGo (grupo `syncthing`, regra `pairsrv-sync`) e `deploy/` (`.env`, volumes, compose). Os clientes poden actualizarse despois ca o servidor: manter a compatibilidade da API pairsrv ↔ cliente. Probar a actualización desde a versión anterior e documentar nas notas da versión o que cambia.

## Contorno de desenvolvemento (decidido)

- Todo en **Podman rootless** + `podman-compose` + `make`; non instalar nada máis no equipo (nin Go nin Syncthing). Ver [README.md](README.md).
- Versións fixadas: Syncthing **2.1.7** (`docker.io/syncthing/syncthing:2.1.7`), SFTPGo **2.7.6** (`docker.io/drakkan/sftpgo:v2.7.6-alpine`, BD SQLite, esquema v33), Go **1.27.4** (contedor `tools`).
- **pairsrv le directamente a BD de SFTPGo** (usuarios, grupos, cartafoles e permisos), reproducindo a súa lóxica en `internal/sftpgo`. Decisión do usuario: o acoplamento non preocupa porque o obxectivo final é integrar o código en SFTPGo. Revisar `internal/sftpgo` ao cambiar de versión de SFTPGo.
- Recursos ofrecidos: directorio persoal e cartafoles virtuais en disco local con **permiso de escritura completo** (ver README).
- API de Syncthing só na rede interna dos contedores, sen portos publicados, con API key.

## Achados verificados (Syncthing 2.1.7, `make demo`)

- O fluxo **pending folders → aceptar co mesmo ID** funciona; a oferta chega en ~2 s tras emparellar.
- **Introducer: hai que forzar a reconexión tras aceptar.** O servidor non reenvía a súa ClusterConfig cando o cliente acepta a carpeta, e o introducer só presenta pares en carpetas xa compartidas con el. Sen reconexión, o último cliente en aceptar non coñece os pares existentes ata a seguinte reconexión. Solución no prototipo: `pause`/`resume` do dispositivo servidor no cliente (`stapi.Reconnect`). Os clientes xa existentes si reciben o novo par ao momento (o cambio de config no servidor reenvía a ClusterConfig).
- Cos pares introducidos (`dynamic`) e só **descubrimento local** (global e relays desactivados), os clientes conectan directamente entre eles (P2P).
- **Revogación:** `DELETE /rest/config/devices/{id}` no servidor quítao tamén das carpetas; os clientes conectados eliminan o par introducido ao momento e o revogado queda sen conexión co servidor e cos pares. A súa config local conserva as entradas (non se pode evitar).
- `/rest/system/connections` pode mostrar `connected: true` un instante en conexións que logo se rexeitan; non fiarse dunha soa mostra.
- Latencia de sincronización observada ~14 s (`fsWatcherDelayS` = 10 s por defecto).
- **pairsrv** (`cmd/pairsrv`, `internal/pairing`, `internal/sftpgo`, `internal/totp`) valida o fluxo de punta a punta co código de vinculación lido da web de pairsrv (como faría o usuario): emparellamento, ofertas só das carpetas permitidas, varios equipos por usuario, listaxe de equipos e revogación desde outro equipo do mesmo usuario. O equipo revogado queda sen conexións en ~4 s (antes pode aparecer aínda como conectado).

## Achados verificados (SFTPGo 2.7.6)

- `GET /api/v2/user/token` con Basic auth (+ `X-SFTPGO-OTP` se o usuario ten 2FA) serve para comprobar o contrasinal dun usuario desde pairsrv (401 se é incorrecto).
- (Xa non se usa: antes pairsrv validaba co TOTP do 2FA de SFTPGo, descifrando `users.filters` → `totp_config.secret`, `Secretbox` do KMS local. Ver o historial de git se fai falta.)
- Ficheiros subidos pola API de SFTPGo chegan aos clientes (~12 s); o que escriben os clientes aparece no disco de SFTPGo.
- Desactivar un usuario en SFTPGo quítalle o acceso aos seus equipos no seguinte ciclo de reconciliación; reactivalo devólvello sen emparellar de novo.
- **Xestor de eventos:** unha regra de provedor (`add`/`update`/`delete` sobre `user` e `group`) cunha acción HTTP avisa a pairsrv e o cambio aplícase en ~1 s. Os **cartafoles non xeran eventos de provedor** (`provider_objects` non inclúe `folder`): un cambio de `mapped_path` só chega coa reconciliación periódica. Non hai restricións para chamar a hosts internos.
- **Ocultar ficheiros de Syncthing:** filtros `file_patterns` con `deny_policy: 1` en `/`. Os de `/` só se herdan de grupos **primarios** (os secundarios ignóranos), por iso o grupo primario `syncthing`.
- **Acceso ao WebClient** (`checkHTTPClientUser` en `httpd/api_utils.go`): rexéitase con `HTTP` en `denied_protocols` ou `password` en `denied_login_methods` (salvo OIDC); os grupos primario e secundarios **engaden** as súas listas ás do usuario; os anónimos teñen HTTP denegado. pairsrv exclúe eses usuarios no `Snapshot` (como os desactivados: os seus equipos quedan sen acceso).
- Limitacións coñecidas: os filtros de ficheiros (patróns permitidos/denegados), as cotas e os hooks de SFTPGo non se aplican ao que escribe Syncthing; `.stfolder` e temporais de Syncthing vense desde SFTPGo.

## Próximos pasos propostos

1. ~~Prototipo mínimo~~ e ~~validar fluxo OTP, pending folders, propagación de pares e baixas~~ (feito). Cliente: API de Syncthing en socket Unix (feito; en desenvolvemento segue en TCP interno). Servidor: feito en `deploy/` (produción).
2. ~~Integrar con SFTPGo~~ (feito lendo a BD), ~~avisos do xestor de eventos~~ e ~~ocultar os ficheiros de Syncthing~~ (grupo primario `syncthing`).
3. Deseñar o modelo de datos do servidor (usuarios, dispositivos, carpetas, permisos) e a súa tradución á configuración de Syncthing.
4. Listar os endpoints REST de Syncthing necesarios.
5. Decidir despois se se fusiona o núcleo en SFTPGo ou se mantén o proceso separado.
6. Integración en SFTPGo, fase 1 (fork de 2.7.6, Syncthing como proceso supervisado, estado en táboas propias `syncply_*`, un nodo; varios nodos máis adiante): ver [PLAN-SFTPGO.md](PLAN-SFTPGO.md). Os plugins de SFTPGo (notifier, auth, kms, eventsearcher, ipfilter) non poden engadir páxinas nin filtrar ficheiros, por iso fai falta o fork.

## Despregamento de produción (`deploy/` verificado en local; stack do hosting verificado no servidor real)

- `deploy/compose.yaml`: Caddy (HTTPS automático, dous dominios: pairsrv e SFTPGo) + SFTPGo + Syncthing (API só en socket Unix no seu volume, montado en pairsrv só lectura) + pairsrv compilado (`deploy/Containerfile.pairsrv`) **A imaxe de pairsrv leva dentro os paquetes do cliente** (`/descargas`, Linux e Windows, os mesmos que `make dist`/`make dist-windows`): os usuarios descárganos do servidor, non de GitHub (decisión do usuario). Ao cambiar o empaquetado, cambialo nos dous sitios. Todos co UID/GID 1000. `./init.sh DOM_SYNCPLY DOM_SFTPGO` xera `.env` (segredos) e `pairsrv.json`; `make up`; `make setup-sftpgo`.
- **Versións:** etiqueta `vX.Y.Z` → `.github/workflows/release.yml` publica `ghcr.io/lindae-coop/syncply-pairsrv:vX.Y.Z` e unha versión de GitHub cos paquetes (copia). Verificado con `v0.1.0` (CI ~2 min; o paquete de ghcr creouse privado e houbo que facelo público a man en *Package settings*: a API non o permite; despois descárgase sen autenticar).
- **Hosting da cooperativa** (`hosting-podman`, stack `stacks/syncply`): dominios (decisión do usuario) `syncply.lindae.eu` (vinculación: pairsrv, Syncthing, descubrimento e relay) e `nube.lindae.eu` (SFTPGo: acceso aos ficheiros); SFTPGo propio sen SFTP (o 2022 é do stack `ficheiros`); proxy do hosting por etiquetas; `discosrv` na rede do host para ver a IP real dos equipos (sen verificar). Ver `docs/decisions.md` §10 dese repo. **Verificado no servidor real (v0.1.0):** vincular, sincronizar e ofrecer un cartafol virtual dun grupo secundario a dous usuarios. Trampa vista: un cartafol virtual engadido a un grupo sen *virtual path* non se garda na asignación (`groups_folders_mapping` baleira) e non se ofrece; para diagnosticar, ler a BD con `sqlite3 -readonly` montando o volume `syncply_sftpgo` en só lectura.
- `pairsrv setup-sftpgo` (`internal/sftpgo/setup.go`) crea o grupo `syncthing` e a acción+regra `pairsrv-sync`; `stproto sftpgo-setup` usa o mesmo código. `serverSyncthingOptions` en `pairsrv.json` aplícase ao Syncthing do servidor en cada arranque.
- **P2P (decisión do usuario): o noso servidor fai de descubrimento e relay.** `stdiscosrv` (8443, imaxe `syncthing/discosrv:2.1.7`) e `strelaysrv` (22067, `syncthing/relaysrv:2.1.7`, con `-token`, sen pool público nin servizo de estado), cos seus propios certificados. Sección `p2p` de `pairsrv.json`: pairsrv le os `cert.pem`, calcula o Device ID (`pairing.DeviceIDFromPEM`, verificado contra `syncthing device-id`) e engade ás opcións dos clientes `globalAnnounceServers` (`?id=`), `listenAddresses` (tcp, quic e `relay://…?id=…&token=…`, sen `default`), `relaysEnabled`, `natEnabled`. Sen `p2p`: sistema pechado (P2P só en LAN). As `syncthingOptions` chegan aos clientes ao vincular ou con «Actualizar a conexión».
- Verificado en local: dous clientes sen descubrimento local e sen conexión directa posible atópanse no noso discosrv e conectan polo noso relay (`relay-client`/`relay-server`).
- **Apoio público (decisión do usuario, opción de configuración):** `p2p.publicDiscovery` engade `"default"` a `globalAnnounceServers` (despois do noso; `true` en `init.sh`) e `p2p.publicRelays` engade `dynamic+https://relays.syncthing.net/endpoint` a `listenAddresses` (`false` en `init.sh`: cada relay é unha escoita á parte coa mesma prioridade, así que un par pode usar o público aínda que o noso funcione). Verificado en local (cos nosos inaccesibles e escoita só en relays): os clientes anúncianse en `discovery-announce-v4`, atópanse con `discovery-lookup` e conectan por un relay público. Explicado na FAQ da web (`#servizos-publicos`).
- `stproto pair` (o que usa `make demo`) non aplica as `syncthingOptions` de pairsrv; só as aplica `syncply`. Para probar opcións nos clientes da demo, `PATCH /rest/config/options` **sen reiniciar** Syncthing: o daemon `syncply` volve aplicar as súas ao arrancalo.
- Achados: un volume novo compartido (`data`) pode quedar de root aínda que a imaxe de SFTPGo o teña de 1000 → SFTPGo non crea os directorios persoais; a entrada do contedor de Syncthing fai `chown 1000:1000` da raíz de datos. SFTPGo crea o directorio persoal ao entrar (SFTP/web). Nota anterior: «non co token da API REST»; pero en 2.7.6 viuse que `GET /api/v2/user/token` (o que usa a web de pairsrv) si crea o persoal e os cartafoles (ver o panel de administración). A imaxe de Syncthing non define healthcheck en formato OCI; desactívase igualmente.
- Verificado con `*.localhost` e a CA interna de Caddy (cliente con `SSL_CERT_FILE`, sen `SYNCPLY_DEV`): vincular por HTTPS, aviso SFTPGo → pairsrv (202), sincronización nos dous sentidos (~16 s).

## Web de pairsrv: código de vinculación (verificado con `make demo`)

- Na raíz de pairsrv o usuario entra co usuario e contrasinal de SFTPGo (comprobados coa API de SFTPGo) e ve o **código de vinculación** cun contador (barra CSS + recarga automática), o texto de vinculación, a descarga do cliente, os recursos ofrecidos e os seus equipos (N de máximo).
- O código é un TOTP (SHA1, 6 díxitos, 30 s, ±1 paso) cun **segredo por usuario xerado por pairsrv** e gardado no seu estado; créase a primeira vez que se mostra. Se o código actual xa se usou, a web mostra o do seguinte paso (xa aceptado), así que sempre mostra un código válido.
- **Revogar desde a web de pairsrv** (pedido polo usuario): botón «Revogar…» en cada equipo → páxina de confirmación (sen `meta refresh`) → `Service.RevokeByUser` (sen OTP: a sesión xa vén do contrasinal de SFTPGo). Verificado en `make up`: o servidor corta a conexión ao momento e os outros equipos quitan o par introducido.
- O mesmo código serve para vincular e para revogar desde o cliente.
- Ligazón **«Os meus ficheiros»** ao WebClient de SFTPGo (pedido polo usuario): `web.filesURL` ou, se falta, `SYNCPLY_FILES_URL` (migración: `deploy/compose.yaml` constrúea con `SFTPGO_DOMAIN`; o stack do hosting ten que definila ou engadir `web.filesURL`). En desenvolvemento SFTPGo publícase en `127.0.0.1:8091`. Os usuarios xa non necesitan activar o 2FA de SFTPGo; se o teñen, a web pídeo para entrar.
- En desenvolvemento: `make web` (`127.0.0.1:8090`); `make otp U=alice` e `make blob U=alice` entran na web co contrasinal de proba e devolven o mesmo código e texto.

## Panel de administración de pairsrv (`/admin/`, verificado contra SFTPGo 2.7.6)

- Pedido polo usuario: crear usuarios e contrasinais, cambiar contrasinais, crear grupos cun **único recurso** cada un, asignar usuarios a grupos e cambiar a pertenza; usuarios **sen carpeta persoal**; acceso coas **credenciais do administrador de SFTPGo**.
- Implementado: `internal/pairing/admin.go` (web, `AdminBackend`/`AdminSession`) + `internal/sftpgo/panel.go` (API REST de SFTPGo co token do admin, non a BD). Engadido tamén: borrar usuarios e grupos (confirmando co nome), listaxe con recursos e número de equipos, e `Service.ForgetUser` (ao borrar ou crear un usuario no panel, pairsrv esquece os seus equipos e segredo: senón, un usuario novo co mesmo nome herdaría os equipos do borrado; borrar en SFTPGo directamente segue deixando o rexistro).
- Usuario do panel: `home_dir` `<admin.homesDir>/<nome>` con `/`: `list`, `download`; primario `syncthing`; grupos secundarios. Grupo do panel: cartafol co mesmo nome en `<admin.groupsDir>/<nome>`, montado en `/<nome>` con `virtual_path` e `user_settings.permissions` `{"/<nome>": ["*"]}`. Só se borran desde o panel os grupos con ese formato e sen membros. O panel só cambia grupos secundarios.
- `admin` en `pairsrv.json` é opcional (activo por defecto; `homesDir` `/srv/sftpgo/data/usuarios`, `groupsDir` `/srv/sftpgo/data/grupos`): as instalacións anteriores reciben o panel sen tocar nada.
- Verificado en `make fresh` contra SFTPGo real: entrar (token de admin con `expires_at`, 20 min), crear grupo e usuario (só se lle ofrece o recurso do grupo, non o persoal), cambio de contrasinal (o vello dá 401), cambiar grupos dun usuario con equipo (o servidor engade/quita a carpeta en ~1 s polo aviso de eventos), negativas (grupo con membros, grupo non creado polo panel), borrar grupo e usuario (o equipo sae do Syncthing do servidor). O `PUT /users/{nome}` co obxecto que devolve o `GET` funciona (sen `password` mantén o actual).
- SFTPGo crea os directorios do usuario e dos seus cartafoles cando entra (tamén co `GET /api/v2/user/token` da web de pairsrv). Se un recurso se comparte antes de que exista no disco, **o Syncthing do servidor crea a ruta** (visto con 2.1.7).

## Cliente `syncply` (prototipo)

- Decidido: app de configuración como **web local** servida por `syncply daemon` en `127.0.0.1`; acceso sen coñecer detalles mediante un **acceso directo** que executa `syncply open` (URL dun só uso → cookie de sesión). Ver README.
- Verificado a man contra o contorno: vincular co OTP desde a web, ver recursos, aceptar nunha carpeta escollida, deixar de sincronizar, pausar/retomar, listar equipos, rexeitamento CSRF.
- Tras "deixar de sincronizar", a oferta tarda ~20 s en volver (reconexión co servidor).
- Vinculación probada polo usuario nun navegador real.
- `syncply daemon` arranca e supervisa o seu Syncthing (verificado: Deter para o proceso e o estado mantense tras reiniciar; Arrancar volve conectar; ao pechar o daemon, Syncthing remata limpo; modo por defecto con API en socket Unix e API key xerada). Syncthing 2.1.7 crea dous procesos (pai e fillo) mesmo con `--no-restart`; paran os dous. `make demo` pasa completo cos clientes executando `syncply` co seu Syncthing (verificado polo usuario).
- **Instalador de Linux** (`make dist`, `packaging/linux/install.sh`): por usuario sen root, servizo de systemd de usuario (ou autostart XDG), acceso directo. Verificado con `make installtest` en Debian 13 con systemd: instalar, vincular desde a web, recibir ficheiros, arranque tras reiniciar, desinstalar; e a man o camiño sen systemd. Syncthing 2.1.7 ten `syncthing device-id --home=…` para ver o Device ID sen arrancalo.
- **Actualización en tempo real da web local** (verificado contra Syncthing 2.1.7 e en Chromium con Puppeteer, `docker.io/zenika/alpine-chrome:124-with-puppeteer`): `internal/client/live.go` (long polling de `/rest/events` → `Hub` con retardo de 0,5 s, 2 s para os de sincronización → SSE `GET /events`) + `static/live.js` (rexións `data-rexion`, `#vivo` con `data-on`/`data-every`). Só no cliente, sen cambios en pairsrv. Medido: oferta tras vincular ~2 s; ficheiro escrito no servidor ou nun par ~13 s (case todo `fsWatcherDelayS` de quen escribe); Deter/Arrancar ~0,5 s. Os IDs de evento volven empezar ao reiniciar Syncthing: detéctase polo erro de conexión ou por `startTime`. O `http.Server` do daemon usa `BaseContext` para cortar as conexións SSE ao pechar. `chromium --dump-dom --timeout` non agarda (volca en ~2 s): para probar JS, Puppeteer.
- **Non usar `Referrer-Policy: no-referrer`** coa comprobación de `Origin`: o navegador envía `Origin: null` nos POST á mesma orixe e rexéitanse os formularios propios. Usar `same-origin`.
- Pendente:
  - **Instalador de Windows: probado en Windows 11** (VM, usuaria sen admin; `make winvm-install WIN_ISO=…` unha vez e `make wintest`; ver README). Falta asinalo, icona, decidir que facer co aviso do Firewall e avisar na web dun equipo revogado. Instalador de macOS (LaunchAgent, app). Paquete arm64 (descargar e verificar o binario de Syncthing das súas publicacións). Icona na bandexa (require CGO en Linux/macOS).
  - Cambiar a carpeta local dun recurso xa aceptado (hoxe: deixar de sincronizar e volver aceptar).

## Cliente xenérico + texto de vinculación + OTP (implementado e verificado)

Motivo: un instalador con configuración por servidor dentro rompe a sinatura Authenticode en Windows (cada operador tería que reconstruír e asinar). Descartados: ficheiro `syncply.json` ao lado do `.exe` (pérdese se se executa desde o zip) e etiquetar o binario asinado (`certificate_tag` de Omaha).

- Formato (`internal/pairing/linktext.go`): `syncply1:` + base64url de `{"v":1,"url","user","server"}`. Sen segredos; o OTP é o que autoriza.
- pairsrv: a web mostra o texto (pasos: descarga, texto + Copiar, código); `web.publicURL` obrigatoria; `syncthingOptions` na configuración de pairsrv e na `PairResponse`. Botón Copiar: `/static/copy.js`, CSP `script-src 'self'`; sen JS, o `textarea` de só lectura.
- syncply: sen `Config`/`syncply.json` nin `-config`. Valores seguros compilados (`client.DefaultSyncthingOptions`) aplicados sempre e, por riba, os de pairsrv gardados no estado. Web en dous pasos: pegar texto (`POST /pair/check`: validación + comprobación de `/healthz`) → confirmación «Vas vincular con <host> como <usuario>» + OTP + nome (`POST /pair`; se falla, volve á confirmación). Rexeita se pairsrv responde con outro Device ID. "Actualizar a conexión" en "Os meus equipos" (texto novo + OTP, só mesmo servidor e usuario) para cambios de enderezo.
- **HTTPS (decidido): pairsrv vai detrás dun proxy; os clientes validan coas CA do sistema, sen fixar certificado** (o pin era sobreenxeñería e rompía coas renovacións do proxy). `web.publicURL` ten que ser `https://`; pairsrv non arranca e o cliente rexeita o texto se é `http://`, agás con **`SYNCPLY_DEV=1`** (modo de desenvolvemento: tamén cookies sen `Secure`). En compose está en todos os contedores; en `make installtest`, como drop-in do servizo de usuario.
- Verificado: `make test`, `make demo` e `make installtest` (vincula co texto de `make blob U=alice` + `make otp`).
- Instalacións anteriores: aparecen sen vincular (o estado non ten `pairsrvURL`); volver vincular renova o token. `install.sh` borra o `syncply.json` vello.

Para máis adiante (non agora): identificador aleatorio que caduque dentro do texto (evitaría que quen coñeza o usuario poida bloquear o seu emparellamento con OTP falsos, risco que xa existe hoxe); ligazón `syncply://…` desde a web que encha a primeira casiña.

Windows (`syncply.exe` autoinstalable, probado en Windows 11): `syncply.exe` autoinstalable (`syncply install`, Syncthing embebido con `go:embed`, `GOOS=windows` sen CGO) asinado con `osslsigncode` desde Linux; alternativas NSIS (`makensis`) ou MSI con `wixl`. Arranque sen admin con `HKCU\…\Run` ou acceso directo no cartafol de Inicio (unha tarefa programada ao iniciar sesión adoita pedir elevación). MSIX descartado. Certificado de asinamento: decisión do usuario.

## Achados verificados en Windows 11 (VM, `make wintest`)

- Contorno: QEMU/KVM nun contedor Podman na rede `syncply_stnet` (`dev/winvm/`), instalación desatendida (LabConfig sen TPM/arranque seguro; grupos locais co nome en castelán: `Administradores`, `Usuarios`), axente PowerShell na sesión de `ana` que consulta a `10.0.2.2:8000`. Windows resolve `pairsrv`/`st-server` co DNS do contedor (`dnssearch=dns.podman` + `AllowUnqualifiedQuery`). Instalar leva ~33 min; cada proba usa unha copia (`test.qcow2` sobre `base.qcow2`).
- Funciona sen admin: instalar, `HKCU\…\Run`, «Aplicacións instaladas», acceso directo, vincular, sincronizar nos dous sentidos, arranque tras reiniciar, revogar, desinstalar.
- **Erro corrixido:** Syncthing 2.x crea un proceso fillo; en Windows `Kill()` só mataba o pai e o fillo quedaba orfo (sincronizando e bloqueando `syncthing.exe`: a desinstalación fallaba con «Acceso denegado»). Pasaba cando Syncthing tardaba máis de 15 s en pechar (equipo recén revogado, en bucle de reconexións). Agora: *Job Object* con `KILL_ON_JOB_CLOSE` en Windows e grupo de procesos (`Setpgid`) en Linux (`internal/client/proc_*.go`).
- Dobre clic: SmartScreen («Editor desconocido», «Ejecutar de todas formas»); aviso do Firewall para «Syncthing - Open Source…» ao escoitar en `:22000`, e «Permitir» pide credenciais de admin. A mensaxe do instalador quedaba detrás de Edge/Firewall: agora vai antes do navegador e con `MB_TOPMOST`.
- Antes de aplicar as opcións de pairsrv, o Syncthing do cliente usa uns segundos o descubrimento global e os relays públicos (visto no log en Windows; tamén pasará en Linux).
- `Invoke-WebRequest` de PowerShell 5.1 truncou unha descarga de 24 MB na VM; `curl.exe` (incluído en Windows) non. Un `Start-Process` dun ficheiro con marca de Internet desde un proceso oculto bloquéase no diálogo de SmartScreen: lanzalo con `explorer.exe`.

## Funcionalidades desexables do cliente (pedidas polo usuario, sen deseñar)

- **Escoller a carpeta local visualmente:** botón «Escoller…» xunto ao campo da ruta ao aceptar un recurso (no canto de escribila ou ademais), e tamén para a futura opción de cambiar a carpeta dun recurso xa aceptado. Ideas sen verificar: o navegador non dá rutas absolutas (`<input type="file" webkitdirectory>` non serve), así que ou o daemon abre o diálogo nativo do sistema na sesión do usuario (Linux: `zenity`/`kdialog`/portal XDG `FileChooser`; Windows: `IFileOpenDialog` con `FOS_PICKFOLDERS`, sen CGO vía COM ou PowerShell) e devolve a ruta á web, ou a web local ofrece un explorador de directorios propio servido polo daemon (funciona igual en todas as plataformas e sen JS; limitalo ao cartafol persoal e unidades, e protexelo coma o resto: sesión + CSRF).

- **Estado de sincronización nos iconos do escritorio:** alterar o aspecto dos iconos dos ficheiros sincronizados para mostrar o seu estado respecto ao servidor (sincronizado, pendente, en conflito…). Ideas sen verificar: fonte de datos na API local de Syncthing (`/rest/db/completion?device=<servidor>`, `/rest/db/file`, `/rest/db/need`, eventos `LocalIndexUpdated`/`ItemFinished`/`RemoteIndexUpdated`); integración por plataforma: Windows con *icon overlays* (DLL COM, límite de ~15 overlays compartidos entre apps) ou a Cloud Files API; macOS con extensión Finder Sync; Linux con extensións de Nautilus (nautilus-python) e Dolphin (`KOverlayIconPlugin`). Require código nativo por plataforma.
  - **Windows (análise, sen verificar):** os *icon overlays* son unha DLL COM cargada no Explorador (mellor C++/Rust que Go), rexistrada en `HKLM` (require admin, choca co instalador sen admin), cun límite de ~15 overlays no sistema que se disputan OneDrive, Dropbox, TortoiseGit…, e necesitan caché + IPC co daemon (*named pipe*). A Cloud Files API (a de OneDrive) usa *placeholders* e choca con Syncthing, que escribe nun temporal e renomea; ademais parte do rexistro pide identidade de paquete. Recomendación: primeiro alternativas baratas (icona na bandexa co estado xeral, sen CGO en Windows; entrada de menú contextual en `HKCU` que abre a web local; estado por recurso/ficheiro na web local) e overlays só nunha fase posterior. Linux (Nautilus/Dolphin) é moito máis doado; macOS (Finder Sync) razoable pero require app asinada.
  - **Referencia: Nextcloud desktop** (lembranza, verificar no seu código): overlays clásicos cunha DLL COM propia en C++ (`shell_integration/windows`), instalador MSI con admin; o modo de ficheiros virtuais (VFS) usa a Cloud Files API, posible porque o motor de sincronización é seu. As extensións (Explorador, Nautilus, Dolphin, Finder) falan co cliente por un socket local / *named pipe* («SocketApi», p. ex. `RETRIEVE_FILE_STATUS` → `STATUS:OK:<ruta>`); bo modelo para o noso protocolo daemon ↔ extensión. Licenza GPL-2.0+: revisar antes de reutilizar código.
  - **Proposta de SocketApi propia (sen deseñar en detalle):** o daemon `syncply` escoita nun socket local por usuario (Unix `0600` en `$XDG_RUNTIME_DIR`; en Windows *named pipe* con ACL só para o SID do usuario). Protocolo de texto, unha liña por mensaxe: o daemon envía `REGISTER_PATH:<ruta>` por cada recurso aceptado ao conectar; a extensión pregunta `RETRIEVE_FILE_STATUS:<ruta>` / `RETRIEVE_FOLDER_STATUS:<ruta>`; o daemon responde `STATUS:<estado>:<ruta>` e **empurra** os cambios de estado; tamén menú contextual (p. ex. «Ver en Syncply» → web local). As extensións son finas e gardan caché; toda a lóxica vai en Go. O mesmo socket pode alimentar a icona da bandexa. Só lectura de estado e ordes inofensivas.
  - Estados respecto ao servidor (endpoints de memoria, verificar en 2.1.7): `SYNC` baixando (`/rest/db/need?folder=…`, eventos `ItemStarted`/`ItemFinished`); `SYNC` pendente de subir (`/rest/db/remoteneed?folder=…&device=<servidor>`); `ERROR` (`/rest/folder/errors?folder=…`); conflito (nome `*.sync-conflict-*`); `OK` se non está en ningún dos anteriores e `FolderCompletion` do servidor está ao 100 %. O daemon mantén en memoria, a partir dos eventos, o conxunto de ficheiros «non OK»; o resto é `OK`.
- **Actualizacións do cliente desde o servidor:** que `syncply` poida recibir actualizacións a través do servidor ao que está vinculado. Ideas sen verificar: pairsrv publica a versión e o paquete por plataforma; o cliente compróbao periodicamente, verifica unha sinatura (clave pública compilada no cliente, independente do operador, porque o cliente é xenérico; Authenticode en Windows) e substitúese e reinicia o servizo. Inclúe o Syncthing embebido/empaquetado (o seu actualizador automático segue desactivado).

## Para futuras versións (pedidas polo usuario, sen deseñar)

- ~~Web con modo escuro automático~~ e ~~estética da web nas aplicacións~~ (feito): `docs/index.html` cambia de paleta con `prefers-color-scheme` (`docs/logo-escuro.svg` en escuro e no pé). As webs de pairsrv (usuarios e `/admin/`) e do cliente usan `internal/brand` (`syncply.css`, `logo.svg` e `logo-escuro.svg` embebidos, servidos en `/static/`; `logo.svg` leva un `<style>` interno que aclara o contorno e «lindæ» en escuro) coa **mesma estética ca a web pública**: barra superior a todo o ancho (`header.barra > .envoltorio`, fóra de `<main>`), tarxetas con sombra de 6 px, pasos numerados en tarxetas, despregables coma a FAQ (círculo «+»), avisos coma `.aviso` e pé negro (`footer.pe`, co logo escuro). Ao cambiar a paleta ou estes compoñentes, cambialos nos dous sitios. Capturas para revisar: Chromium sen interface nun contedor (`docker.io/zenika/alpine-chrome:124`, `--screenshot`). Pendente: instaladores (mensaxes de Windows) e a futura icona da bandexa.
- ~~**Panel de administración propio**~~ (feito, ver abaixo).
- ~~**A web e o lema**~~ (feito en `docs/index.html`): «a nube simple para pequenos equipos con pequenos servidores» e decrecemento (sección `#pequeno`: «Pequeno a propósito», comparación coa nube das grandes empresas, FAQ «Que servidor necesitamos?» e «Por que non ten máis funcións?»). Pendente: levar o lema ás webs de pairsrv e do cliente se se quere.

## Para o futuro: cambios en SFTPGo ao integrar (pedidos polo usuario)

- **Eliminar do código de SFTPGo as funcionalidades que deixan de ter sentido con Syncthing.** Exemplo: as **cotas dos usuarios**, que non se aplican ao que escribe Syncthing. Revisar tamén outras do mesmo tipo (límites de transferencia, filtros de ficheiros, hooks por operación…) e decidir caso a caso.
- Ocultar os ficheiros internos de Syncthing directamente no código de SFTPGo, sen depender dun grupo primario configurado a man.
- Mover a web de pairsrv (código de vinculación con contador, descargas, equipos) ao WebClient de SFTPGo, usando a súa sesión en vez dun login propio.
- **Indicadores visuais en SFTPGo** (WebAdmin e/ou WebClient) para cada recurso: se se ofrece ou non en Syncthing. Nos **non ofrecidos**, un tooltip ou similar co **motivo** (p. ex. sen permiso de escritura completo, almacenamento non local ou cifrado, permisos máis restritivos nunha subruta). Implica que `internal/sftpgo` devolva o motivo de exclusión de cada recurso, non só a lista dos ofrecidos.
