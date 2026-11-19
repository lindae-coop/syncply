#!/bin/sh
# Proba o instalador de Linux (dist/cld-linux-amd64.tar.gz) nun Debian 13 con
# systemd e un usuario normal, conectado á rede do contorno:
#   instalar → servizo de usuario activo → vincular como alice desde a web →
#   aceptar /comun e recibir ficheiros → reiniciar o equipo → desinstalar.
# Require `make up` (con SFTPGo preparado: make demo ou make fresh) e `make dist`.
set -eu
cd "$(dirname "$0")/.."

img=localhost/cld-installtest:latest
name=cld-installtest
tar=dist/cld-linux-amd64.tar.gz

ok() { printf '  OK  %s\n' "$*"; }
fail() { printf 'ERRO: %s\n' "$*" >&2; exit 1; }
as_ana() { podman exec -u ana -w /home/ana -e XDG_RUNTIME_DIR=/run/user/1000 "$@"; }

[ -f "$tar" ] || fail "falta $tar (make dist)"
podman image exists "$img" || podman build -q -t "$img" -f dev/Containerfile.installtest dev >/dev/null
podman rm -f -t 2 "$name" >/dev/null 2>&1 || true
trap 'podman rm -f -t 2 "$name" >/dev/null 2>&1 || true' EXIT

podman run -d --name "$name" --hostname portatil-ana --systemd=always --network cld_stnet \
	-v "./$tar:/tmp/cld.tar.gz:ro,Z" "$img" >/dev/null
i=0
until podman exec "$name" test -S /run/user/1000/bus 2>/dev/null; do
	i=$((i + 1)); [ $i -lt 60 ] || fail "o systemd de usuario non arrancou"; sleep 1
done

echo "### Instalar como usuario"
# O contorno usa pairsrv por http (http://pairsrv:8080): só se acepta en
# desenvolvemento. Drop-in do servizo con CLD_DEV=1 (consérvase ao reiniciar).
as_ana "$name" sh -c 'mkdir -p ~/.config/systemd/user/cld.service.d &&
	printf "[Service]\nEnvironment=CLD_DEV=1\n" >~/.config/systemd/user/cld.service.d/dev.conf'
as_ana "$name" sh -c 'tar xzf /tmp/cld.tar.gz && cld-linux-amd64/install.sh --no-open' | sed 's/^/  /'
[ "$(as_ana "$name" systemctl --user is-active cld.service)" = active ] || fail "servizo inactivo"
ok "servizo de usuario cld activo"
as_ana "$name" test -S /home/ana/.config/cld/syncthing/gui.sock || fail "sen socket da API"
ok "API de Syncthing no socket Unix"

echo "### Vincular como alice desde a web (texto + código) e sincronizar /comun"
blob=$(make -s blob U=alice)
case "$blob" in cld1:*) ;; *) fail "sen texto de vinculación: $blob" ;; esac
otp=$(make -s otp U=alice | cut -d' ' -f1)
as_ana "$name" env BLOB="$blob" OTP="$otp" bash -c '
set -eu
U=$(~/.local/share/cld/bin/cld open -print); B=${U%%/login*}
H=(-H "Origin: $B" -H "Sec-Fetch-Site: same-origin")
curl -sf -c /tmp/j -o /dev/null "$U"
curl -s -b /tmp/j "${H[@]}" --data-urlencode linktext="$BLOB" "$B/pair/check" | grep -q "Vas vincular.*pairsrv:8080.*alice" \
	|| { echo "ERRO: sen confirmación do servidor e usuario"; exit 1; }
echo "  OK  confirmación: pairsrv:8080 como alice"
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" "${H[@]}" --data-urlencode linktext="$BLOB" \
	--data-urlencode otp="$OTP" --data-urlencode device=portátil-de-ana "$B/pair")
case "$r" in *ok=*) echo "  OK  vinculado" ;; *) echo "ERRO ao vincular: $r"; exit 1 ;; esac
for i in $(seq 1 30); do curl -s -b /tmp/j "$B/" | grep -q "sftpgo-folder-comun" && break; sleep 2; done
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" "${H[@]}" --data-urlencode id=sftpgo-folder-comun \
	--data-urlencode path=/home/ana/cld/comun "$B/accept")
case "$r" in *ok=*) echo "  OK  /comun aceptado" ;; *) echo "ERRO ao aceptar: $r"; exit 1 ;; esac
for i in $(seq 1 45); do [ -n "$(ls ~/cld/comun 2>/dev/null)" ] && break; sleep 2; done
n=$(ls ~/cld/comun | wc -l); [ "$n" -gt 0 ] || { echo "ERRO: non chegou ningún ficheiro"; exit 1; }
echo "  OK  recibidos $n ficheiros en ~/cld/comun"'

echo "### Reiniciar o equipo"
podman restart -t 5 "$name" >/dev/null
i=0
until as_ana "$name" /home/ana/.local/share/cld/bin/cld open -print >/dev/null 2>&1; do
	i=$((i + 1)); [ $i -lt 60 ] || fail "o servizo non volveu tras reiniciar"; sleep 1
done
ok "o servizo arranca só ao iniciar"

echo "### Revogar este equipo (para que a proba se poida repetir: alice ten un límite de equipos)"
otp=$(make -s otp U=alice | cut -d' ' -f1)
as_ana "$name" env OTP="$otp" bash -c '
set -eu
ID=$(~/.local/share/cld/bin/syncthing device-id --home="$HOME/.config/cld/syncthing")
U=$(~/.local/share/cld/bin/cld open -print); B=${U%%/login*}
curl -sf -c /tmp/j -o /dev/null "$U"
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" -H "Origin: $B" -H "Sec-Fetch-Site: same-origin" \
	--data-urlencode id="$ID" --data-urlencode otp="$OTP" --data-urlencode back=/devices "$B/revoke")
case "$r" in *ok=*) echo "  OK  equipo revogado" ;; *) echo "ERRO ao revogar: $r"; exit 1 ;; esac'

echo "### Desinstalar"
as_ana "$name" cld-linux-amd64/install.sh --uninstall >/dev/null
[ "$(as_ana "$name" systemctl --user is-active cld.service || true)" != active ] || fail "o servizo segue activo"
as_ana "$name" sh -c '! ps -o args -u ana | grep -q "[s]yncthing serve"' || fail "segue Syncthing en marcha"
as_ana "$name" test -d /home/ana/cld/comun || fail "borráronse as carpetas sincronizadas"
ok "desinstalado; consérvanse configuración e carpetas"
echo
echo "Instalador de Linux: todo correcto."
