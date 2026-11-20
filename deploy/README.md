# Servidor Syncply en produción

SFTPGo + Syncthing + pairsrv en contedores, detrás de **Caddy** (HTTPS
automático con Let's Encrypt). Funciona con `podman-compose` ou con
`docker compose` (`make … COMPOSE="docker compose"`).

| Servizo   | Exposto                       | Para que                                            |
|-----------|-------------------------------|-----------------------------------------------------|
| caddy     | 80, 443/tcp                   | `https://DOMINIO_SYNCPLY` → pairsrv; `https://DOMINIO_SFTPGO` → SFTPGo |
| syncthing | 22000/tcp e 22000/udp         | conexións dos clientes (`tcp://` e `quic://DOMINIO_SYNCPLY:22000`) |
| sftpgo    | 2022/tcp                      | SFTP                                                |
| discosrv  | 8443/tcp                      | descubrimento global propio (P2P)                   |
| relaysrv  | 22067/tcp                     | relay propio, só con token (P2P)                    |
| pairsrv   | —                             | só a través de Caddy; `:8081` (avisos de SFTPGo) só na rede interna |

A API de Syncthing está só nun **socket Unix** dentro do seu volume, con API
key. Os tres servizos usan o UID/GID 1000.

## Requisitos

- Un servidor Linux con `podman` + `podman-compose` (ou Docker).
- Dous nomes DNS apuntando a el (p. ex. `nube.example.org` e
  `sftp.example.org`) **antes de arrancar**: Caddy pide os certificados ao
  arrancar.
- Portos abertos no cortalumes: 80/tcp, 443/tcp, 22000/tcp, 22000/udp,
  2022/tcp, 8443/tcp, 22067/tcp.
- Con **Podman rootless**:
  - portos baixos: `sudo sysctl net.ipv4.ip_unprivileged_port_start=80`
    (e gardalo en `/etc/sysctl.d/`);
  - que os contedores sigan ao pechar a sesión e arranquen co equipo:
    `loginctl enable-linger $USER` e
    `systemctl --user enable --now podman-restart.service`.

## Instalación

```sh
git clone … syncply && cd syncply
make dist dist-windows        # paquetes do cliente en dist/ (a web ofréceos)
cd deploy
./init.sh nube.example.org sftp.example.org   # crea .env e pairsrv.json
make up                       # compila pairsrv e arranca todo
make setup-sftpgo             # grupo "syncthing" e aviso de SFTPGo a pairsrv
```

`init.sh` xera segredos aleatorios en `.env` (permisos 600), entre eles o
contrasinal do administrador de SFTPGo. Non sobrescribe ficheiros que xa
existan. `make setup-sftpgo` pódese repetir: non toca o que xa existe.

## Dar de alta usuarios

En `https://DOMINIO_SFTPGO/web/admin` (usuario `admin`, contrasinal en
`.env`):

1. Usuario con almacenamento **disco local**.
2. **Grupo primario `syncthing`** (oculta en SFTPGo os ficheiros internos de
   Syncthing). Os grupos de recursos compartidos, como secundarios.
3. Ofrécense en Syncthing o directorio persoal e os cartafoles virtuais nos
   que o usuario ten **permiso de escritura completo** (ver o README
   principal).

O usuario entra en `https://DOMINIO_SYNCPLY` co seu usuario e contrasinal de
SFTPGo, descarga o cliente, copia o texto de vinculación e introduce o código.

## Contrasinais e segredos (`.env`)

| Variable                | Uso                                                       |
|-------------------------|-----------------------------------------------------------|
| `SFTPGO_ADMIN_USER`, `SFTPGO_ADMIN_PASSWORD` | administrador de SFTPGo. Só se usan para **crealo no primeiro arranque** e para `make setup-sftpgo` |
| `ST_APIKEY`             | API key do Syncthing do servidor (só a usa pairsrv)       |
| `PAIRSRV_HOOK_TOKEN`    | aviso de SFTPGo a pairsrv                                 |
| `RELAY_TOKEN`           | acceso ao relay (tamén está en `pairsrv.json`)            |

- **Garda `.env`** nun xestor de contrasinais ou xunto ás copias: sen el non
  tes o contrasinal de admin de SFTPGo.
- **Cambiar o contrasinal de admin:** na WebAdmin de SFTPGo. Despois
  actualiza `SFTPGO_ADMIN_PASSWORD` en `.env` (cambialo só en `.env` non
  cambia nada en SFTPGo). Se activas o 2FA do admin, `make setup-sftpgo`
  deixa de poder entrar con el: usa outro administrador sen 2FA para iso.
- **`ST_APIKEY`:** pódese cambiar en `.env` e `make up` (Syncthing e pairsrv
  len o novo valor ao arrancar).
- **`PAIRSRV_HOOK_TOKEN`:** ao cambialo, hai que cambiar tamén a cabeceira
  `Authorization` da acción de eventos `pairsrv-sync` na WebAdmin
  (`make setup-sftpgo` non modifica o que xa existe). Mentres non coincidan,
  os cambios de usuarios chegan só coa reconciliación periódica (5 min).
- **`RELAY_TOKEN`:** cambialo obriga a todos os clientes a «Actualizar a
  conexión»: cámbiao en `.env` e en `pairsrv.json` e `make up`.

## Descubrimento e P2P

O propio servidor fai de **servidor de descubrimento** (`stdiscosrv`) e de
**relay** (`strelaysrv`); non se usa a infraestrutura pública de Syncthing.

- Os clientes anuncian os seus enderezos no noso descubrimento e buscan alí
  os dos pares (os pares introducidos teñen enderezo `dynamic`). Conectan
  directamente se poden (tamén coa axuda de UPnP/NAT-PMP); se non, a través
  do noso relay.
- Cada servizo xera o seu certificado no primeiro arranque. pairsrv le os
  certificados (volumes `discosrv` e `relaysrv`), calcula os Device ID e
  envía aos clientes as URL fixadas (`?id=…`), xunto co **token do relay**
  (`RELAY_TOKEN` en `.env`; sen el o relay rexeita a conexión e non se une
  ao pool público).
- O servidor Syncthing non os usa: os clientes conéctanse a el polo seu
  enderezo fixo.
- Configuración: `p2p` en `pairsrv.json`. Sen esa sección, sistema pechado:
  P2P só na mesma LAN (descubrimento local) e, entre redes, a través do
  servidor.

O descubrimento é aberto (calquera pode anunciarse nel, como o público), pero
só dá os enderezos de quen coñeza o Device ID.

Os clientes reciben estas opcións **ao vincular**: un cambio posterior chega
aos equipos xa vinculados cando o usuario fai «Actualizar a conexión» (ou
volve vincular). Se se perde o volume `discosrv` ou `relaysrv`, cambia o seu
Device ID e pasa o mesmo.

## Copias de seguridade

| Volume          | Contido                                                  |
|-----------------|----------------------------------------------------------|
| `data`          | ficheiros dos usuarios                                   |
| `sftpgo-lib`    | BD de SFTPGo (usuarios, grupos, permisos)                |
| `syncthing`     | **identidade do servidor** (certificado = Device ID) e índices |
| `pairsrv-state` | equipos vinculados, tokens e segredos dos códigos        |
| `discosrv`, `relaysrv` | certificados (Device ID fixados nos clientes)     |

Se se perde o volume `syncthing`, o servidor ten outro Device ID e os
clientes rexéitano: todos terían que volver vincular. Garda tamén `.env` e
`pairsrv.json`.

## Datos dos usuarios nun directorio do equipo

Por defecto os ficheiros van no volume `data`. Para telos nun directorio do
equipo (máis doado de copiar ou de poñer noutro disco), cambia en
`compose.yaml`, nos servizos `sftpgo` e `syncthing`, a liña
`- data:/srv/sftpgo/data` por:

```yaml
      - /srv/syncply-datos:/srv/sftpgo/data:z
```

O directorio ten que pertencer ao UID/GID 1000 **dentro dos contedores**:

```sh
sudo mkdir -p /srv/syncply-datos
sudo chown 1000:1000 /srv/syncply-datos                # Docker ou Podman con root
podman unshare chown 1000:1000 /srv/syncply-datos      # Podman rootless (directorio do teu usuario)
```

A ruta de dentro (`/srv/sftpgo/data`) non se cambia: é a que gardan SFTPGo e
Syncthing. Para mover datos existentes, para os servizos (`make down`),
copia o contido do volume (`podman volume mount` ou un contedor temporal) e
arranca de novo.

## Actualizar

```sh
git pull && make dist dist-windows && cd deploy && make up
```

Antes de cambiar a versión de SFTPGo, revisar `internal/sftpgo` (le a súa BD
directamente).

## Estado e logs

```sh
make ps                                  # contedores e portos
make logs                                # todos os servizos
podman-compose logs -f pairsrv           # un servizo: caddy, sftpgo, syncthing, discosrv, relaysrv, pairsrv
```

Ao arrancar, pairsrv escribe as URL de descubrimento e relay que envía aos
clientes (`P2P: …`).

## Problemas habituais

- **Caddy non obtén o certificado** (`logs caddy`): os dous dominios teñen
  que apuntar a este equipo e os portos 80 e 443 teñen que ser accesibles
  desde internet. En Podman rootless, comprobar
  `net.ipv4.ip_unprivileged_port_start`. Moitos intentos fallidos esgotan
  o límite de Let's Encrypt durante unha hora.
- **pairsrv non arranca** (`logs pairsrv`): `web.publicURL` ten que ser
  `https://`; agarda a Syncthing, a SFTPGo e aos certificados de discosrv e
  relaysrv ata 2 minutos e despois sae (reinicia só).
- **A web non acepta o usuario:** o contrasinal compróbase con SFTPGo; o
  usuario ten que estar activo e, se ten 2FA en SFTPGo, a web pídeo.
- **O usuario non ve recursos:** grupo primario `syncthing`, almacenamento en
  disco local e permiso de escritura completo no recurso (só lectura non se
  ofrece). Os cambios en usuarios e grupos aplícanse en ~1 s; os de
  cartafoles virtuais (ruta), coa reconciliación (5 min).
- **O cliente vincula pero non sincroniza:** o porto 22000 (tcp e udp) ten
  que ser accesible desde fóra (`nc -vz DOMINIO_SYNCPLY 22000` desde outra
  rede); `serverAddresses` de `pairsrv.json` ten que levar o dominio público.
- **Os clientes non conectan entre eles fóra da LAN:** portos 8443 e 22067
  accesibles; os equipos vinculados antes de activar ou cambiar `p2p`
  necesitan «Actualizar a conexión».
- **Os ficheiros tardan:** Syncthing agarda uns 10 s a agrupar cambios; o
  normal son 10–20 s.

## Desinstalar

```sh
make down                     # para e borra os contedores; os volumes (datos) quedan
podman-compose down -v        # ⚠ borra tamén TODOS os volumes: ficheiros, BD, identidades
```

Nos equipos dos usuarios: en Linux, `./install.sh --uninstall` desde o
paquete descargado (non se copia ao instalar); en Windows, desde
«Aplicacións instaladas». Os dous conservan as carpetas sincronizadas e a
configuración. Convén revogar antes o equipo desde «Os meus equipos».

## Probado

Esta pila verificouse en local con dominios `*.localhost` (CA interna de
Caddy): arranque, API de Syncthing no socket, `setup-sftpgo`, aviso de
SFTPGo → pairsrv, vinculación dun cliente do paquete de Linux por HTTPS
(sen `SYNCPLY_DEV`) e sincronización nos dous sentidos. P2P: dous clientes sen
descubrimento local nin conexión directa posible atopáronse no noso
descubrimento e conectaron entre eles polo noso relay. **Falta probala nun
servidor real con Let's Encrypt.**
