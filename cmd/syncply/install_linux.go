//go:build linux

package main

// Instalador de Linux: syncply instálase a si mesmo, só para o usuario e sen
// root. As rutas son as mesmas que usaba install.sh ata v0.4.1, así que
// instalar por riba dunha instalación anterior actualízaa:
//
//	~/.local/share/syncply/bin/          syncply, syncthing (o embebido), LICENZAS.txt
//	~/.config/syncply/                   estado
//	~/.config/systemd/user/syncply.service   servizo de usuario (sen systemd de
//	                                     usuario: ~/.config/autostart/syncply-daemon.desktop)
//	~/.local/share/applications/syncply.desktop  acceso directo (`syncply open`)
//
// Sen argumentos (dobre clic ou ./syncply no terminal) instala ou, se xa é o
// instalado, abre a configuración. O progreso vese no terminal e, se hai
// sesión gráfica, nunha páxina local no navegador (install_progress_linux.go).

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// syncthingMarker garda o SHA-256 do Syncthing embebido que se instalou
// xunto a syncply: se non coincide co que leva o binario, volve instalarse.
const syncthingMarker = ".syncthing.sha256"

type linuxInstall struct {
	bin, cfg, unit, autostart, shortcut, pidfile string
}

func installPaths() (linuxInstall, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return linuxInstall{}, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	conf, err := os.UserConfigDir()
	if err != nil {
		return linuxInstall{}, err
	}
	cfg := defaultStateDir()
	return linuxInstall{
		bin:       filepath.Join(data, "syncply", "bin"),
		cfg:       cfg,
		unit:      filepath.Join(conf, "systemd", "user", "syncply.service"),
		autostart: filepath.Join(conf, "autostart", "syncply-daemon.desktop"),
		shortcut:  filepath.Join(data, "applications", "syncply.desktop"),
		pidfile:   filepath.Join(cfg, "syncply.pid"),
	}, nil
}

func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// runningInstalled indica se este syncply é o instalado.
func runningInstalled() bool {
	exe, err := selfPath()
	p, err2 := installPaths()
	return err == nil && err2 == nil && filepath.Dir(exe) == filepath.Clean(p.bin)
}

func defaultAction() error {
	if runningInstalled() {
		return open(nil)
	}
	return install(nil)
}

func info(text string) { fmt.Println(text) }

// O servizo arráncao systemd ou o arranque da sesión.
func ensureDaemon(string) error { return nil }

// hasSystemd: systemd de usuario dispoñible e funcionando nesta sesión.
func hasSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func hasDisplay() bool { return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" }

func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	noOpen := fs.Bool("no-open", false, "sen navegador: o progreso só no terminal e sen abrir a configuración ao rematar")
	fs.Parse(args)

	p, err := installPaths()
	if err != nil {
		return err
	}
	pr := startProgress(!*noOpen && hasDisplay())
	err = doInstall(p, pr)
	pr.finish(err, func() (string, error) { return loginURL(p.cfg) })
	if err != nil {
		return err
	}
	info("Syncply " + version + " instalado.")
	if pr.browserSeen() {
		return nil
	}
	info("Abre «Syncply» desde o menú de aplicacións e pega o texto de vinculación\n" +
		"que atoparás na páxina do servidor, xunto ao código de vinculación.")
	if !*noOpen && hasDisplay() {
		return openUI(p.cfg, false)
	}
	return nil
}

func doInstall(p linuxInstall, pr *installProgress) error {
	if os.Geteuid() == 0 {
		return errors.New("executa o instalador co teu usuario, non como root")
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	exe := filepath.Join(p.bin, "syncply")

	pr.step("Parando a versión instalada, se a hai")
	if err := stopDaemonLinux(p); err != nil {
		return err
	}

	pr.step("Copiando os programas a " + p.bin)
	if err := os.MkdirAll(p.bin, 0o755); err != nil {
		return fmt.Errorf("non se puido crear o directorio de instalación: %w", err)
	}
	err = os.MkdirAll(p.cfg, 0o700)
	if err == nil {
		err = os.Chmod(p.cfg, 0o700)
	}
	if err != nil {
		return fmt.Errorf("non se puido preparar o directorio da configuración: %w", err)
	}
	if self != exe {
		if err := copyExec(self, exe); err != nil {
			return fmt.Errorf("copiando syncply: %w", err)
		}
	}
	if len(embeddedSyncthingGz) > 0 {
		if _, err := installEmbeddedSyncthing(p.bin); err != nil {
			return fmt.Errorf("instalando syncthing: %w", err)
		}
	} else if filepath.Dir(self) != p.bin {
		// Compilación de desenvolvemento: o syncthing que haxa ao seu lado.
		if err := copyExec(filepath.Join(filepath.Dir(self), "syncthing"), filepath.Join(p.bin, "syncthing")); err != nil {
			return fmt.Errorf("este syncply non leva Syncthing e non hai syncthing ao seu lado: %w", err)
		}
	}
	if len(embeddedLicenses) > 0 {
		if err := os.WriteFile(filepath.Join(p.bin, "LICENZAS.txt"), embeddedLicenses, 0o644); err != nil {
			return fmt.Errorf("copiando as licenzas: %w", err)
		}
	}
	// O cliente é xenérico: o servidor indícao o texto de vinculación. As
	// versións anteriores levaban syncply.json, que xa non se usa.
	os.Remove(filepath.Join(p.cfg, "syncply.json"))
	os.Remove(filepath.Join(p.cfg, "syncply.json.anterior"))

	pr.step("Creando o acceso directo «Syncply» no menú de aplicacións")
	if _, err := writeShortcut(exe, ""); err != nil {
		return fmt.Errorf("acceso directo: %w", err)
	}

	if hasSystemd() {
		pr.step("Configurando o servizo de usuario de systemd")
		unit := "[Unit]\nDescription=Syncply: sincronización co servidor de ficheiros\nAfter=network-online.target\n\n" +
			"[Service]\nExecStart=" + systemdQuote(exe) + " daemon\nRestart=on-failure\nRestartSec=5\n" +
			"# syncply detén o seu Syncthing limpamente ao recibir SIGTERM.\nKillMode=mixed\nTimeoutStopSec=30\n\n" +
			"[Install]\nWantedBy=default.target\n"
		if err := os.MkdirAll(filepath.Dir(p.unit), 0o755); err == nil {
			err = os.WriteFile(p.unit, []byte(unit), 0o644)
		}
		if err != nil {
			return fmt.Errorf("servizo de systemd: %w", err)
		}
		os.Remove(p.autostart)
		pr.step("Arrancando o servizo")
		pr.setLogs("journalctl --user -u syncply")
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
		if err := systemctl("enable", "--now", "syncply.service"); err != nil {
			return fmt.Errorf("non se puido arrancar o servizo (journalctl --user -u syncply): %w", err)
		}
	} else {
		pr.step("Configurando o arranque ao iniciar sesión")
		desktop := "[Desktop Entry]\nType=Application\nName=Syncply (servizo)\nExec=" + desktopQuote(exe) +
			" daemon\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n"
		if err := os.MkdirAll(filepath.Dir(p.autostart), 0o755); err == nil {
			err = os.WriteFile(p.autostart, []byte(desktop), 0o644)
		}
		if err != nil {
			return fmt.Errorf("arranque ao iniciar sesión: %w", err)
		}
		pr.step("Arrancando o servizo")
		pr.setLogs(filepath.Join(p.cfg, "syncply.log"))
		if err := startDetachedLinux(exe, filepath.Join(p.cfg, "syncply.log"), p.pidfile); err != nil {
			return fmt.Errorf("arrancando o servizo: %w", err)
		}
	}

	pr.step("Agardando a que o servizo responda")
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := loginURL(p.cfg)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("o servizo non responde: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func uninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	fs.Parse(args)
	p, err := installPaths()
	if err != nil {
		return err
	}
	if err := stopDaemonLinux(p); err != nil {
		return err
	}
	systemd := hasSystemd()
	if systemd {
		if _, err := os.Stat(p.unit); err == nil {
			systemctl("disable", "syncply.service")
		}
	}
	for _, f := range []string{p.unit, p.autostart, p.shortcut} {
		os.Remove(f)
	}
	if systemd {
		systemctl("daemon-reload")
	}
	for _, f := range []string{"syncply", "syncthing", "syncply.old", "syncthing.old", "LICENZAS.txt", syncthingMarker} {
		os.Remove(filepath.Join(p.bin, f))
	}
	os.Remove(p.bin)
	os.Remove(filepath.Dir(p.bin))
	info("Syncply desinstalado.")
	info("Consérvanse a configuración (" + p.cfg + ") e as carpetas sincronizadas.")
	info("Para borrar tamén a configuración e a identidade deste equipo: rm -r \"" + p.cfg + "\"")
	return nil
}

// stopDaemonLinux pecha o daemon en marcha: o servizo de systemd, o
// arrancado sen systemd (pidfile) e, se aínda responde (p. ex. arrancado
// polo autostart da sesión), pola canle de control.
func stopDaemonLinux(p linuxInstall) error {
	if _, err := os.Stat(p.unit); err == nil && hasSystemd() {
		systemctl("stop", "syncply.service")
	}
	if b, err := os.ReadFile(p.pidfile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
			if syscall.Kill(pid, syscall.SIGTERM) == nil {
				for i := 0; i < 60 && syscall.Kill(pid, 0) == nil; i++ {
					time.Sleep(500 * time.Millisecond)
				}
			}
		}
		os.Remove(p.pidfile)
	}
	if _, err := loginURL(p.cfg); err != nil {
		return nil
	}
	if err := controlCall(p.cfg, "shutdown", nil); err != nil {
		return fmt.Errorf("non se pode pechar o servizo syncply en marcha: %w", err)
	}
	deadline := time.Now().Add(40 * time.Second)
	for {
		if _, err := loginURL(p.cfg); err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("o servizo syncply en marcha non se pecha; reinicia a sesión e volve tentalo")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// startDetachedLinux arranca o daemon nunha sesión propia (segue en marcha
// ao pechar o instalador ou o terminal), co rexistro en logPath.
func startDetachedLinux(exe, logPath, pidfile string) error {
	lf, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer lf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// prepareSyncthing (ao arrancar o daemon instalado): se o Syncthing
// embebido neste syncply non é o instalado ao seu lado, instálao. Así unha
// actualización só ten que substituír syncply.
func prepareSyncthing() {
	if len(embeddedSyncthingGz) == 0 || !runningInstalled() {
		return
	}
	exe, err := selfPath()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	if changed, err := installEmbeddedSyncthing(dir); err != nil {
		log.Printf("syncply: non se puido instalar o Syncthing embebido: %v", err)
	} else if changed {
		log.Printf("syncply: instalado o Syncthing embebido en %s", dir)
	}
}

// installEmbeddedSyncthing escribe dir/syncthing a partir do embebido,
// salvo que xa estea instalado (marca co seu SHA-256). Indica se o escribiu.
func installEmbeddedSyncthing(dir string) (bool, error) {
	h := sha256.Sum256(embeddedSyncthingGz)
	sum := hex.EncodeToString(h[:])
	dst := filepath.Join(dir, "syncthing")
	marker := filepath.Join(dir, syncthingMarker)
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == sum {
		if fi, err := os.Stat(dst); err == nil && fi.Mode().IsRegular() {
			return false, nil
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(embeddedSyncthingGz))
	if err != nil {
		return false, err
	}
	if err := writeExec(dst, zr); err != nil {
		return false, err
	}
	return true, os.WriteFile(marker, []byte(sum+"\n"), 0o644)
}

func copyExec(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeExec(dst, f)
}

// writeExec escribe un executable nun temporal e renomea (seguro aínda que
// o binario anterior estea en execución).
func writeExec(path string, r io.Reader) error {
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o755)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// systemdQuote: ruta para ExecStart (comiñas dobres; escapa \ e ").
func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// desktopQuote: ruta para Exec dun .desktop.
func desktopQuote(s string) string { return fmt.Sprintf("%q", s) }
