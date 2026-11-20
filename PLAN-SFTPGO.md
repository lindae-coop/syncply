# Plan: fase 1 da integración en SFTPGo

Obxectivo: substituír pairsrv por código dentro dun **fork de SFTPGo 2.7.6**. Syncthing segue sendo un **proceso aparte**, pero agora SFTPGo arráncao e supervísao. O estado gárdase en **táboas propias** na BD de SFTPGo. O cliente `syncply` non cambia, nin tampouco os IDs de carpeta, polo que os equipos xa vinculados seguen funcionando sen vincular de novo.

Fóra desta fase: embeber Syncthing como biblioteca, borrar funcionalidades do código de SFTPGo e o modo con **varios nodos** (ver o final; o esquema xa se deseña para el).

Estimación total: **18–27 días** (+2–4 d se se fai o paso opcional 11).

## Puntos de enganche comprobados en SFTPGo v2.7.6

| Que | Onde | Nota |
|---|---|---|
| Avisos de cambios | `internal/dataprovider/actions.go` → `executeAction` | Chámase para `user`, `group` **e `folder`** (`AddFolder`/`UpdateFolder`/`DeleteFolder`). Só o xestor de eventos ignora os cartafoles; en memoria chegan todos. Para engadir un callback, seguir o patrón de `SetEventRulesCallbacks` (`dataprovider.go:286`) |
| Ocultar ficheiros | `internal/dataprovider/user.go:1136` → `User.IsFileAllowed` | É o único punto que usan SFTP, SCP, FTP, WebDAV, HTTP/WebClient e os enlaces compartidos (`common/connection.go`, `sftpd/*`, `webdavd/file.go`). Devolve `(false, sdk.DenyPolicyHide)` |
| Usuarios e grupos | `dataprovider.GetUsers` (paxinado), `GetUserWithGroupSettings`, `User.GetPermissionsForPath`, `HasPerm`, `HasPermissionsInside`, `VirtualFolders` | Substitúe a lóxica reproducida en `internal/sftpgo/model.go` |
| BD | `dataprovider.Config` (`driver`, `name`, `host`, `port`, `username`, `password`, `sslmode`…; `is_shared`) | `dataprovider` non expón o seu `*sql.DB`: abrimos a nosa propia conexión coa mesma configuración |
| Segredos | `internal/kms` (`kms.NewPlainSecret` → `Encrypt`/`Decrypt`) | O mesmo mecanismo co que SFTPGo garda os segredos TOTP do 2FA |
| Configuración | `internal/config/config.go` (`setViperDefaults`, prefixo `SFTPGO_`) | Nova sección `syncthing` |
| Arranque e parada | `internal/service/service.go` → `startServices` / canle `Shutdown` | |
| Rutas | `internal/httpd/server.go` → `initializeRouter` (chi); constantes en `httpd.go` (`webClient*Path`) | |
| Páxinas do WebClient | `internal/httpd/webclient.go` (`loadClientTemplates`, `renderClientTemplate`), `templates/webclient/*.html`, menú en `templates/webclient/base.html` | Os scripts levan `nonce="{{.CSPNonce}}"` |
| Traducións | `static/locales/{de,en,fr,it}/translation.json` | Non hai galego |
| Build | `go 1.26.0`, `mattn/go-sqlite3` (CGO) | O contedor `tools` (Go 1.27.4) ten que poder compilar con CGO |

## Decisión previa: onde vive o código común

**Recomendado:** sacar de `internal/` os paquetes que usan o servidor e o cliente (`pairing` sen a web, `totp`, `stapi`, `linktext` e o supervisor de `internal/client`) e levalos a `pkg/` neste repo. O fork importaríaos como módulo Go. Así o fork só leva a cola de unión (adaptador, almacenamento, rutas, páxinas), e o diff contra upstream queda pequeno.

Alternativa: copialos no fork. É máis simple ao principio, pero queda código duplicado entre `syncply` e o fork.

## Pasos

Cada paso remata con algo verificable. A orde permite manter `make demo` funcionando todo o tempo.

### 0. Base do fork (0,5–1 d)
- Facer un fork de `drakkan/sftpgo` na etiqueta `v2.7.6`, nunha rama `syncply`.
- Compilar sen cambios no contedor `tools` e facer unha imaxe a partir do `Dockerfile` de upstream, coa etapa extra de copiar `syncthing` 2.1.7 desde `docker.io/syncthing/syncthing:2.1.7`.
- Cambiar o servizo `sftpgo` de `compose.yaml` pola imaxe do fork.
- **Verificar:** `make demo` pasa igual que hoxe (pairsrv segue fóra).

### 1. Extraer os paquetes comúns (1 d)
- Mover `pairing` (servizo, configuración, linktext), `totp`, `stapi` e o supervisor a `pkg/`. A web propia de pairsrv queda en `cmd/pairsrv` ata o paso 12.
- Separar o almacenamento do servizo de vinculación cunha interface `Store`. A implementación actual, sobre `state.json`, segue sendo a de pairsrv.
- **Verificar:** `make test`, `make demo`.

### 2. Adaptador do dataprovider (2–3 d)
- Novo paquete no fork (p. ex. `internal/syncply`) que implemente `Directory.Snapshot` con `dataprovider`. Percorre os usuarios paxinados, aplica os grupos e calcula os recursos ofrecidos:
  - directorio persoal e cartafoles virtuais con `FsConfig.Provider` local (sen cifrado nin SFTP/S3);
  - permiso de escritura completo en toda a árbore, tendo en conta os permisos de subrutas e os cartafoles montados dentro;
  - o usuario está activo e non caducou;
  - **o usuario pode entrar no WebClient** (decisión: se non pode, non ve o código de vinculación e queda excluído). Mesmas condicións que `checkHTTPClientUser` (`httpd/api_utils.go`): `HTTP` en `Filters.DeniedProtocols` (os grupos primario e secundarios engaden os seus) ou `password` non permitido por `IsLoginMethodAllowed` (salvo login OIDC), ademais de `CheckLoginConditions`. Os anónimos teñen HTTP denegado. Xa implementado no prototipo (`internal/sftpgo`, `webClientAllowed`); no fork, usar directamente as funcións de SFTPGo.
- **Devolver tamén os recursos excluídos co seu motivo**: enum `nonLocal`, `encrypted`, `noWrite`, `restrictedSubpath`, `nested`, `userDisabled`, `noWebClient`. Úsase no paso 9.
- Manter os IDs `sftpgo-home-<usuario>` e `sftpgo-folder-<nome>`.
- Portar os casos de `internal/sftpgo/*_test.go` a probas co provedor `memory`.
- **Verificar:** as probas unitarias dan os mesmos recursos que o adaptador actual sobre `dev/sftpgo-demo.json`.

### 3. Estado en táboas propias (3–4 d)
- **Sen interferir con SFTPGo:**
  - táboas co prefixo `syncply_` e versión de esquema propia (`syncply_schema_version`), independente da de SFTPGo (v33), con migracións propias;
  - **sen claves foráneas** cara ás táboas de SFTPGo; o callback do paso 5 borra as filas dun usuario borrado;
  - conexión propia (`*sql.DB`) aberta coa configuración do `data_provider`.
- **Provedores:** SQLite, MySQL, PostgreSQL e CockroachDB. Con `bolt`/`memory`, Syncthing non se ofrece (erro claro ao arrancar se `syncthing.enabled`).
- **Esquema** (pensado xa para varios nodos):
  - `syncply_users`: `username` (PK), `otp_secret` (cifrado co KMS de SFTPGo), `otp_last_step`, `updated_at`.
  - `syncply_devices`: `device_id` (PK), `username`, `name`, `token_hash`, `created_at`, `last_seen`.
  - `syncply_server`: identidade de Syncthing do servidor (certificado e clave cifrados co KMS) e Device ID. Garda o mesmo que o home de Syncthing, para que se poida recrear.
  - `syncply_lease`: `name`, `node`, `expires_at`. **Creada pero sen usar** nesta fase (líder para varios nodos).
  - `syncply_changes`: contador de cambios. Tamén **sen usar** de momento (avisos entre nodos).
- **OTP atómico:** marcar o código como usado con `UPDATE syncply_users SET otp_last_step = ? WHERE username = ? AND otp_last_step < ?`. Se non actualiza ningunha fila, o código xa se usou, aínda que chegue por dous nodos distintos.
- Datas en `int64` (ms), como fai SFTPGo, para evitar diferenzas de tipos entre dialectos.
- **Migración desde pairsrv:** importar `state.json` (segredos, dispositivos, tokens) e o certificado do home de Syncthing a `syncply_*`. Así o servidor conserva o seu Device ID e ninguén ten que vincular de novo.
- **Verificar:** probas do `Store` contra SQLite, PostgreSQL e MySQL en contedores; a importación de `state.json` produce o mesmo estado.

### 4. Configuración e ciclo de vida (1–2 d)
- Sección `syncthing` en `config.go`:
  - `enabled`, `binary`, `home`;
  - `public_url`, `max_devices`, `syncthing_options`, `reconcile_interval`, `downloads_dir`.
- En `startServices`: abrir o `Store`, crear o servizo de vinculación, arrancar Syncthing co supervisor (API en socket Unix e API key xerada) e o bucle de reconciliación. Parar todo cando se pecha `Shutdown`.
- Se o home de Syncthing non ten certificado, escribir o de `syncply_server` antes de arrancar. Se non hai ningún, xeralo e gardalo.
- Manter a regra `public_url` `https://` agás con `SYNCPLY_DEV=1`.
- **Verificar:** SFTPGo arranca Syncthing co Device ID gardado, e `/rest/system/status` responde polo socket.

### 5. Avisos en memoria (0,5–1 d)
- Callback rexistrado desde `internal/syncply`, chamado en `executeAction` para `user`, `group` e `folder` → `RequestReconcile()`. É unha liña en `actions.go` máis un setter. Cando se borra un usuario, borra tamén as súas filas en `syncply_*`.
- A reconciliación periódica queda como rede de seguridade (as caducidades de usuario non xeran eventos).
- Retirar da demo a regra HTTP do xestor de eventos.
- **Verificar:** un cambio de `mapped_path` dun cartafol aplícase en ~1 s (hoxe non chega ata a reconciliación periódica).

### 6. API para o cliente (1–2 d)
- Montar os manexadores de `pairing/http.go` no router de SFTPGo, sen os middlewares de autenticación de SFTPGo:
  - `/healthz` → `/syncply/healthz`;
  - `/api/v1/pair`, `/api/v1/devices`… → baixo un prefixo (p. ex. `/syncply/api/v1/…`).
- `public_url` = URL base de SFTPGo + prefixo. O cliente segue construíndo as rutas desde a URL do texto de vinculación, así que non cambia.
- Desaparece `/internal/v1/sync`.
- Considerar o **defender** de SFTPGo para os OTP fallidos (bloqueo por IP).
- **Verificar:** `syncply` vincula e lista equipos contra o fork.

### 7. Páxina no WebClient (3–5 d)
- Nova ruta `/web/client/sync`, entrada no menú de `base.html`, modelo `sync.html` e manexador en `webclient.go` que colle o usuario da sesión (JWT). **O login propio desaparece**, e de paso funcionan o 2FA e OIDC de SFTPGo.
- Contido: código de vinculación con contador (JS con `nonce`), texto de vinculación con Copiar, descargas desde `downloads_dir`, recursos ofrecidos e os meus equipos (N de máximo).
- Novo: **revogar desde a web** (POST con CSRF de SFTPGo; a sesión xa autentica, sen OTP).
- Chaves i18n en `en` (e `it`/`de`/`fr` en inglés ou traducidas). O galego é opcional, porque supón engadir un idioma a SFTPGo.
- Endpoint de API de usuario `GET /api/v2/user/syncply` (token JWT de `/api/v2/user/token`) que devolva código e texto: serve para `make otp`/`make blob` sen raspar o HTML.
- **Verificar:** vincular nun navegador real desde o WebClient.

### 8. Ocultar os ficheiros de Syncthing no código (1 d)
- En `User.IsFileAllowed`, se `syncthing.enabled`, denegar con `DenyPolicyHide` `HiddenPatterns` (`.stfolder`, `.stversions`, `.stignore`, `~syncthing~*`, `.syncthing.*.tmp`).
- Comprobar **cada compoñente da ruta**, non só o nome final: hoxe o filtro mira `path.Base`, e `.stversions/x` ten que quedar oculto.
- Os `*.sync-conflict-*` seguen visibles.
- Retirar o grupo primario `syncthing` de `internal/sftpgo/setup.go` e da demo.
- **Verificar:** probas por SFTP, WebDAV e WebClient; a comprobación de `cmd/stproto` («bob ve /comun sen .stfolder») segue pasando sen o grupo.

### 9. Indicadores de recurso ofrecido / non ofrecido (2–3 d)
- WebClient (páxina do paso 7): todos os recursos do usuario, ofrecidos ou non, co motivo nun tooltip.
- WebAdmin: tarxeta de só lectura «Syncthing» en `user.html` cos recursos e os motivos (usa o adaptador do paso 2 sobre ese usuario) e os equipos vinculados.
- Os usuarios con `noWebClient` non ven a páxina; o motivo só aparece no WebAdmin.
- **Verificar:** un usuario de proba con cada tipo de exclusión.

### 10. Probas e contorno (2–3 d)
- `cmd/stproto` e `make demo` sen o contedor pairsrv. `make otp`/`make blob` co endpoint do paso 7.
- `make installtest` contra o fork.
- Probas unitarias no fork, no estilo de SFTPGo, para o adaptador, o `Store`, os avisos e o filtro.
- Unha variante da demo con PostgreSQL, ademais de SQLite.

### 11. (Opcional) Ocultar na UI as funcionalidades que non se aplican a Syncthing (2–4 d)
- Cotas, límites de transferencia, patróns de ficheiros e hooks por operación: ocultalos ou avisar nas páxinas cando `syncthing.enabled`. **Non borrar código** nesta fase, para que o rebase siga sendo barato.

### 12. Retirar pairsrv (0,5 d)
- Borrar `cmd/pairsrv`, a web de `pairing`, `internal/sftpgo`, o `Store` sobre `state.json` (manter só a importación) e o servizo de compose.
- Actualizar README e CLAUDE.md.

## Ficheiros de upstream tocados (para manter os rebases baratos)

`dataprovider/actions.go`, `dataprovider/user.go` (`IsFileAllowed`), `config/config.go`, `service/service.go`, `httpd/httpd.go`, `httpd/server.go`, `httpd/webclient.go`, `httpd/webadmin.go`, `templates/webclient/base.html`, `templates/webadmin/user.html`, `static/locales/*/translation.json`, `Dockerfile`, `go.mod`. Todo o demais (incluídas as táboas `syncply_*` e as súas migracións) vai en ficheiros novos.

## Riscos e preguntas abertas

- **Tres dialectos SQL** (marcadores `?`/`$1`, tipos, `UPSERT`): manter as consultas simples e probalas contra os tres motores.
- **Restauracións de SFTPGo** (backup/restore de `dumpdata`): non inclúen as táboas `syncply_*`. Documentar que se fai copia da BD enteira, ou engadir exportación propia máis adiante.
- **CGO:** comprobar que o contedor `tools` compila SFTPGo con `CGO_ENABLED=1` (SQLite de `mattn`).
- **Rebase a novas versións de SFTPGo:** revisar o adaptador e os puntos de enganche (substitúe a nota actual sobre `internal/sftpgo`).

## Fase posterior: varios nodos (activo/pasivo, +6–10 d)

Só se aparece unha necesidade real. Require `is_shared: 1` (MySQL, PostgreSQL ou CockroachDB) e disco compartido (NFS ou similar) entre os nodos.

1. **Un único Syncthing activo:** o nodo que ten a concesión en `syncply_lease` (`UPDATE … WHERE expires_at < now`, renovada periodicamente) executa Syncthing e a reconciliación. O resto de nodos serven a web e a API e só escriben na BD.
2. **Mesma identidade tras un fallo:** o novo líder arranca Syncthing co certificado de `syncply_server`. Despois dunha caída hai que reescanear todo.
3. **Avisos entre nodos:** `executeAction` só se dispara no nodo onde se fixo o cambio, e SFTPGo só sincroniza as súas cachés cada 10 min. Cada nodo incrementa `syncply_changes` e o líder consúltao cada poucos segundos (ou chámase o líder usando a táboa `nodes` de SFTPGo).
4. **Cambios no disco desde outros nodos:** inotify non os ve en NFS. Cada evento de ficheiro de SFTPGo, en calquera nodo, ten que forzar `POST /rest/db/scan` no líder.
5. **Enderezo do servidor para os clientes:** IP virtual, DNS ou descubrimento propio, para que atopen o líder novo.
6. **Probas:** dous nodos + PostgreSQL + volume compartido en compose, con caída do líder.
