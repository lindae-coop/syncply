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
	echo "uso: $0 DOMINIO_SYNCPLY DOMINIO_SFTPGO" >&2
	exit 2
fi
syncply=$1 sftp=$2

rnd() { head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n'; }

# O token do relay vai en .env (para strelaysrv) e en pairsrv.json (para os
# clientes): se .env xa existe, úsase o seu.
relay_token=$( (grep -s '^RELAY_TOKEN=' .env || true) | cut -d= -f2-)
if [ -e .env ] && [ -z "$relay_token" ]; then
	echo ".env xa existe e non ten RELAY_TOKEN: engádeo (p. ex. RELAY_TOKEN=$(rnd))" >&2
	exit 1
fi
[ -n "$relay_token" ] || relay_token=$(rnd)

if [ -e .env ]; then
	echo ".env xa existe: non se toca"
else
	umask 077
	cat > .env <<ENV
# Xerado por init.sh. Non o subas ao repositorio.
SYNCPLY_DOMAIN=$syncply
SFTPGO_DOMAIN=$sftp
SFTPGO_ADMIN_USER=admin
SFTPGO_ADMIN_PASSWORD=$(rnd)
ST_APIKEY=$(rnd)
PAIRSRV_HOOK_TOKEN=$(rnd)
RELAY_TOKEN=$relay_token
ENV
	umask 022
	echo "creado .env (o contrasinal de admin de SFTPGo está aí)"
fi

if [ -e pairsrv.json ]; then
	echo "pairsrv.json xa existe: non se toca"
else
	cat > pairsrv.json <<JSON
{
  "serverAddresses": ["tcp://$syncply:22000", "quic://$syncply:22000"],
  "maxDevicesPerUser": 5,
  "syncthingOptions": {
    "localAnnounceEnabled": true
  },
  "p2p": {
    "discovery": {"url": "https://$syncply:8443/", "certFile": "/discosrv/cert.pem"},
    "relay": {"url": "relay://$syncply:22067/", "certFile": "/relaysrv/cert.pem", "token": "$relay_token"},
    "publicDiscovery": true,
    "publicRelays": false
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
    "publicURL": "https://$syncply",
    "filesURL": "https://$sftp/web/client",
    "downloads": [
      {"label": "Linux (x86-64)", "url": "/descargas/syncply-linux-amd64.tar.gz"},
      {"label": "Windows (x86-64, en probas)", "url": "/descargas/syncply-windows-amd64.exe"}
    ]
  }
}
JSON
	echo "creado pairsrv.json"
fi
