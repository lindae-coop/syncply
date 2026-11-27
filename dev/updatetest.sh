#!/bin/sh
# Proba as actualizacións automáticas do cliente de Linux nun Debian 13 con
# systemd (a mesma imaxe que installtest), incluída a migración ao paquete
# dun só ficheiro:
#   instalar v0.0.1 (código de v0.4.1 con install.sh: o formato anterior) →
#   vincular como alice e aceptar /comun → o servidor publica v0.0.2 →
#   `syncply update` instala o paquete anterior (clave linux-amd64) → o servizo
#   volve con v0.0.2 (mesmo PID, mesma vinculación) e instala o seu Syncthing
#   embebido → o servidor publica v0.0.3 → `syncply update` instala o paquete
#   novo (clave linux-amd64-v2, só syncply) → segue sincronizando →
#   manifesto manipulado rexeitado.
# Asina cunha clave de desenvolvemento (.dev/update-dev.key), que o cliente só
# acepta con SYNCPLY_DEV=1 e SYNCPLY_UPDATE_PUBKEY.
# Require `make up` (con SFTPGo preparado: make demo ou make fresh). Deixa en
# dist/ os paquetes de Linux v0.0.3 (sen manifesto).
set -eu
cd "$(dirname "$0")/.."

img=localhost/syncply-installtest:latest
name=syncply-updatetest
key=.dev/update-dev.key
tar=dist/syncply-linux-amd64.tar.gz
legacy=dist/syncply-linux-amd64-anterior.tar.gz
work=.dev/updatetest
old=$work/syncply-v0.0.1.tar.gz
oldtag=v0.4.1 # última versión co instalador install.sh

ok() { printf '  OK  %s\n' "$*"; }
fail() { printf 'ERRO: %s\n' "$*" >&2; exit 1; }
as_ana() { podman exec -u ana -w /home/ana -e XDG_RUNTIME_DIR=/run/user/1000 "$@"; }
tools() { podman-compose run --rm tools "$@" 2>/dev/null; }
relsign() { tools go run ./cmd/relsign "$@"; }
publish() { # versión: constrúe e asina os paquetes de Linux
	make -s dist VERSION="$1" >/dev/null 2>&1 || fail "make dist $1"
	relsign manifest -key "$key" -version "$1" "$tar" "$legacy" >/dev/null || fail "non se puido asinar $1"
}
cleanup() {
	podman rm -f -t 2 "$name" >/dev/null 2>&1 || true
	rm -f dist/update.json dist/update.json.sig .dev/sftpgo/data/shared/comun/actualizado-*.txt
}
wait_version() { # versión: o daemon volveu con ela
	i=0
	until as_ana "$name" sh -c "journalctl --user -u syncply -o cat | grep -q 'syncply $1: web en'"; do
		i=$((i + 1)); [ $i -lt 60 ] || fail "o daemon non volveu con $1"; sleep 1
	done
	[ "$(as_ana "$name" $bin/syncply version)" = "syncply $1" ] || fail "o binario instalado non é $1"
	[ "$(as_ana "$name" systemctl --user is-active syncply.service)" = active ] || fail "servizo inactivo"
	[ "$(as_ana "$name" systemctl --user show -p MainPID --value syncply.service)" = "$pid" ] || fail "cambiou o PID (non foi reexec)"
	as_ana "$name" test -x $bin/syncply.old || fail "non quedou a copia syncply.old"
	i=0
	until [ "$(as_ana "$name" sh -c 'ps -o args= -u ana | grep -c "[s]yncthing serve"')" -ge 1 ]; do
		i=$((i + 1)); [ $i -lt 30 ] || fail "Syncthing non volveu arrancar"; sleep 1
	done
}
check_sync() { # versión
	f=actualizado-$(date +%s).txt
	echo "despois da actualización" >.dev/sftpgo/data/shared/comun/$f
	i=0
	until as_ana "$name" test -f /home/ana/syncply/comun/$f; do
		i=$((i + 1)); [ $i -lt 45 ] || fail "non chegou $f despois de actualizar"; sleep 2
	done
	as_ana "$name" env V="$1" bash -c '
	set -eu
	U=$(~/.local/share/syncply/bin/syncply open -print); B=${U%%/login*}
	curl -sf -c /tmp/j -o /dev/null "$U"
	curl -s -b /tmp/j "$B/devices" | grep -q "Syncply <strong>$V</strong>" || { echo "ERRO: a web non mostra $V"; exit 1; }
	curl -s -b /tmp/j "$B/devices" | grep -q "alice" || { echo "ERRO: perdeuse a vinculación"; exit 1; }'
	ok "segue vinculado como alice e sincronizando (chegou $f)"
}

cleanup
trap cleanup EXIT
echo "### Paquetes v0.0.1 (instalado, formato anterior) e v0.0.2 (publicado no servidor)"
[ -f "$key" ] || relsign keygen -out "$key" >/dev/null
pub=$(relsign pubkey -key "$key" | tail -1)
rm -rf "$work/src" "$work/pkg"
mkdir -p "$work/src" "$work/pkg/syncply-linux-amd64"
git archive "$oldtag" | tar -x -C "$work/src"
tools sh -c "cd $work/src && GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X main.version=v0.0.1' \
	-o ../pkg/syncply-linux-amd64/syncply ./cmd/syncply" || fail "non se puido compilar $oldtag"
podman run --rm --entrypoint cat docker.io/syncthing/syncthing:2.1.7 /bin/syncthing >"$work/pkg/syncply-linux-amd64/syncthing"
cp "$work/src/packaging/linux/install.sh" "$work/pkg/syncply-linux-amd64/"
chmod 755 "$work/pkg/syncply-linux-amd64/"*
tar -C "$work/pkg" -czf "$old" syncply-linux-amd64
ok "v0.0.1 compilado desde $oldtag (install.sh)"
publish v0.0.2
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

echo "### Actualizar v0.0.1 → v0.0.2 (paquete no formato anterior)"
out=$(as_ana "$name" $bin/syncply update 2>&1) || fail "syncply update: $out"
case "$out" in *"Instalando a versión v0.0.2"*) ok "$out" ;; *) fail "resposta inesperada: $out" ;; esac
wait_version v0.0.2
as_ana "$name" sh -c "journalctl --user -u syncply -o cat | grep -q 'instalado o Syncthing embebido'" ||
	fail "v0.0.2 non instalou o seu Syncthing embebido"
as_ana "$name" test -f $bin/.syncthing.sha256 || fail "sen marca do Syncthing embebido"
ok "daemon v0.0.2 co mesmo PID ($pid), co seu Syncthing embebido instalado"
check_sync v0.0.2

echo "### Actualizar v0.0.2 → v0.0.3 (paquete novo: só syncply)"
publish v0.0.3
tar tzf "$tar" | grep -qx syncply || fail "o paquete novo non leva syncply na raíz"
out=$(as_ana "$name" $bin/syncply update 2>&1) || fail "syncply update: $out"
case "$out" in *"Instalando a versión v0.0.3"*) ok "$out" ;; *) fail "resposta inesperada: $out" ;; esac
wait_version v0.0.3
as_ana "$name" sh -c "journalctl --user -u syncply -o cat | grep 'actualización: v0.0.2 → v0.0.3' | grep -q 'syncply-linux-amd64.tar.gz'" ||
	fail "v0.0.2 non usou o paquete novo"
ok "daemon v0.0.3 co mesmo PID ($pid) desde o paquete dun só ficheiro"
check_sync v0.0.3

out=$(as_ana "$name" $bin/syncply update 2>&1) || fail "syncply update: $out"
case "$out" in *"última versión (v0.0.3)"*) ok "$out" ;; *) fail "resposta inesperada: $out" ;; esac

echo "### Rexeitar un manifesto manipulado"
sed -i 's/"v0.0.3"/"v0.0.4"/' dist/update.json
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
