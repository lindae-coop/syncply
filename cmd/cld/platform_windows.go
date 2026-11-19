package main

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// En Windows cld compílase como aplicación gráfica (-H windowsgui) para que
// o daemon e o acceso directo non abran unha xanela de consola. Se se
// executa desde unha consola, enganchámonos a ela para mostrar a saída.
var hasConsole bool

func init() {
	h, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err == nil && h != 0 && h != windows.InvalidHandle {
		hasConsole = true // saída redirixida (ficheiro, tubería)
		return
	}
	attach := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	const attachParentProcess = ^uintptr(0)
	if r, _, _ := attach.Call(attachParentProcess); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
		log.SetOutput(f)
		hasConsole = true
	}
}

// reportError mostra o erro nunha caixa de diálogo se non hai consola
// (acceso directo, dobre clic).
func reportError(err error) {
	if !hasConsole {
		messageBox("cld", "Erro: "+err.Error(), windows.MB_OK|windows.MB_ICONERROR)
	}
}

func messageBox(title, text string, flags uint32) int32 {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(title)
	r, _ := windows.MessageBox(0, t, c, flags|windows.MB_SETFOREGROUND)
	return r
}

func info(text string) {
	if hasConsole {
		os.Stdout.WriteString(text + "\n")
		return
	}
	messageBox("cld", text, windows.MB_OK|windows.MB_ICONINFORMATION)
}

// defaultStateDir: %LOCALAPPDATA%\cld. Non AppData\Roaming: a identidade do
// equipo e a base de datos de Syncthing non deben viaxar co perfil.
func defaultStateDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "cld")
	}
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "cld")
}

// defaultLogFile: sen consola, o daemon escribe en <dir>\cld.log.
func defaultLogFile(dir string) string { return filepath.Join(dir, "cld.log") }

// mutexName depende do directorio de estado: un daemon por estado.
func mutexName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return `Local\cld-daemon-` + hashString(abs)
}

// singleInstance impide dous daemons co mesmo estado (o arranque
// automático e o acceso directo poderían lanzalo á vez).
func singleInstance(dir string) (func(), error) {
	name, _ := windows.UTF16PtrFromString(mutexName(dir))
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return nil, errAlreadyRunning
	}
	if err != nil {
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}

// daemonRunning indica se hai un daemon en marcha co estado dir.
func daemonRunning(dir string) bool {
	name, _ := windows.UTF16PtrFromString(mutexName(dir))
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// startDetached lanza un proceso que sobrevive a este.
func startDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
