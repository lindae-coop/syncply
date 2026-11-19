# Servidor cld en produción

SFTPGo + Syncthing + pairsrv en contedores, detrás de **Caddy** (HTTPS
automático con Let's Encrypt). Funciona con `podman-compose` ou con
`docker compose` (`make … COMPOSE="docker compose"`).

| Servizo   | Exposto                       | Para que                                            |
|-----------|-------------------------------|-----------------------------------------------------|
| caddy     | 80, 443/tcp                   | `https://DOMINIO_CLD` → pairsrv; `https://DOMINIO_SFTPGO` → SFTPGo |
| syncthing | 22000/tcp e 22000/udp         | conexións dos clientes (`tcp://` e `quic://DOMINIO_CLD:22000`) |
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
git clone … cld && cd cld
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

O usuario entra en `https://DOMINIO_CLD` co seu usuario e contrasinal de
SFTPGo, descarga o cliente, copia o texto de vinculación e introduce o código.

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

## Actualizar

```sh
git pull && make dist dist-windows && cd deploy && make up
```

Antes de cambiar a versión de SFTPGo, revisar `internal/sftpgo` (le a súa BD
directamente).

## Probado

Esta pila verificouse en local con dominios `*.localhost` (CA interna de
Caddy): arranque, API de Syncthing no socket, `setup-sftpgo`, aviso de
SFTPGo → pairsrv, vinculación dun cliente do paquete de Linux por HTTPS
(sen `CLD_DEV`) e sincronización nos dous sentidos. P2P: dous clientes sen
descubrimento local nin conexión directa posible atopáronse no noso
descubrimento e conectaron entre eles polo noso relay. **Falta probala nun
servidor real con Let's Encrypt.**
