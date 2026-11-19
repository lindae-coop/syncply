#!/bin/sh
# Prepara o servidor: crea .env (segredos aleatorios e dominios) e
# pairsrv.json. Non sobrescribe ficheiros existentes.
#
#   ./init.sh nube.example.org sftp.example.org
#
# O primeiro dominio é o da web de pairsrv e o do Syncthing do servidor
# (porto 22000); o segundo, o da web de SFTPGo. Os dous teñen que apuntar a
# este equipo antes de arrancar (Caddy pide os certificados ao arrancar).
set -eu
cd "$(dirname "$0")"

if [ $# -ne 2 ]; then
	echo "uso: $0 DOMINIO_CLD DOMINIO_SFTPGO" >&2
	exit 2
fi
cld=$1 sftp=$2

rnd() { head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n'; }

if [ -e .env ]; then
	echo ".env xa existe: non se toca"
else
	umask 077
	cat > .env <<ENV
# Xerado por init.sh. Non o subas ao repositorio.
CLD_DOMAIN=$cld
SFTPGO_DOMAIN=$sftp
SFTPGO_ADMIN_USER=admin
SFTPGO_ADMIN_PASSWORD=$(rnd)
ST_APIKEY=$(rnd)
PAIRSRV_HOOK_TOKEN=$(rnd)
ENV
	umask 022
	echo "creado .env (o contrasinal de admin de SFTPGo está aí)"
fi

if [ -e pairsrv.json ]; then
	echo "pairsrv.json xa existe: non se toca"
else
	cat > pairsrv.json <<JSON
{
  "serverAddresses": ["tcp://$cld:22000", "quic://$cld:22000"],
  "maxDevicesPerUser": 5,
  "syncthingOptions": {
    "globalAnnounceEnabled": false,
    "localAnnounceEnabled": true,
    "relaysEnabled": false,
    "natEnabled": false
  },
  "serverSyncthingOptions": {
    "globalAnnounceEnabled": false,
    "localAnnounceEnabled": false,
    "relaysEnabled": false,
    "natEnabled": false,
    "urAccepted": -1,
    "crashReportingEnabled": false,
    "autoUpgradeIntervalH": 0
  },
  "sftpgo": {
    "db": "/sftpgo/sftpgo.db",
    "apiURL": "http://sftpgo:8080"
  },
  "web": {
    "publicURL": "https://$cld",
    "downloads": [
      {"label": "Linux (x86-64)", "url": "/descargas/cld-linux-amd64.tar.gz"},
      {"label": "Windows (x86-64, en probas)", "url": "/descargas/cld-windows-amd64.exe"}
    ]
  }
}
JSON
	echo "creado pairsrv.json"
fi
