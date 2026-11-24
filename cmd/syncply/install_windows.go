package main

// Instalador de Windows: syncply.exe instálase a si mesmo, só para o usuario e
// sen permisos de administrador.
//
//	%LOCALAPPDATA%\Programs\syncply\   syncply.exe, syncthing.exe, LICENZAS.txt
//	%LOCALAPPDATA%\syncply\            estado (o mesmo que en Linux ~/.config/syncply)
//	HKCU\...\Run\syncply               arranque do daemon ao iniciar sesión
//	HKCU\...\Uninstall\syncply         entrada en «Aplicacións instaladas»
//	Menú Inicio\Programas\Syncply.lnk  acceso directo (`syncply open`)

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\syncply`
	valueName    = "syncply"
	idYes        = 6 // MessageBox: botón «Si»
)

func installDir() (string, error) {
	d := os.Getenv("LOCALAPPDATA")
	if d == "" {
		return "", errors.New("non se atopa %LOCALAPPDATA%")
	}
	return filepath.Join(d, "Programs", "syncply"), nil
}

func shortcutPath() (string, error) {
	d, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_CREATE)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "Syncply.lnk"), nil
}

func samePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// runningInstalled indica se este syncply.exe é o instalado.
func runningInstalled() bool {
	exe, err := os.Executable()
	dir, err2 := installDir()
	return err == nil && err2 == nil && samePath(filepath.Dir(exe), dir)
}

// defaultAction: o que pasa co dobre clic. Desde a descarga, instala; xa
// instalado, abre a configuración.
func defaultAction() error {
	if runningInstalled() {
		return open(nil)
	}
	return install(nil)
}

func install(args []string) (err error) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	noOpen := fs.Bool("no-open", false, "non abrir a configuración ao rematar")
	quiet := fs.Bool("quiet", false, "sen mensaxes nin navegador")
	upd := fs.Bool("update", false, "actualización automática (o daemon executa a versión descargada): sen mensaxes; os erros van ao rexistro")
	fs.Parse(args)
	if *upd {
		*quiet = true
		silentErrors = true
		if f, lerr := openLog(defaultLogFile(defaultStateDir())); lerr == nil {
			defer f.Close()
			log.SetOutput(f)
			os.Stderr = f
		}
		log.Printf("syncply %s: actualizando", version)
		defer func() {
			if err == nil {
				return
			}
			// Que o equipo non quede sen sincronizar: volver arrancar o
			// daemon instalado (o novo, se xa se copiou; senón, o anterior).
			if dst, derr := installDir(); derr == nil {
				exe := filepath.Join(dst, "syncply.exe")
				if !daemonRunning(defaultStateDir()) {
					startDetached(exe, "daemon")
				}
			}
		}()
	}

	dst, err := installDir()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	state := defaultStateDir()
	if err := stopDaemon(state); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	exe := filepath.Join(dst, "syncply.exe")

	// Syncthing: o que leva dentro este syncply.exe ou, se non, o que haxa ao
	// seu lado (compilación de desenvolvemento).
	switch {
	case len(embeddedSyncthingGz) > 0:
		zr, err := gzip.NewReader(bytes.NewReader(embeddedSyncthingGz))
		if err != nil {
			return err
		}
		err = writeFileAtomic(filepath.Join(dst, "syncthing.exe"), zr)
		if err != nil {
			return fmt.Errorf("instalando syncthing.exe: %w", err)
		}
	case !samePath(filepath.Dir(self), dst):
		if err := copyFile(filepath.Join(filepath.Dir(self), "syncthing.exe"), filepath.Join(dst, "syncthing.exe")); err != nil {
			return fmt.Errorf("este syncply.exe non leva Syncthing e non hai syncthing.exe ao seu lado: %w", err)
		}
	}
	if len(embeddedLicenses) > 0 {
		if err := writeFileAtomic(filepath.Join(dst, "LICENZAS.txt"), bytes.NewReader(embeddedLicenses)); err != nil {
			return err
		}
	}
	if !samePath(self, exe) {
		if err := copyFile(self, exe); err != nil {
			return fmt.Errorf("copiando syncply.exe: %w", err)
		}
	}

	if err := setString(registry.CURRENT_USER, runKey, valueName, quoteArg(exe)+" daemon"); err != nil {
		return fmt.Errorf("arranque automático: %w", err)
	}
	if err := writeUninstallEntry(dst, exe); err != nil {
		return fmt.Errorf("entrada en Aplicacións instaladas: %w", err)
	}
	lnk, err := shortcutPath()
	if err == nil {
		err = createShortcut(lnk, exe, "open", "Configurar a sincronización co servidor de ficheiros")
	}
	if err != nil {
		return fmt.Errorf("acceso directo no menú Inicio: %w", err)
	}

	if err := startDetached(exe, "daemon"); err != nil {
		return fmt.Errorf("arrancando o servizo: %w", err)
	}
	if err := waitDaemon(state, 30*time.Second); err != nil {
		return fmt.Errorf("o servizo non responde; mira %s: %w", defaultLogFile(state), err)
	}
	if *quiet {
		return nil
	}
	// Primeiro a mensaxe e despois o navegador: ao revés, o navegador
	// quedaba diante e a mensaxe agochada (visto en Windows 11).
	next := "Atoparás «Syncply» no menú Inicio."
	if !*noOpen {
		next = "Ao aceptar, abrirase a configuración no navegador (tamén está «Syncply» no menú Inicio)."
	}
	info("Syncply " + version + " instalado.\n\n" + next + " Para vincular este equipo, pega o texto de " +
		"vinculación que atoparás na páxina do servidor, xunto ao código de vinculación.")
	if !*noOpen {
		return openUI(state, false)
	}
	return nil
}

func uninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	quiet := fs.Bool("quiet", false, "sen preguntar nin mensaxes")
	fs.Parse(args)

	if !*quiet && !hasConsole {
		if messageBox("Syncply", "Desinstalar Syncply?\n\nConsérvanse a configuración deste equipo e as carpetas sincronizadas.",
			windows.MB_YESNO|windows.MB_ICONQUESTION) != idYes {
			return nil
		}
	}
	dst, err := installDir()
	if err != nil {
		return err
	}
	state := defaultStateDir()
	if err := stopDaemon(state); err != nil {
		return err
	}
	deleteValue(registry.CURRENT_USER, runKey, valueName)
	registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	if lnk, err := shortcutPath(); err == nil {
		os.Remove(lnk)
	}
	for _, f := range []string{"syncthing.exe", "LICENZAS.txt"} {
		err := retry(func() error { return os.Remove(filepath.Join(dst, f)) })
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	exe := filepath.Join(dst, "syncply.exe")
	if runningInstalled() {
		// Windows non deixa borrar un .exe en execución: bórrao un cmd
		// cando este proceso remate.
		if err := deleteAfterExit(exe, dst); err != nil {
			return err
		}
	} else {
		os.Remove(exe)
		os.Remove(dst)
	}
	if !*quiet {
		info("Syncply desinstalado.\n\nConsérvanse a configuración e a identidade deste equipo (" + state +
			") e as carpetas sincronizadas. Para borrar tamén a configuración, borra ese cartafol.")
	}
	return nil
}

// stopDaemon pecha o daemon en marcha (se o hai) e agarda a que remate,
// xunto co seu Syncthing, para poder substituír os binarios.
func stopDaemon(dir string) error {
	if !daemonRunning(dir) {
		return nil
	}
	if err := controlCall(dir, "shutdown", nil); err != nil {
		return fmt.Errorf("non se pode pechar o servizo syncply en marcha: %w", err)
	}
	deadline := time.Now().Add(40 * time.Second)
	for daemonRunning(dir) {
		if time.Now().After(deadline) {
			return errors.New("o servizo syncply en marcha non se pecha; reinicia a sesión e volve tentalo")
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// ensureDaemon arranca o daemon se non está en marcha (o acceso directo
// tamén serve para volver arrancalo se se pechou).
func ensureDaemon(dir string) error {
	if daemonRunning(dir) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"daemon"}
	if !samePath(dir, defaultStateDir()) {
		args = append(args, "-dir", dir)
	}
	if err := startDetached(exe, args...); err != nil {
		return err
	}
	return waitDaemon(dir, 30*time.Second)
}

func waitDaemon(dir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := loginURL(dir)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func writeUninstallEntry(dir, exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	var size uint32
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				size += uint32(fi.Size() / 1024)
			}
		}
		return nil
	})
	for name, v := range map[string]string{
		"DisplayName":          "Syncply",
		"DisplayVersion":       version,
		"Publisher":            "Syncply",
		"DisplayIcon":          exe,
		"InstallLocation":      dir,
		"UninstallString":      quoteArg(exe) + " uninstall",
		"QuietUninstallString": quoteArg(exe) + " uninstall -quiet",
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	for name, v := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": size} {
		if err := k.SetDWordValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

func setString(root registry.Key, path, name, value string) error {
	k, _, err := registry.CreateKey(root, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func deleteValue(root registry.Key, path, name string) {
	k, err := registry.OpenKey(root, path, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.DeleteValue(name)
}

// createShortcut crea un .lnk co obxecto COM WScript.Shell desde
// PowerShell (evita depender de COM en Go). As rutas van por variables de
// contorno, non dentro do script, para non ter que escapalas.
func createShortcut(lnk, target, args, desc string) error {
	const script = `$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:SYNCPLY_LNK); ` +
		`$s.TargetPath = $env:SYNCPLY_TARGET; $s.Arguments = $env:SYNCPLY_ARGS; ` +
		`$s.WorkingDirectory = Split-Path $env:SYNCPLY_TARGET; $s.Description = $env:SYNCPLY_DESC; ` +
		`$s.IconLocation = $env:SYNCPLY_TARGET + ',0'; $s.Save()`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "SYNCPLY_LNK="+lnk, "SYNCPLY_TARGET="+target, "SYNCPLY_ARGS="+args, "SYNCPLY_DESC="+desc)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deleteAfterExit borra o .exe e o seu directorio uns segundos despois de
// que remate este proceso.
func deleteAfterExit(exe, dir string) error {
	if strings.ContainsAny(exe+dir, `%"^`) {
		return fmt.Errorf("borra a man %s", dir)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
		// Liña de ordes literal: cmd non segue as regras de comiñas de Go.
		CmdLine: fmt.Sprintf(`cmd.exe /d /c "ping -n 4 127.0.0.1 >nul & del /f /q "%s" & rmdir "%s""`, exe, dir),
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// retry: un proceso que acaba de rematar pode ter aínda o seu .exe bloqueado
// uns instantes.
func retry(f func() error) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = f(); err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

func quoteArg(s string) string { return `"` + s + `"` }

func copyFile(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeFileAtomic(dst, f)
}

// writeFileAtomic escribe nun temporal e renomea: un fallo a medias non
// deixa un binario roto.
func writeFileAtomic(path string, r io.Reader) error {
	tmp := path + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := retry(func() error { return os.Rename(tmp, path) }); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
