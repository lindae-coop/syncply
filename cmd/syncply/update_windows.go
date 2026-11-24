package main

import (
	"context"
	"errors"
	"os/exec"
	"syscall"

	"github.com/lindae-coop/syncply/internal/update"
	"golang.org/x/sys/windows"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// platformUpdateDisabled: en Windows, só o instalado en
// %LOCALAPPDATA%\Programs\syncply.
func platformUpdateDisabled(exe, stBin string) string {
	if !runningInstalled() {
		return "non se executa desde o cartafol de instalación"
	}
	return ""
}

// applyUpdate (Windows): o syncply.exe novo é o instalador. Execútase con
// `install -update`, que pecha este daemon pola canle de control, substitúe
// os binarios e arranca o daemon novo (o mesmo camiño ca unha instalación a
// man por riba).
func applyUpdate(ctx context.Context, u *updater, staged string, m *update.Manifest) error {
	if err := checkBinary(ctx, staged, m.Version); err != nil {
		return err
	}
	return startDetached(staged, "install", "-update")
}

// En Windows non se pode substituír o proceso: applyUpdate non usa restart.
func reexec(string) error { return errors.New("reexec: non dispoñible en Windows") }
