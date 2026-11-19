# Proxecto: nube de ficheiros lixeira con SFTPGo + Syncthing

Contexto para continuar en Claude Code. Idioma de traballo co usuario: galego.

## Obxectivo

Engadir a SFTPGo capacidades de sincronización con clientes usando o núcleo de Syncthing (sen a súa interface), para ter unha nube de ficheiros lixeira para **grupos pequenos de usuarios de confianza**.

## Requisitos (ditos polo usuario)

### Servidor
- SFTPGo incorpora o núcleo de Syncthing (idea orixinal: usar o seu código; sen GUI).
- SFTPGo comparte mediante Syncthing as carpetas segundo os **permisos dos usuarios**.
- **Decisión actual:** por agora só se ofrecen recursos nos que o usuario ten **permisos de escritura**.
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
- Limitacións coñecidas: os filtros de ficheiros (patróns permitidos/denegados), as cotas e os hooks de SFTPGo non se aplican ao que escribe Syncthing; `.stfolder` e temporais de Syncthing vense desde SFTPGo.

## Próximos pasos propostos

1. ~~Prototipo mínimo~~ e ~~validar fluxo OTP, pending folders, propagación de pares e baixas~~ (feito). Cliente: API de Syncthing en socket Unix (feito; en desenvolvemento segue en TCP interno). Servidor: pendente.
2. ~~Integrar con SFTPGo~~ (feito lendo a BD), ~~avisos do xestor de eventos~~ e ~~ocultar os ficheiros de Syncthing~~ (grupo primario `syncthing`).
3. Deseñar o modelo de datos do servidor (usuarios, dispositivos, carpetas, permisos) e a súa tradución á configuración de Syncthing.
4. Listar os endpoints REST de Syncthing necesarios.
5. Decidir despois se se fusiona o núcleo en SFTPGo ou se mantén o proceso separado.

## Web de pairsrv: código de vinculación (verificado con `make demo`)

- Na raíz de pairsrv o usuario entra co usuario e contrasinal de SFTPGo (comprobados coa API de SFTPGo) e ve o **código de vinculación** cun contador (barra CSS + recarga automática), o texto de vinculación, a descarga do cliente, os recursos ofrecidos e os seus equipos (N de máximo).
- O código é un TOTP (SHA1, 6 díxitos, 30 s, ±1 paso) cun **segredo por usuario xerado por pairsrv** e gardado no seu estado; créase a primeira vez que se mostra. Se o código actual xa se usou, a web mostra o do seguinte paso (xa aceptado), así que sempre mostra un código válido.
- O mesmo código serve para vincular e para revogar. Os usuarios xa non necesitan activar o 2FA de SFTPGo; se o teñen, a web pídeo para entrar.
- En desenvolvemento: `make web` (`127.0.0.1:8090`); `make otp U=alice` e `make blob U=alice` entran na web co contrasinal de proba e devolven o mesmo código e texto.

## Cliente `cld` (prototipo)

- Decidido: app de configuración como **web local** servida por `cld daemon` en `127.0.0.1`; acceso sen coñecer detalles mediante un **acceso directo** que executa `cld open` (URL dun só uso → cookie de sesión). Ver README.
- Verificado a man contra o contorno: vincular co OTP desde a web, ver recursos, aceptar nunha carpeta escollida, deixar de sincronizar, pausar/retomar, listar equipos, rexeitamento CSRF.
- Tras "deixar de sincronizar", a oferta tarda ~20 s en volver (reconexión co servidor).
- Vinculación probada polo usuario nun navegador real.
- `cld daemon` arranca e supervisa o seu Syncthing (verificado: Deter para o proceso e o estado mantense tras reiniciar; Arrancar volve conectar; ao pechar o daemon, Syncthing remata limpo; modo por defecto con API en socket Unix e API key xerada). Syncthing 2.1.7 crea dous procesos (pai e fillo) mesmo con `--no-restart`; paran os dous. `make demo` pasa completo cos clientes executando `cld` co seu Syncthing (verificado polo usuario).
- **Instalador de Linux** (`make dist`, `packaging/linux/install.sh`): por usuario sen root, servizo de systemd de usuario (ou autostart XDG), acceso directo. Verificado con `make installtest` en Debian 13 con systemd: instalar, vincular desde a web, recibir ficheiros, arranque tras reiniciar, desinstalar; e a man o camiño sen systemd. Syncthing 2.1.7 ten `syncthing device-id --home=…` para ver o Device ID sen arrancalo.
- **Non usar `Referrer-Policy: no-referrer`** coa comprobación de `Origin`: o navegador envía `Origin: null` nos POST á mesma orixe e rexéitanse os formularios propios. Usar `same-origin`.
- Pendente:
  - Instaladores de Windows (tarefa de inicio, menú Inicio) e macOS (LaunchAgent, app). Paquete arm64 (descargar e verificar o binario de Syncthing das súas publicacións). Icona na bandexa (require CGO en Linux/macOS).
  - Cambiar a carpeta local dun recurso xa aceptado (hoxe: deixar de sincronizar e volver aceptar).

## Cliente xenérico + texto de vinculación + OTP (implementado e verificado)

Motivo: un instalador con configuración por servidor dentro rompe a sinatura Authenticode en Windows (cada operador tería que reconstruír e asinar). Descartados: ficheiro `cld.json` ao lado do `.exe` (pérdese se se executa desde o zip) e etiquetar o binario asinado (`certificate_tag` de Omaha).

- Formato (`internal/pairing/linktext.go`): `cld1:` + base64url de `{"v":1,"url","user","server"}`. Sen segredos; o OTP é o que autoriza.
- pairsrv: a web mostra o texto (pasos: descarga, texto + Copiar, código); `web.publicURL` obrigatoria; `syncthingOptions` na configuración de pairsrv e na `PairResponse`. Botón Copiar: `/static/copy.js`, CSP `script-src 'self'`; sen JS, o `textarea` de só lectura.
- cld: sen `Config`/`cld.json` nin `-config`. Valores seguros compilados (`client.DefaultSyncthingOptions`) aplicados sempre e, por riba, os de pairsrv gardados no estado. Web en dous pasos: pegar texto (`POST /pair/check`: validación + comprobación de `/healthz`) → confirmación «Vas vincular con <host> como <usuario>» + OTP + nome (`POST /pair`; se falla, volve á confirmación). Rexeita se pairsrv responde con outro Device ID. "Actualizar a conexión" en "Os meus equipos" (texto novo + OTP, só mesmo servidor e usuario) para cambios de enderezo.
- **HTTPS (decidido): pairsrv vai detrás dun proxy; os clientes validan coas CA do sistema, sen fixar certificado** (o pin era sobreenxeñería e rompía coas renovacións do proxy). `web.publicURL` ten que ser `https://`; pairsrv non arranca e o cliente rexeita o texto se é `http://`, agás con **`CLD_DEV=1`** (modo de desenvolvemento: tamén cookies sen `Secure`). En compose está en todos os contedores; en `make installtest`, como drop-in do servizo de usuario.
- Verificado: `make test`, `make demo` e `make installtest` (vincula co texto de `make blob U=alice` + `make otp`).
- Instalacións anteriores: aparecen sen vincular (o estado non ten `pairsrvURL`); volver vincular renova o token. `install.sh` borra o `cld.json` vello.

Para máis adiante (non agora): identificador aleatorio que caduque dentro do texto (evitaría que quen coñeza o usuario poida bloquear o seu emparellamento con OTP falsos, risco que xa existe hoxe); ligazón `cld://…` desde a web que encha a primeira casiña.

Windows (avaliado, sen probar): `cld.exe` autoinstalable (`cld install`, Syncthing embebido con `go:embed`, `GOOS=windows` sen CGO) asinado con `osslsigncode` desde Linux; alternativas NSIS (`makensis`) ou MSI con `wixl`. Arranque sen admin con `HKCU\…\Run` ou acceso directo no cartafol de Inicio (unha tarefa programada ao iniciar sesión adoita pedir elevación). MSIX descartado. Certificado de asinamento: decisión do usuario.

## Para o futuro: cambios en SFTPGo ao integrar (pedidos polo usuario)

- **Eliminar do código de SFTPGo as funcionalidades que deixan de ter sentido con Syncthing.** Exemplo: as **cotas dos usuarios**, que non se aplican ao que escribe Syncthing. Revisar tamén outras do mesmo tipo (límites de transferencia, filtros de ficheiros, hooks por operación…) e decidir caso a caso.
- Ocultar os ficheiros internos de Syncthing directamente no código de SFTPGo, sen depender dun grupo primario configurado a man.
- Mover a web de pairsrv (código de vinculación con contador, descargas, equipos) ao WebClient de SFTPGo, usando a súa sesión en vez dun login propio.
- **Indicadores visuais en SFTPGo** (WebAdmin e/ou WebClient) para cada recurso: se se ofrece ou non en Syncthing. Nos **non ofrecidos**, un tooltip ou similar co **motivo** (p. ex. sen permiso de escritura completo, almacenamento non local ou cifrado, permisos máis restritivos nunha subruta). Implica que `internal/sftpgo` devolva o motivo de exclusión de cada recurso, non só a lista dos ofrecidos.
