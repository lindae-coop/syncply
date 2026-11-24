#!/bin/sh
# Proba as actualizacións automáticas do cliente de Linux nun Debian 13 con
# systemd (a mesma imaxe que installtest):
#   instalar v0.0.1 → vincular como alice e aceptar /comun → o servidor publica
#   v0.0.2 asinada → `syncply update` → o servizo volve con v0.0.2 (mesmo PID,
#   mesma vinculación) e segue sincronizando → manifesto manipulado rexeitado.
# Asina cunha clave de desenvolvemento (.dev/update-dev.key), que o cliente só
# acepta con SYNCPLY_DEV=1 e SYNCPLY_UPDATE_PUBKEY.
# Require `make up` (con SFTPGo preparado: make demo ou make fresh). Deixa en
# dist/ o paquete de Linux v0.0.2 (sen manifesto).
set -eu
cd "$(dirname "$0")/.."

img=localhost/syncply-installtest:latest
name=syncply-updatetest
key=.dev/update-dev.key
tar=dist/syncply-linux-amd64.tar.gz
old=.dev/updatetest/syncply-v0.0.1.tar.gz

ok() { printf '  OK  %s\n' "$*"; }
fail() { printf 'ERRO: %s\n' "$*" >&2; exit 1; }
as_ana() { podman exec -u ana -w /home/ana -e XDG_RUNTIME_DIR=/run/user/1000 "$@"; }
relsign() { podman-compose run --rm tools go run ./cmd/relsign "$@" 2>/dev/null; }
sign() { relsign manifest -key "$key" -version v0.0.2 "$tar" >/dev/null 2>&1; }
cleanup() {
	podman rm -f -t 2 "$name" >/dev/null 2>&1 || true
	rm -f dist/update.json dist/update.json.sig .dev/sftpgo/data/shared/comun/actualizado-*.txt
}

cleanup
trap cleanup EXIT
echo "### Paquetes v0.0.1 (instalado) e v0.0.2 (publicado no servidor)"
[ -f "$key" ] || relsign keygen -out "$key" >/dev/null
pub=$(relsign pubkey -key "$key" | tail -1)
mkdir -p "$(dirname "$old")"
make -s dist VERSION=v0.0.1 >/dev/null && cp "$tar" "$old"
make -s dist VERSION=v0.0.2 >/dev/null
sign || fail "non se puido asinar"
ok "dist/update.json asinado (v0.0.2)"

podman image exists "$img" || podman build -q -t "$img" -f dev/Containerfile.installtest dev >/dev/null
podman run -d --name "$name" --hostname equipo-actualizado --systemd=always --network syncply_stnet \
	-v "./$old:/tmp/syncply.tar.gz:ro,Z" "$img" >/dev/null
i=0
until podman exec "$name" test -S /run/user/1000/bus 2>/dev/null; do
	i=$((i + 1)); [ $i -lt 60 ] || fail "o systemd de usuario non arrancou"; sleep 1
done

echo "### Instalar v0.0.1, vincular como alice e aceptar /comun"
as_ana "$name" env PUB="$pub" sh -c 'mkdir -p ~/.config/systemd/user/syncply.service.d &&
	printf "[Service]\nEnvironment=SYNCPLY_DEV=1\nEnvironment=SYNCPLY_UPDATE_PUBKEY=%s\n" "$PUB" \
	>~/.config/systemd/user/syncply.service.d/dev.conf'
as_ana "$name" sh -c 'tar xzf /tmp/syncply.tar.gz && syncply-linux-amd64/install.sh --no-open' >/dev/null
bin=/home/ana/.local/share/syncply/bin
[ "$(as_ana "$name" $bin/syncply version)" = "syncply v0.0.1" ] || fail "non é v0.0.1"
blob=$(make -s blob U=alice)
otp=$(make -s otp U=alice | cut -d' ' -f1)
as_ana "$name" env BLOB="$blob" OTP="$otp" bash -c '
set -eu
U=$(~/.local/share/syncply/bin/syncply open -print); B=${U%%/login*}
H=(-H "Origin: $B" -H "Sec-Fetch-Site: same-origin")
curl -sf -c /tmp/j -o /dev/null "$U"
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" "${H[@]}" --data-urlencode linktext="$BLOB" \
	--data-urlencode otp="$OTP" --data-urlencode device=equipo-actualizado "$B/pair")
case "$r" in *ok=*) ;; *) echo "ERRO ao vincular: $r"; exit 1 ;; esac
for i in $(seq 1 30); do curl -s -b /tmp/j "$B/" | grep -q "sftpgo-folder-comun" && break; sleep 2; done
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" "${H[@]}" --data-urlencode id=sftpgo-folder-comun \
	--data-urlencode path=/home/ana/syncply/comun "$B/accept")
case "$r" in *ok=*) ;; *) echo "ERRO ao aceptar: $r"; exit 1 ;; esac
curl -s -b /tmp/j "$B/devices" | grep -q "Syncply <strong>v0.0.1</strong>" || { echo "ERRO: a web non mostra v0.0.1"; exit 1; }'
ok "v0.0.1 vinculado e sincronizando /comun"
pid=$(as_ana "$name" systemctl --user show -p MainPID --value syncply.service)

echo "### Actualizar (syncply update)"
out=$(as_ana "$name" $bin/syncply update 2>&1) || fail "syncply update: $out"
case "$out" in *"Instalando a versión v0.0.2"*) ok "$out" ;; *) fail "resposta inesperada: $out" ;; esac
i=0
until as_ana "$name" sh -c "journalctl --user -u syncply -o cat | grep -q 'syncply v0.0.2: web en'"; do
	i=$((i + 1)); [ $i -lt 60 ] || fail "o daemon non volveu con v0.0.2"; sleep 1
done
[ "$(as_ana "$name" $bin/syncply version)" = "syncply v0.0.2" ] || fail "o binario instalado non é v0.0.2"
[ "$(as_ana "$name" systemctl --user is-active syncply.service)" = active ] || fail "servizo inactivo"
[ "$(as_ana "$name" systemctl --user show -p MainPID --value syncply.service)" = "$pid" ] || fail "cambiou o PID (non foi reexec)"
as_ana "$name" test -x $bin/syncply.old || fail "non quedou a copia syncply.old"
[ "$(as_ana "$name" sh -c 'ps -o args= -u ana | grep -c "[s]yncthing serve"')" -ge 1 ] || fail "Syncthing non volveu arrancar"
ok "daemon v0.0.2 co mesmo PID ($pid), Syncthing en marcha, copia da versión anterior"

f=actualizado-$(date +%s).txt
echo "despois da actualización" >.dev/sftpgo/data/shared/comun/$f
i=0
until as_ana "$name" test -f /home/ana/syncply/comun/$f; do
	i=$((i + 1)); [ $i -lt 45 ] || fail "non chegou $f despois de actualizar"; sleep 2
done
as_ana "$name" bash -c '
set -eu
U=$(~/.local/share/syncply/bin/syncply open -print); B=${U%%/login*}
curl -sf -c /tmp/j -o /dev/null "$U"
curl -s -b /tmp/j "$B/devices" | grep -q "Syncply <strong>v0.0.2</strong>" || { echo "ERRO: a web non mostra v0.0.2"; exit 1; }
curl -s -b /tmp/j "$B/devices" | grep -q "alice" || { echo "ERRO: perdeuse a vinculación"; exit 1; }'
ok "segue vinculado como alice e sincronizando (chegou $f)"

out=$(as_ana "$name" $bin/syncply update 2>&1) || fail "syncply update: $out"
case "$out" in *"última versión (v0.0.2)"*) ok "$out" ;; *) fail "resposta inesperada: $out" ;; esac

echo "### Rexeitar un manifesto manipulado"
sed -i 's/"v0.0.2"/"v0.0.3"/' dist/update.json
out=$(as_ana "$name" $bin/syncply update 2>&1) && fail "aceptou un manifesto manipulado: $out"
case "$out" in *sinatura*) ok "rexeitado: $out" ;; *) fail "erro inesperado: $out" ;; esac

echo "### Revogar este equipo (para poder repetir a proba)"
otp=$(make -s otp U=alice | cut -d' ' -f1)
as_ana "$name" env OTP="$otp" bash -c '
set -eu
ID=$(~/.local/share/syncply/bin/syncthing device-id --home="$HOME/.config/syncply/syncthing")
U=$(~/.local/share/syncply/bin/syncply open -print); B=${U%%/login*}
curl -sf -c /tmp/j -o /dev/null "$U"
r=$(curl -s -b /tmp/j -o /dev/null -w "%{redirect_url}" -H "Origin: $B" -H "Sec-Fetch-Site: same-origin" \
	--data-urlencode id="$ID" --data-urlencode otp="$OTP" --data-urlencode back=/devices "$B/revoke")
case "$r" in *ok=*) ;; *) echo "ERRO ao revogar: $r"; exit 1 ;; esac'
ok "equipo revogado"
echo
echo "Actualizacións do cliente de Linux: todo correcto."
