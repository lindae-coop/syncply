#!/bin/sh
# Proba o instalador de Linux (dist/syncply-linux-amd64.tar.gz: só syncply, que
# se instala a si mesmo) nun Debian 13 con systemd e un usuario normal,
# conectado á rede do contorno:
#   ./syncply (como co dobre clic, con DISPLAY e un xdg-open falso que fai de
#   navegador: segue a páxina de progreso ata a web local) → servizo de usuario
#   activo → vincular como alice desde a web → aceptar /comun e recibir
#   ficheiros → reiniciar o equipo → desinstalar.
# Require `make up` (con SFTPGo preparado: make demo ou make fresh) e `make dist`.
set -eu
cd "$(dirname "$0")/.."

img=localhost/syncply-installtest:latest
name=syncply-installtest
tar=dist/syncply-linux-amd64.tar.gz

ok() { printf '  OK  %s\n' "$*"; }
fail() { printf 'ERRO: %s\n' "$*" >&2; exit 1; }
as_ana() { podman exec -u ana -w /home/ana -e XDG_RUNTIME_DIR=/run/user/1000 "$@"; }

[ -f "$tar" ] || fail "falta $tar (make dist)"
podman image exists "$img" || podman build -q -t "$img" -f dev/Containerfile.installtest dev >/dev/null
podman rm -f -t 2 "$name" >/dev/null 2>&1 || true
trap 'podman rm -f -t 2 "$name" >/dev/null 2>&1 || true' EXIT

podman run -d --name "$name" --hostname portatil-ana --systemd=always --network syncply_stnet \
	-v "./$tar:/tmp/syncply.tar.gz:ro,Z" "$img" >/dev/null
i=0
until podman exec "$name" test -S /run/user/1000/bus 2>/dev/null; do
	i=$((i + 1)); [ $i -lt 60 ] || fail "o systemd de usuario non arrancou"; sleep 1
done

echo "### Instalar como usuario"
# O contorno usa pairsrv por http (http://pairsrv:8080): só se acepta en
# desenvolvemento. Drop-in do servizo con SYNCPLY_DEV=1 (consérvase ao reiniciar).
as_ana "$name" sh -c 'mkdir -p ~/.config/systemd/user/syncply.service.d &&
	printf "[Service]\nEnvironment=SYNCPLY_DEV=1\n" >~/.config/systemd/user/syncply.service.d/dev.conf'
# Navegador falso: recarga a páxina de progreso (como fai o meta refresh) ata
# que redirixe á web local, e garda as páxinas vistas.
podman exec -i -u ana -w /home/ana "$name" sh -c 'mkdir -p ~/bin && cat >~/bin/xdg-open && chmod +x ~/bin/xdg-open' <<'XDG'
#!/bin/sh
(
	i=0
	while [ $i -lt 120 ]; do
		r=$(curl -s -o /tmp/progreso-$i.html -w "%{http_code} %{redirect_url}" "$1") || exit 1
		case "$r" in "303 "*) echo "${r#303 }" >/tmp/redirixe; curl -s -c /tmp/xj -L -o /tmp/web.html "${r#303 }"; exit 0 ;; esac
		grep -q 'http-equiv="refresh"' /tmp/progreso-$i.html || exit 0
		i=$((i + 1)); sleep 1
	done
) >/dev/null 2>&1 &
XDG
[ "$(as_ana "$name" sh -c 'mkdir pkg && tar xzf /tmp/syncply.tar.gz -C pkg && ls -A pkg')" = syncply ] || fail "o paquete non leva só syncply"
as_ana "$name" test -x pkg/syncply || fail "syncply non é executable"
ok "o paquete leva só syncply (executable)"
as_ana "$name" env PATH=/home/ana/bin:/usr/bin:/bin DISPLAY=:0 pkg/syncply | sed 's/^/  /'
[ "$(as_ana "$name" systemctl --user is-active syncply.service)" = active ] || fail "servizo inactivo"
ok "servizo de usuario syncply activo"
as_ana "$name" test -S /home/ana/.config/syncply/syncthing/gui.sock || fail "sen socket da API"
ok "API de Syncthing no socket Unix"
as_ana "$name" grep -q "Instalando Syncply" /tmp/progreso-0.html || fail "o navegador non viu a páxina de progreso"
as_ana "$name" grep -q "/login?t=" /tmp/redirixe || fail "a páxina de progreso non redirixiu á web local"
as_ana "$name" grep -q "Vincular este equipo" /tmp/web.html || fail "a redirección non abriu a web local"
ok "progreso no navegador e, ao rematar, a web local para vincular"
as_ana "$name" test -f /home/ana/.local/share/syncply/bin/LICENZAS.txt || fail "sen LICENZAS.txt"

echo "### Vincular como alice desde a web (texto + código) e sincronizar /comun"
# Con make fresh /comun está baleiro: un ficheiro que teña que chegar.
echo "proba do instalador" >.dev/sftpgo/data/shared/comun/installtest.txt
blob=$(make -s blob U=alice)
case "$blob" in syncply1:*) ;; *) fail "sen texto de vinculación: $blob" ;; esac
otp=$(make -s otp U=alice | cut -d' ' -f1)
as_ana "$name" env BLOB="$blob" OTP="$otp" bash -c '
set -eu
U=$(~/.local/share/syncply/bin/syncply open -print); B=${U%%/login*}
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
	--data-urlencode path=/home/ana/syncply/comun "$B/accept")
case "$r" in *ok=*) echo "  OK  /comun aceptado" ;; *) echo "ERRO ao aceptar: $r"; exit 1 ;; esac
for i in $(seq 1 45); do [ -n "$(ls ~/syncply/comun 2>/dev/null)" ] && break; sleep 2; done
n=$(ls ~/syncply/comun | wc -l); [ "$n" -gt 0 ] || { echo "ERRO: non chegou ningún ficheiro"; exit 1; }
echo "  OK  recibidos $n ficheiros en ~/syncply/comun"'

echo "### Reiniciar o equipo"
podman restart -t 5 "$name" >/dev/null
i=0
until as_ana "$name" /home/ana/.local/share/syncply/bin/syncply open -print >/dev/null 2>&1; do
	i=$((i + 1)); [ $i -lt 60 ] || fail "o servizo non volveu tras reiniciar"; sleep 1
done
ok "o servizo arranca só ao iniciar"

echo "### Revogar este equipo (para que a proba se poida repetir: alice ten un límite de equipos)"
otp=$(make -s otp U=alice | cut -d' ' -f1)
as_ana "$name" env OTP="$otp" bash -c '
set -eu
ID=$(~/.local/share/syncply/bin/syncthing device-id --home="$HOME/.config/syncply/syncthing")
U=$(~/.local/share/syncply/bin/syncply open -print); B=${U%%/login*}
curl -sf -c /tmp/j -o /dev/null "$U"
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" -H "Origin: $B" -H "Sec-Fetch-Site: same-origin" \
	--data-urlencode id="$ID" --data-urlencode otp="$OTP" --data-urlencode back=/devices "$B/revoke")
case "$r" in *ok=*) echo "  OK  equipo revogado" ;; *) echo "ERRO ao revogar: $r"; exit 1 ;; esac'

echo "### Desinstalar"
as_ana "$name" /home/ana/.local/share/syncply/bin/syncply uninstall >/dev/null
as_ana "$name" test ! -e /home/ana/.local/share/syncply || fail "quedan ficheiros en ~/.local/share/syncply"
[ "$(as_ana "$name" systemctl --user is-active syncply.service || true)" != active ] || fail "o servizo segue activo"
as_ana "$name" sh -c '! ps -o args -u ana | grep -q "[s]yncthing serve"' || fail "segue Syncthing en marcha"
as_ana "$name" test -d /home/ana/syncply/comun || fail "borráronse as carpetas sincronizadas"
ok "desinstalado; consérvanse configuración e carpetas"
echo
echo "Instalador de Linux: todo correcto."
