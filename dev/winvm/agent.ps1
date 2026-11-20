# Axente de probas da VM de Windows (só desenvolvemento). Arranca en cada
# inicio de sesión, na sesión do usuario (como un programa normal: sen
# administrador), e pregunta a winvm.py (10.0.2.2:8000, o contedor) se hai
# algo que executar. Cada script execútase nun PowerShell novo; a saída e o
# código de saída volven a winvm.py.
$ErrorActionPreference = 'Continue'
$srv = 'http://10.0.2.2:8000'
$dir = Join-Path $env:TEMP 'winvm'
New-Item -ItemType Directory -Force $dir | Out-Null
while ($true) {
    try {
        $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 30 "$srv/cmd?user=$env:USERNAME"
        if ($r.StatusCode -ne 200) { Start-Sleep -Seconds 1; continue }
        $id = $r.Headers['X-Id']
        $ps1 = Join-Path $dir "$id.ps1"
        $out = Join-Path $dir "$id.out"
        [IO.File]::WriteAllBytes($ps1, $r.RawContentStream.ToArray())
        # Sen -Wait: en PowerShell 5.1 agarda tamén polos fillos (o daemon
        # que deixa en marcha o instalador).
        $p = Start-Process -FilePath powershell.exe -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput $out -RedirectStandardError "$out.err" `
            -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $ps1
        $null = $p.Handle  # sen isto, ExitCode pode quedar baleiro
        $limit = [int]$r.Headers['X-Timeout']; if ($limit -le 0) { $limit = 300 }
        $text = ''
        if (-not $p.WaitForExit($limit * 1000)) {
            taskkill.exe /T /F /PID $p.Id | Out-Null
            $text = "winvm: esgotado o tempo ($limit s); árbore de procesos matada`n"
        }
        foreach ($f in $out, "$out.err") {
            if (Test-Path $f) { $text += [IO.File]::ReadAllText($f) }
        }
        Invoke-WebRequest -UseBasicParsing -Method Post -TimeoutSec 30 `
            -Uri "$srv/result/$id`?code=$(if ($p.ExitCode -ne $null) { $p.ExitCode } else { 124 })" `
            -ContentType 'text/plain; charset=utf-8' `
            -Body ([Text.Encoding]::UTF8.GetBytes($text)) | Out-Null
        Remove-Item -Force -ErrorAction SilentlyContinue $ps1, $out, "$out.err"
    } catch {
        Start-Sleep -Seconds 2
    }
}
