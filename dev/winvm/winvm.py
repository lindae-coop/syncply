#!/usr/bin/env python3
"""VM de Windows para probar o cliente (só desenvolvemento).

Execútase dentro do contedor (dev/winvm/Containerfile), co directorio de
traballo en /work (.winvm no repositorio):

  winvm.py install   instala Windows desde /iso/windows.iso en /work/base.qcow2
                     (desatendido: autounattend.xml + agent.ps1 nun CD)
  winvm.py run       arranca unha copia nova (test.qcow2 sobre base.qcow2)
  winvm.py resume    arranca test.qcow2 tal como quedou
  winvm.py exec      (podman exec) executa en Windows o script PowerShell da
                     entrada estándar, como o usuario con sesión iniciada
  winvm.py wait      agarda a que o axente de Windows responda
  winvm.py shot F    captura da pantalla en F (PNG)
  winvm.py key K...  preme teclas (nomes de QMP: ret, tab, esc, alt-f4...)
  winvm.py click X Y clic nas coordenadas X Y dunha captura (1280x800)
  winvm.py reboot | powerdown

O axente (agent.ps1) pregunta por HTTP a 10.0.2.2:8000 (este contedor visto
desde a rede de usuario de QEMU); /files/<nome> serve ./dist e
/files/agent.ps1 a versión actual do axente.
"""
import http.server
import json
import os
import shutil
import socket
import subprocess
import sys
import threading
import time
import uuid

WORK = "/work"
RUN = "/run/winvm"
QMP = os.path.join(RUN, "qmp.sock")
QUEUE = os.path.join(RUN, "queue")
RESULTS = os.path.join(RUN, "results")
LASTPOLL = os.path.join(RUN, "lastpoll")
DIST = "/dist"
OVMF_CODE = "/usr/share/OVMF/OVMF_CODE_4M.fd"
OVMF_VARS = "/usr/share/OVMF/OVMF_VARS_4M.fd"


# --- servidor do axente -------------------------------------------------

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def send(self, code, body=b"", headers=None):
        self.send_response(code)
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.startswith("/cmd"):
            with open(LASTPOLL, "w") as f:
                f.write(str(time.time()))
            jobs = sorted(os.listdir(QUEUE))
            if not jobs:
                return self.send(204)
            job = jobs[0]
            p = os.path.join(QUEUE, job)
            body = open(p, "rb").read()
            os.remove(p)
            timeout = job.rsplit("-", 1)[-1]
            return self.send(200, body, {"X-Id": job, "X-Timeout": timeout, "Content-Type": "application/octet-stream"})
        if self.path.startswith("/files/"):
            name = os.path.basename(self.path[len("/files/"):])
            p = os.path.join("/winvm" if name == "agent.ps1" else DIST, name)
            if not os.path.isfile(p):
                return self.send(404)
            return self.send(200, open(p, "rb").read(), {"Content-Type": "application/octet-stream"})
        self.send(404)

    def do_POST(self):
        if self.path.startswith("/result/"):
            job, _, q = self.path[len("/result/"):].partition("?")
            code = q.partition("code=")[2] or "1"
            n = int(self.headers.get("Content-Length", "0"))
            data = self.rfile.read(n)
            tmp = os.path.join(RESULTS, job + ".tmp")
            with open(tmp, "wb") as f:
                f.write(code.encode() + b"\n" + data)
            os.rename(tmp, os.path.join(RESULTS, job))
            return self.send(200)
        self.send(404)


def serve():
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 8000), Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()


# --- QMP ------------------------------------------------------------------

def qmp(cmd, **args):
    s = socket.socket(socket.AF_UNIX)
    s.connect(QMP)
    f = s.makefile("rw")
    f.readline()
    for c in ({"execute": "qmp_capabilities"}, {"execute": cmd, "arguments": args}):
        f.write(json.dumps(c) + "\n")
        f.flush()
        while True:
            r = json.loads(f.readline())
            if "return" in r or "error" in r:
                break
    s.close()
    if "error" in r:
        raise SystemExit(f"QMP {cmd}: {r['error']}")
    return r["return"]


def keys(*names):
    for k in names:
        qmp("send-key", keys=[{"type": "qcode", "data": p} for p in k.split("-")])
        time.sleep(0.15)


def click(x, y, w=1280, h=800):
    ev = lambda axis, v: {"type": "abs", "data": {"axis": axis, "value": v}}
    btn = lambda down: {"type": "btn", "data": {"button": "left", "down": down}}
    qmp("input-send-event", events=[ev("x", int(x * 32767 / w)), ev("y", int(y * 32767 / h))])
    time.sleep(0.1)
    qmp("input-send-event", events=[btn(True)])
    time.sleep(0.1)
    qmp("input-send-event", events=[btn(False)])


# --- VM -------------------------------------------------------------------

def config_iso():
    d = os.path.join(RUN, "cfg")
    os.makedirs(d, exist_ok=True)
    for f in ("autounattend.xml", "agent.ps1"):
        shutil.copy(os.path.join("/winvm", f), d)
    iso = os.path.join(RUN, "config.iso")
    subprocess.run(["genisoimage", "-quiet", "-J", "-r", "-V", "WINVM", "-o", iso, d], check=True)
    return iso


def qemu(disk, vars_fd, cdroms):
    cmd = [
        "qemu-system-x86_64", "-name", "winvm",
        "-machine", "q35,accel=kvm", "-cpu", "host", "-smp", os.environ.get("WINVM_CPUS", "2"),
        "-m", os.environ.get("WINVM_MEM", "4096"),
        "-drive", f"if=pflash,format=raw,readonly=on,file={OVMF_CODE}",
        "-drive", f"if=pflash,format=raw,file={vars_fd}",
        "-drive", f"file={disk},if=none,id=d0,format=qcow2,discard=unmap",
        "-device", "ide-hd,drive=d0,bus=ide.0",
        "-nic", "user,model=e1000e,domainname=dns.podman,dnssearch=dns.podman",
        "-usb", "-device", "usb-tablet",
        "-vga", "std", "-display", "none",
        "-vnc", "0.0.0.0:0",
        "-qmp", f"unix:{QMP},server,nowait",
        "-rtc", "base=localtime",
    ]
    for i, iso in enumerate(cdroms):
        cmd += ["-drive", f"file={iso},media=cdrom,if=none,id=cd{i},readonly=on",
                "-device", f"ide-cd,drive=cd{i},bus=ide.{i + 1}"]
    return subprocess.Popen(cmd)


def press_boot_from_cd(p):
    # «Press any key to boot from CD or DVD...»: só uns segundos tras arrancar.
    for _ in range(60):
        if p.poll() is not None:
            return
        try:
            keys("ret")
        except (OSError, SystemExit):
            pass
        time.sleep(0.5)


def start(mode):
    for d in (QUEUE, RESULTS):
        os.makedirs(d, exist_ok=True)
    base = os.path.join(WORK, "base.qcow2")
    base_vars = os.path.join(WORK, "base-vars.fd")
    serve()
    if mode == "install":
        if not os.path.exists("/iso/windows.iso"):
            raise SystemExit("falta a ISO de Windows en /iso/windows.iso")
        for f in (base, base_vars):
            if os.path.exists(f):
                os.remove(f)
        subprocess.run(["qemu-img", "create", "-q", "-f", "qcow2", base, "64G"], check=True)
        shutil.copy(OVMF_VARS, base_vars)
        p = qemu(base, base_vars, ["/iso/windows.iso", config_iso()])
        press_boot_from_cd(p)
        # Rematou cando o axente pregunta (sesión de ana iniciada): apagar.
        while p.poll() is None:
            if os.path.exists(LASTPOLL):
                print("winvm: Windows instalado; apagando", flush=True)
                time.sleep(20)
                qmp("system_powerdown")
                break
            time.sleep(5)
        p.wait()
        open(os.path.join(WORK, "base.ok"), "w").close()
        return
    if not os.path.exists(os.path.join(WORK, "base.ok")):
        raise SystemExit("Windows sen instalar (make winvm-install)")
    test = os.path.join(WORK, "test.qcow2")
    test_vars = os.path.join(WORK, "test-vars.fd")
    if mode == "run" or not os.path.exists(test):
        if os.path.exists(test):
            os.remove(test)
        subprocess.run(["qemu-img", "create", "-q", "-f", "qcow2", "-F", "qcow2", "-b", base, test], check=True)
        shutil.copy(base_vars, test_vars)
    p = qemu(test, test_vars, [])
    sys.exit(p.wait())


# --- ordes desde fóra (podman exec) -------------------------------------------

def run_script(script, timeout):
    # O nome leva o tempo máximo: o axente mata a orde se se pasa.
    job = f"{time.time():.6f}-{uuid.uuid4().hex[:8]}-{timeout}"
    tmp = os.path.join(RUN, job + ".tmp")
    with open(tmp, "wb") as f:
        # BOM: PowerShell 5.1 le os .ps1 sen BOM como ANSI.
        f.write(b"\xef\xbb\xbf[Console]::OutputEncoding = [Text.Encoding]::UTF8\n" + script.encode())
    os.rename(tmp, os.path.join(QUEUE, job))
    res = os.path.join(RESULTS, job)
    deadline = time.time() + timeout
    deadline += 30  # marxe para que o axente mate a orde e responda
    while not os.path.exists(res):
        if time.time() > deadline:
            raise SystemExit(f"winvm: sen resposta do axente en {timeout} s")
        time.sleep(0.3)
    code, _, out = open(res, "rb").read().partition(b"\n")
    os.remove(res)
    sys.stdout.write(out.decode("utf-8", "replace"))
    try:
        return int(code)
    except ValueError:
        return 1


def wait(timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if time.time() - float(open(LASTPOLL).read()) < 10:
                return 0
        except (OSError, ValueError):
            pass
        time.sleep(2)
    raise SystemExit(f"winvm: o axente non responde en {timeout} s")


def main():
    a = sys.argv[1:] or ["run"]
    cmd, rest = a[0], a[1:]
    if cmd in ("install", "run", "resume"):
        start(cmd)
    elif cmd == "exec":
        timeout = int(os.environ.get("WINVM_TIMEOUT", "300"))
        sys.exit(run_script(sys.stdin.read(), timeout))
    elif cmd == "wait":
        sys.exit(wait(int(rest[0]) if rest else 600))
    elif cmd == "shot":
        qmp("screendump", filename=rest[0], format="png")
    elif cmd == "key":
        keys(*rest)
    elif cmd == "click":
        click(int(rest[0]), int(rest[1]))
    elif cmd == "reboot":
        if os.path.exists(LASTPOLL):
            os.remove(LASTPOLL)
        qmp("system_reset")
    elif cmd == "powerdown":
        qmp("system_powerdown")
    else:
        raise SystemExit(__doc__)


if __name__ == "__main__":
    main()
