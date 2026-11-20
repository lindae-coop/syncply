#!/bin/sh
# Proba o instalador de Windows (dist/syncply-windows-amd64.exe) nunha VM con
# Windows 11 (dev/winvm), conectada á rede do contorno, como usuaria normal
# (ana, sen administrador):
#   descargar de pairsrv → instalar → vincular como alice desde a web local →
#   aceptar /comun e sincronizar nos dous sentidos → reiniciar Windows →
#   revogar o equipo → desinstalar.
# Require `make up` (con SFTPGo preparado: make demo ou make fresh),
# `make dist-windows` e Windows instalado (`make winvm-install`). Cada proba
# parte dunha copia nova do Windows recén instalado.
set -eu
cd "$(dirname "$0")/.."

img=localhost/syncply-winvm:latest
name=syncply-winvm
exe=dist/syncply-windows-amd64.exe

ok() { printf '  OK  %s\n' "$*"; }
fail() { printf 'ERRO: %s\n' "$*" >&2; exit 1; }
# win: executa en Windows (como ana) o PowerShell da entrada estándar.
win() { podman exec -i -e WINVM_TIMEOUT="${T:-300}" "$name" python3 /winvm/winvm.py exec; }
shot() { podman exec "$name" python3 /winvm/winvm.py shot "/work/$1.png" && echo "  captura: .winvm/$1.png"; }

[ -f "$exe" ] || fail "falta $exe (make dist-windows)"
[ -f .winvm/base.ok ] || fail "Windows sen instalar (make winvm-install)"
podman build -q -t "$img" dev/winvm >/dev/null
podman rm -f -t 2 "$name" >/dev/null 2>&1 || true
[ -n "${KEEP:-}" ] || trap 'podman rm -f -t 5 "$name" >/dev/null 2>&1 || true' EXIT

podman run -d --name "$name" --device /dev/kvm --network syncply_stnet \
	-v ./.winvm:/work -v ./dist:/dist:ro -p 127.0.0.1:5900:5900 "$img" run >/dev/null
echo "### Arrancar Windows (VNC en 127.0.0.1:5900)"
podman exec "$name" python3 /winvm/winvm.py wait 600 || { shot fallo-arranque; fail "Windows non arrancou"; }
ok "sesión de ana iniciada"
# Axente actualizado para os seguintes arranques (o do Windows instalado pode ser anterior).
echo 'curl.exe -sf -o C:\winvm\agent.ps1 http://10.0.2.2:8000/files/agent.ps1' | win >/dev/null || true

echo "### Descargar o instalador de pairsrv e instalar como ana"
win <<'EOF' || fail "non se pode instalar"
$ErrorActionPreference = 'Stop'
$me = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if ($me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'ana non debería ser administradora' }
$f = "$env:USERPROFILE\Downloads\syncply.exe"
# curl.exe: Invoke-WebRequest de PowerShell 5.1 truncou a descarga (10 de 24 MB) na VM.
curl.exe -sf -o $f http://pairsrv:8080/descargas/syncply-windows-amd64.exe; if ($LASTEXITCODE) { throw 'descarga' }
"  OK  descargado de http://pairsrv:8080 ($((Get-Item $f).Length) bytes)"
# O contorno usa pairsrv por http: só se acepta en desenvolvemento (persiste no perfil).
[Environment]::SetEnvironmentVariable('SYNCPLY_DEV', '1', 'User'); $env:SYNCPLY_DEV = '1'
$out = & $f install -no-open 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { throw "install: $out" }
$d = "$env:LOCALAPPDATA\Programs\syncply"
foreach ($x in 'syncply.exe', 'syncthing.exe', 'LICENZAS.txt') { if (-not (Test-Path "$d\$x")) { throw "falta $d\$x" } }
"  OK  binarios en $d"
$run = (Get-ItemProperty HKCU:\Software\Microsoft\Windows\CurrentVersion\Run).syncply
if ($run -ne "`"$d\syncply.exe`" daemon") { throw "Run: $run" }
"  OK  arranque ao iniciar sesión: $run"
$u = Get-ItemProperty HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\syncply
"  OK  en Aplicacións instaladas: $($u.DisplayName) $($u.DisplayVersion) ($($u.EstimatedSize) KB)"
$lnk = "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Syncply.lnk"
$s = (New-Object -ComObject WScript.Shell).CreateShortcut($lnk)
if ($s.TargetPath -ne "$d\syncply.exe" -or $s.Arguments -ne 'open') { throw "acceso directo: $($s.TargetPath) $($s.Arguments)" }
"  OK  acceso directo no menú Inicio"
$p = Get-Process syncply, syncthing -ErrorAction SilentlyContinue | Group-Object Name | ForEach-Object { "$($_.Name)×$($_.Count)" }
if (-not ($p -match 'syncthing')) { throw "sen procesos: $p" }
"  OK  en marcha: $($p -join ', ')"
EOF

# Un ficheiro novo no servidor (make fresh deixa /comun baleiro).
d=.dev/sftpgo/data/shared/comun
mkdir -p "$d"
rm -f "$d"/desde-servidor-*.txt "$d"/desde-windows-*.txt
echo "Ola desde o servidor $(date -Is)" >"$d/desde-servidor-$$.txt"
wf=desde-windows-$$.txt

echo "### Vincular como alice desde a web local (texto + código) e sincronizar /comun"
blob=$(make -s blob U=alice)
case "$blob" in syncply1:*) ;; *) fail "sen texto de vinculación: $blob" ;; esac
otp=$(make -s otp U=alice | cut -d' ' -f1)
# Valores ao principio do script; o resto vai literal (sen expandir $).
vars() { printf '$Blob = "%s"; $Otp = "%s"; $WF = "%s"\n' "$@"; }
{ vars "$blob" "$otp" "$wf"; cat <<'EOF'; } | T=200 win || { shot fallo-vincular; fail "non se pode vincular"; }
$ErrorActionPreference = 'Stop'
$exe = "$env:LOCALAPPDATA\Programs\syncply\syncply.exe"
$j = "$env:TEMP\cookies.txt"
$U = (& $exe open -print | Out-String).Trim(); $B = $U -replace '/login.*$', ''
$H = @('-H', "Origin: $B", '-H', 'Sec-Fetch-Site: same-origin')
curl.exe -sf -c $j -o NUL $U; if ($LASTEXITCODE) { throw "login $U" }
$r = curl.exe -s -b $j @H --data-urlencode "linktext=$Blob" "$B/pair/check" | Out-String
if ($r -notmatch 'Vas vincular.*pairsrv:8080.*alice') { throw "sen confirmación: $r" }
"  OK  confirmación: pairsrv:8080 como alice"
$r = curl.exe -s -b $j -o NUL -w '%{redirect_url}' @H --data-urlencode "linktext=$Blob" `
	--data-urlencode "otp=$Otp" --data-urlencode "device=portatil-ana-windows" "$B/pair"
if ($r -notmatch 'ok=') { throw "ao vincular: $r" }
"  OK  vinculado"
for ($i = 0; $i -lt 30; $i++) { if ((curl.exe -s -b $j "$B/" | Out-String) -match 'sftpgo-folder-comun') { break }; Start-Sleep 2 }
$dst = "$env:USERPROFILE\syncply\comun"
$r = curl.exe -s -b $j -o NUL -w '%{redirect_url}' @H --data-urlencode "id=sftpgo-folder-comun" `
	--data-urlencode "path=$dst" "$B/accept"
if ($r -notmatch 'ok=') { throw "ao aceptar: $r" }
"  OK  /comun aceptado en $dst"
for ($i = 0; $i -lt 45; $i++) { if (Get-ChildItem $dst -File -ErrorAction SilentlyContinue) { break }; Start-Sleep 2 }
$n = @(Get-ChildItem $dst -File).Count
if ($n -eq 0) { throw 'non chegou ningún ficheiro' }
"  OK  recibidos $n ficheiros en $dst"
Set-Content -Encoding UTF8 "$dst\$WF" "Ola desde Windows $(Get-Date -Format o)"
EOF

echo "### Ficheiro escrito en Windows → disco de SFTPGo"
f=$(find "$d" -name "$wf" | head -1)
i=0
while [ -z "$f" ]; do
	i=$((i + 1)); [ $i -lt 30 ] || fail "$wf non chegou ao servidor"; sleep 2
	f=$(find "$d" -name "$wf" | head -1)
done
ok "chegou a $f"

echo "### Reiniciar Windows"
echo 'shutdown.exe /r /t 5' | win >/dev/null || fail "ana non pode reiniciar"
# A seguinte orde queda na cola ata que o axente volva tras reiniciar.
sleep 30
T=900 win <<'EOF' || { shot fallo-reinicio; fail "o daemon non arranca ao iniciar sesión"; }
$ErrorActionPreference = 'Stop'
$exe = "$env:LOCALAPPDATA\Programs\syncply\syncply.exe"
$up = (Get-Date) - (Get-CimInstance Win32_OperatingSystem).LastBootUpTime
if ($up.TotalMinutes -gt 10) { throw "Windows non reiniciou (leva $up acendido)" }
for ($i = 0; $i -lt 60; $i++) { if (Get-Process syncthing -ErrorAction SilentlyContinue) { break }; Start-Sleep 1 }
if (-not (Get-Process syncthing -ErrorAction SilentlyContinue)) { throw 'sen syncthing.exe' }
"  OK  o daemon arranca só ao iniciar sesión"
EOF

echo "### Revogar este equipo (para que a proba se poida repetir: alice ten un límite de equipos)"
otp=$(make -s otp U=alice | cut -d' ' -f1)
{ vars "" "$otp" "$wf"; cat <<'EOF'; } | win || fail "non se pode revogar"
$ErrorActionPreference = 'Stop'
$d = "$env:LOCALAPPDATA\Programs\syncply"
$ID = (& "$d\syncthing.exe" device-id "--home=$env:LOCALAPPDATA\syncply\syncthing" | Out-String).Trim()
$U = (& "$d\syncply.exe" open -print | Out-String).Trim(); $B = $U -replace '/login.*$', ''
$j = "$env:TEMP\cookies.txt"
curl.exe -sf -c $j -o NUL $U
$r = curl.exe -s -b $j -o NUL -w '%{redirect_url}' -H "Origin: $B" -H 'Sec-Fetch-Site: same-origin' `
	--data-urlencode "id=$ID" --data-urlencode "otp=$Otp" --data-urlencode 'back=/devices' "$B/revoke"
if ($r -notmatch 'ok=') { throw "ao revogar: $r" }
"  OK  equipo revogado ($ID)"
EOF

echo "### Matar o daemon: Syncthing remata con el; o acceso directo volve arrancalo"
win <<'EOF' || fail "Syncthing sobrevive ao daemon"
$ErrorActionPreference = 'Stop'
Stop-Process -Name syncply -Force; Start-Sleep 3
$p = Get-Process syncthing -ErrorAction SilentlyContinue
if ($p) { throw "seguen $(@($p).Count) procesos syncthing.exe orfos" }
"  OK  sen procesos syncthing.exe orfos"
$exe = "$env:LOCALAPPDATA\Programs\syncply\syncply.exe"
$u = (& $exe open -print | Out-String).Trim()
if ($u -notmatch '^http://127\.0\.0\.1:\d+/login') { throw "open: $u" }
for ($i = 0; $i -lt 30; $i++) { if (Get-Process syncthing -ErrorAction SilentlyContinue) { break }; Start-Sleep 1 }
if (-not (Get-Process syncthing -ErrorAction SilentlyContinue)) { throw 'syncthing non volveu' }
"  OK  syncply open volve arrancar o daemon e Syncthing"
EOF

echo "### Desinstalar (o que executa «Aplicacións instaladas», sen preguntar)"
win <<'EOF' || fail "non se pode desinstalar"
$ErrorActionPreference = 'Stop'
$q = (Get-ItemProperty HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\syncply).QuietUninstallString
$out = cmd.exe /c "$q 2>&1"
if ($LASTEXITCODE) { throw "uninstall: $out" }
Start-Sleep 8
$d = "$env:LOCALAPPDATA\Programs\syncply"
if (Test-Path $d) { throw "segue ${d}: $(Get-ChildItem $d -Name)" }
if (Get-Process syncply, syncthing -ErrorAction SilentlyContinue) { throw 'seguen procesos en marcha' }
if ((Get-ItemProperty HKCU:\Software\Microsoft\Windows\CurrentVersion\Run).syncply) { throw 'segue o arranque automático' }
if (Test-Path HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\syncply) { throw 'segue en Aplicacións instaladas' }
if (Test-Path "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Syncply.lnk") { throw 'segue o acceso directo' }
if (-not (Get-ChildItem "$env:USERPROFILE\syncply\comun\desde-windows-*.txt")) { throw 'borráronse as carpetas sincronizadas' }
if (-not (Test-Path "$env:LOCALAPPDATA\syncply\syncthing")) { throw 'borrouse a configuración' }
"  OK  desinstalado; consérvanse configuración e carpetas"
EOF
echo
echo "Instalador de Windows: todo correcto."
