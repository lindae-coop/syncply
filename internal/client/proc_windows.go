package client

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow evita que Syncthing (aplicación de consola) abra unha xanela
// de consola: o daemon de Windows non ten consola propia.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
