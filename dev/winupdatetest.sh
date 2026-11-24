#!/bin/sh
# Proba as actualizacións automáticas do cliente de Windows na VM
# (dev/winvm), como usuaria normal (ana):
#   instalar v0.0.1 → vincular como alice e aceptar /comun → o servidor publica
#   v0.0.2 asinada → `syncply update` → o daemon executa o novo syncply.exe
#   como instalador (install -update) → v0.0.2 instalada e en marcha, segue
#   vinculada e sincronizando.
# Asina cunha clave de desenvolvemento (.dev/update-dev.key), que o cliente só
# acepta con SYNCPLY_DEV=1 e SYNCPLY_UPDATE_PUBKEY.
# Require `make up` (con SFTPGo preparado) e Windows instalado
# (`make winvm-install`). Deixa en dist/ o instalador de Windows v0.0.2.
set -eu
cd "$(dirname "$0")/.."

img=localhost/syncply-winvm:latest
name=syncply-winvm
key=.dev/update-dev.key
exe=dist/syncply-windows-amd64.exe
old=dist/updatetest-v0.0.1.exe

ok() { printf '  OK  %s\n' "$*"; }
fail() { printf 'ERRO: %s\n' "$*" >&2; exit 1; }
win() { podman exec -i -e WINVM_TIMEOUT="${T:-300}" "$name" python3 /winvm/winvm.py exec; }
shot() { podman exec "$name" python3 /winvm/winvm.py shot "/work/$1.png" && echo "  captura: .winvm/$1.png"; }
relsign() { podman-compose run --rm tools go run ./cmd/relsign "$@" 2>/dev/null; }
cleanup() {
	[ -n "${KEEP:-}" ] || podman rm -f -t 5 "$name" >/dev/null 2>&1 || true
	rm -f dist/update.json dist/update.json.sig "$old" .dev/sftpgo/data/shared/comun/winactualizado-*.txt
}

[ -f .winvm/base.ok ] || fail "Windows sen instalar (make winvm-install)"
cleanup
trap cleanup EXIT

echo "### Instaladores v0.0.1 (instalado) e v0.0.2 (publicado no servidor)"
[ -f "$key" ] || relsign keygen -out "$key" >/dev/null
pub=$(relsign pubkey -key "$key" | tail -1)
make -s dist-windows VERSION=v0.0.1 >/dev/null && cp "$exe" "$old"
make -s dist-windows VERSION=v0.0.2 >/dev/null
relsign manifest -key "$key" -version v0.0.2 "$exe" >/dev/null || fail "non se puido asinar"
ok "dist/update.json asinado (v0.0.2)"

podman build -q -t "$img" dev/winvm >/dev/null
podman rm -f -t 2 "$name" >/dev/null 2>&1 || true
podman run -d --name "$name" --device /dev/kvm --network syncply_stnet \
	-v ./.winvm:/work -v ./dist:/dist:ro -p 127.0.0.1:5900:5900 "$img" run >/dev/null
echo "### Arrancar Windows (VNC en 127.0.0.1:5900)"
podman exec "$name" python3 /winvm/winvm.py wait 600 || { shot fallo-arranque; fail "Windows non arrancou"; }
echo 'curl.exe -sf -o C:\winvm\agent.ps1 http://10.0.2.2:8000/files/agent.ps1' | win >/dev/null || true
ok "sesión de ana iniciada"

echo "### Instalar v0.0.1, vincular como alice e aceptar /comun"
blob=$(make -s blob U=alice)
otp=
vars() { printf '$Blob = "%s"; $Otp = "%s"; $Pub = "%s"\n' "$@"; }
{ vars "$blob" "$otp" "$pub"; cat <<'EOF'; } | T=300 win || { shot fallo-instalar; fail "non se pode instalar e vincular"; }
$ErrorActionPreference = 'Stop'
foreach ($v in @(@('SYNCPLY_DEV', '1'), @('SYNCPLY_UPDATE_PUBKEY', $Pub))) {
	[Environment]::SetEnvironmentVariable($v[0], $v[1], 'User'); Set-Item "env:$($v[0])" $v[1]
}
$f = "$env:USERPROFILE\Downloads\syncply.exe"
curl.exe -sf -o $f http://pairsrv:8080/descargas/updatetest-v0.0.1.exe; if ($LASTEXITCODE) { throw 'descarga' }
$out = & $f install -no-open 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { throw "install: $out" }
$exe = "$env:LOCALAPPDATA\Programs\syncply\syncply.exe"
$v = (& $exe version | Out-String).Trim()
if ($v -ne 'syncply v0.0.1') { throw "versión: $v" }
# Primeiro arranque: Syncthing tarda en xerar a identidade; vincular antes falla.
$stlog = "$env:LOCALAPPDATA\syncply\syncthing\syncthing.log"
for ($i = 0; $i -lt 90; $i++) { if ((Test-Path $stlog) -and (Select-String -Quiet 'GUI and API listening' $stlog)) { break }; Start-Sleep 1 }
EOF

# O código caduca en 30-60 s e a orde pode tardar en chegar ao axente:
# ata 3 intentos cun código novo.
paired=
for attempt in 1 2 3; do
	otp=$(make -s otp U=alice | cut -d' ' -f1)
	if { vars "$blob" "$otp" "$pub"; cat <<'EOF'; } | T=120 win; then paired=1; break; fi
$ErrorActionPreference = 'Stop'
$exe = "$env:LOCALAPPDATA\Programs\syncply\syncply.exe"
$j = "$env:TEMP\cookies.txt"
$U = (& $exe open -print | Out-String).Trim(); $B = $U -replace '/login.*$', ''
$H = @('-H', "Origin: $B", '-H', 'Sec-Fetch-Site: same-origin')
curl.exe -sf -c $j -o NUL $U; if ($LASTEXITCODE) { throw "login $U" }
$r = curl.exe -s -b $j -o NUL -w '%{redirect_url}' @H --data-urlencode "linktext=$Blob" `
	--data-urlencode "otp=$Otp" --data-urlencode "device=windows-actualizado" "$B/pair"
if (-not ("$r" -match '\?ok=')) { throw "ao vincular: $r" }
EOF
	echo "  (intento $attempt de vincular fallou; código novo)"
done
[ -n "$paired" ] || { shot fallo-vincular; fail "non se pode vincular"; }
{ vars "$blob" "" "$pub"; cat <<'EOF'; } | T=300 win || { shot fallo-aceptar; fail "non se pode aceptar /comun"; }
$ErrorActionPreference = 'Stop'
$exe = "$env:LOCALAPPDATA\Programs\syncply\syncply.exe"
$j = "$env:TEMP\cookies.txt"
$U = (& $exe open -print | Out-String).Trim(); $B = $U -replace '/login.*$', ''
$H = @('-H', "Origin: $B", '-H', 'Sec-Fetch-Site: same-origin')
curl.exe -sf -c $j -o NUL $U; if ($LASTEXITCODE) { throw "login $U" }
$offer = $false
for ($i = 0; $i -lt 90; $i++) { if ((curl.exe -s -b $j "$B/" | Out-String) -match 'sftpgo-folder-comun') { $offer = $true; break }; Start-Sleep 2 }
if (-not $offer) { throw 'o servidor non ofreceu /comun en 3 min' }
"  OK  vinculado; oferta de /comun en $(2*$i) s"
$r = curl.exe -s -b $j -o NUL -w '%{redirect_url}' @H --data-urlencode "id=sftpgo-folder-comun" `
	--data-urlencode "path=$env:USERPROFILE\syncply\comun" "$B/accept"
if (-not ("$r" -match '\?ok=')) { throw "ao aceptar: $r" }
"  OK  v0.0.1 instalado, vinculado e sincronizando /comun"
EOF

echo "### Actualizar (syncply update)"
T=400 win <<'EOF' || { shot fallo-actualizar; fail "non se actualizou"; }
$ErrorActionPreference = 'Stop'
$d = "$env:LOCALAPPDATA\Programs\syncply"
# A comprobación automática (2 min ±20 % tras arrancar) pode ter empezado xa.
$ErrorActionPreference = 'Continue'
$out = (& "$d\syncply.exe" update 2>&1 | Out-String).Trim()
$ErrorActionPreference = 'Stop'
if ($out -match 'Instalando a versión v0.0.2') { "  OK  $out" }
else { "  OK  a comprobación automática xa estaba actualizando ($out)" }
$key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\syncply'
for ($i = 0; $i -lt 120; $i++) {
	if ((Get-ItemProperty $key).DisplayVersion -eq 'v0.0.2' -and (Get-Process syncthing -ErrorAction SilentlyContinue)) { break }
	Start-Sleep 1
}
$v = (& "$d\syncply.exe" version | Out-String).Trim()
if ($v -ne 'syncply v0.0.2') { throw "versión instalada: $v" }
if ((Get-ItemProperty $key).DisplayVersion -ne 'v0.0.2') { throw "Aplicacións instaladas: $((Get-ItemProperty $key).DisplayVersion)" }
$p = @(Get-Process syncply -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq "$d\syncply.exe" })
if ($p.Count -ne 1) { throw "procesos syncply instalados: $($p.Count)" }
if (-not (Get-Process syncthing -ErrorAction SilentlyContinue)) { throw 'sen syncthing.exe' }
"  OK  v0.0.2 instalada (Aplicacións instaladas: v0.0.2); daemon e Syncthing en marcha"
for ($i = 0; $i -lt 30; $i++) {
	$log = Get-Content -Encoding UTF8 "$env:LOCALAPPDATA\syncply\syncply.log" -Raw
	if ($log -match 'syncply v0.0.2: web en') { break }; Start-Sleep 1
}
if ($log -notmatch 'actualización: v0.0.1 → v0.0.2' -or $log -notmatch 'syncply v0.0.2: actualizando' -or $log -notmatch 'syncply v0.0.2: web en') { throw "o rexistro non mostra a actualización: $(($log -split "`n" | Select-String 'syncply|actualiz') -join ' | ')" }
"  OK  rexistro: v0.0.1 descargou v0.0.2, instalador v0.0.2 e daemon v0.0.2"
$out = (& "$d\syncply.exe" update 2>&1 | Out-String).Trim()
if ($out -notmatch 'última versión \(v0.0.2\)') { throw "update: $out" }
"  OK  $out"
EOF

echo "### Segue sincronizando"
f=winactualizado-$(date +%s).txt
echo "despois da actualización" >.dev/sftpgo/data/shared/comun/$f
printf '$F = "%s"\n%s\n' "$f" '
$ErrorActionPreference = "Stop"
for ($i = 0; $i -lt 60; $i++) { if (Test-Path "$env:USERPROFILE\syncply\comun\$F") { break }; Start-Sleep 2 }
if (-not (Test-Path "$env:USERPROFILE\syncply\comun\$F")) { throw "non chegou $F" }
"  OK  chegou $F"' | T=200 win || fail "non sincroniza despois de actualizar"

echo "### Revogar este equipo (para poder repetir a proba)"
otp=$(make -s otp U=alice | cut -d' ' -f1)
{ vars "" "$otp" ""; cat <<'EOF'; } | win || fail "non se pode revogar"
$ErrorActionPreference = 'Stop'
$d = "$env:LOCALAPPDATA\Programs\syncply"
$ID = (& "$d\syncthing.exe" device-id "--home=$env:LOCALAPPDATA\syncply\syncthing" | Out-String).Trim()
$U = (& "$d\syncply.exe" open -print | Out-String).Trim(); $B = $U -replace '/login.*$', ''
$j = "$env:TEMP\cookies.txt"
curl.exe -sf -c $j -o NUL $U
$r = curl.exe -s -b $j -o NUL -w '%{redirect_url}' -H "Origin: $B" -H 'Sec-Fetch-Site: same-origin' `
	--data-urlencode "id=$ID" --data-urlencode "otp=$Otp" --data-urlencode 'back=/devices' "$B/revoke"
if ($r -notmatch 'ok=') { throw "ao revogar: $r" }
"  OK  equipo revogado"
EOF
echo
echo "Actualizacións do cliente de Windows: todo correcto."
