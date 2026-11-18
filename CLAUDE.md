# Proxecto: nube de ficheiros lixeira con SFTPGo + Syncthing

Contexto para continuar en Claude Code. Idioma de traballo co usuario: galego.

## Obxectivo

Engadir a SFTPGo capacidades de sincronización con clientes usando o núcleo de Syncthing (sen a súa interface), para ter unha nube de ficheiros lixeira para **grupos pequenos de usuarios de confianza**.

## Requisitos (ditos polo usuario)

### Servidor
- SFTPGo incorpora o núcleo de Syncthing (idea orixinal: usar o seu código; sen GUI).
- SFTPGo comparte mediante Syncthing as carpetas segundo os **permisos dos usuarios**.
- **Decisión actual:** por agora só se ofrecen recursos nos que o usuario ten **permisos de escritura**.
- Cada usuario ten un **OTP baseado en tempo (TOTP)** na súa conta.

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
O cliente envía OTP + o seu Device ID; o servidor valida o OTP, autoriza ese dispositivo para as carpetas do usuario e devolve o seu propio Device ID. O OTP só vale para vincular; despois a identidade é o certificado do dispositivo.

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
- Versións fixadas: Syncthing **2.1.7** (`docker.io/syncthing/syncthing:2.1.7`), Go **1.27.4** (contedor `tools`).
- API de Syncthing só na rede interna dos contedores, sen portos publicados, con API key.

## Achados verificados (Syncthing 2.1.7, `make demo`)

- O fluxo **pending folders → aceptar co mesmo ID** funciona; a oferta chega en ~2 s tras emparellar.
- **Introducer: hai que forzar a reconexión tras aceptar.** O servidor non reenvía a súa ClusterConfig cando o cliente acepta a carpeta, e o introducer só presenta pares en carpetas xa compartidas con el. Sen reconexión, o último cliente en aceptar non coñece os pares existentes ata a seguinte reconexión. Solución no prototipo: `pause`/`resume` do dispositivo servidor no cliente (`stapi.Reconnect`). Os clientes xa existentes si reciben o novo par ao momento (o cambio de config no servidor reenvía a ClusterConfig).
- Cos pares introducidos (`dynamic`) e só **descubrimento local** (global e relays desactivados), os clientes conectan directamente entre eles (P2P).
- **Revogación:** `DELETE /rest/config/devices/{id}` no servidor quítao tamén das carpetas; os clientes conectados eliminan o par introducido ao momento e o revogado queda sen conexión co servidor e cos pares. A súa config local conserva as entradas (non se pode evitar).
- `/rest/system/connections` pode mostrar `connected: true` un instante en conexións que logo se rexeitan; non fiarse dunha soa mostra.
- Latencia de sincronización observada ~14 s (`fsWatcherDelayS` = 10 s por defecto).

## Próximos pasos propostos

1. Prototipo mínimo: Syncthing separado con GUI en socket Unix; script que cree dispositivo e carpeta compartida por API; cliente de proba con servidor como introducer.
2. Validar: fluxo OTP, aceptación de carpetas (pending folders), propagación de pares e baixas.
3. Deseñar o modelo de datos do servidor (usuarios, dispositivos, carpetas, permisos) e a súa tradución á configuración de Syncthing.
4. Listar os endpoints REST de Syncthing necesarios.
5. Decidir despois se se fusiona o núcleo en SFTPGo ou se mantén o proceso separado.
