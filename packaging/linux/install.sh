#!/bin/sh
# Instalador de syncply para Linux. Instala só para o teu usuario (sen root):
#
#   ./install.sh               instala ou actualiza e abre a configuración
#   ./install.sh --no-open     instala sen abrir o navegador
#   ./install.sh --uninstall   desinstala (conserva configuración e carpetas)
set -eu

here=$(cd "$(dirname "$0")" && pwd)
data_home=${XDG_DATA_HOME:-$HOME/.local/share}
config_home=${XDG_CONFIG_HOME:-$HOME/.config}
bindir=$data_home/syncply/bin
cfgdir=$config_home/syncply
unit=$config_home/systemd/user/syncply.service
autostart=$config_home/autostart/syncply-daemon.desktop
shortcut=$data_home/applications/syncply.desktop
pidfile=$cfgdir/syncply.pid

msg() { printf '%s\n' "$*"; }
die() { printf 'syncply: %s\n' "$*" >&2; exit 1; }

# systemd de usuario dispoñible e funcionando nesta sesión?
has_systemd() {
	command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1
}

stop_daemon() {
	if has_systemd && [ -f "$unit" ]; then
		systemctl --user stop syncply.service 2>/dev/null || true
	fi
	if [ -f "$pidfile" ]; then
		pid=$(cat "$pidfile")
		if kill -0 "$pid" 2>/dev/null; then
			kill "$pid" 2>/dev/null || true
			i=0
			while kill -0 "$pid" 2>/dev/null && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
		fi
		rm -f "$pidfile"
	fi
}

uninstall() {
	stop_daemon
	if has_systemd && [ -f "$unit" ]; then
		systemctl --user disable syncply.service 2>/dev/null || true
	fi
	rm -f "$unit" "$autostart" "$shortcut"
	has_systemd && systemctl --user daemon-reload 2>/dev/null || true
	rm -f "$bindir/syncply" "$bindir/syncthing"
	rmdir "$bindir" "$data_home/syncply" 2>/dev/null || true
	msg "Syncply desinstalado."
	msg "Consérvanse a configuración ($cfgdir) e as carpetas sincronizadas."
	msg "Para borrar tamén a configuración e a identidade deste equipo: rm -r \"$cfgdir\""
}

open_ui=1
case "${1:-}" in
	--uninstall) uninstall; exit 0 ;;
	--no-open) open_ui=0 ;;
	"") ;;
	*) die "uso: $0 [--no-open | --uninstall]" ;;
esac

[ "$(id -u)" -ne 0 ] || die "executa o instalador co teu usuario, non como root."
for f in syncply syncthing; do
	[ -f "$here/$f" ] || die "falta $f no paquete."
done

stop_daemon
mkdir -p "$bindir" "$cfgdir"
chmod 700 "$cfgdir"
cp "$here/syncply" "$here/syncthing" "$bindir/"
chmod 755 "$bindir/syncply" "$bindir/syncthing"

# O cliente é xenérico: o servidor indícao o texto de vinculación. As
# versións anteriores levaban syncply.json, que xa non se usa.
rm -f "$cfgdir/syncply.json" "$cfgdir/syncply.json.anterior"

"$bindir/syncply" shortcut >/dev/null

if has_systemd; then
	mkdir -p "$(dirname "$unit")"
	cat >"$unit" <<EOF
[Unit]
Description=Syncply: sincronización co servidor de ficheiros
After=network-online.target

[Service]
ExecStart=$bindir/syncply daemon
Restart=on-failure
RestartSec=5
# syncply detén o seu Syncthing limpamente ao recibir SIGTERM.
KillMode=mixed
TimeoutStopSec=30

[Install]
WantedBy=default.target
EOF
	systemctl --user daemon-reload
	systemctl --user enable --now syncply.service >/dev/null 2>&1 || die "non se puido arrancar o servizo (journalctl --user -u syncply)"
	rm -f "$autostart"
	logs="journalctl --user -u syncply"
else
	# Sen systemd de usuario: arranque automático ao iniciar sesión (XDG).
	mkdir -p "$(dirname "$autostart")"
	cat >"$autostart" <<EOF
[Desktop Entry]
Type=Application
Name=Syncply (servizo)
Exec=$bindir/syncply daemon
NoDisplay=true
X-GNOME-Autostart-enabled=true
EOF
	nohup "$bindir/syncply" daemon >"$cfgdir/syncply.log" 2>&1 &
	echo $! >"$pidfile"
	logs="$cfgdir/syncply.log"
fi

# Agardar a que o servizo responda.
i=0
until "$bindir/syncply" open -print >/dev/null 2>&1; do
	i=$((i + 1))
	[ $i -lt 30 ] || die "o servizo non responde; mira $logs"
	sleep 1
done

msg "Syncply $("$bindir/syncply" version | cut -d' ' -f2) instalado."
msg "Abre «Syncply» desde o menú de aplicacións e pega o texto de vinculación"
msg "que atoparás na páxina do servidor, xunto ao código de vinculación."
if [ $open_ui -eq 1 ] && [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]; then
	"$bindir/syncply" open || true
fi
