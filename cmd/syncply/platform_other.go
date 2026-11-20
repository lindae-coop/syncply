//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

const hasConsole = true

func reportError(error) {}

func defaultStateDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "syncply")
}

// Fóra de Windows o daemon escribe en stderr (journal de systemd).
func defaultLogFile(string) string { return "" }

// Fóra de Windows o servizo de usuario xa evita duplicados.
func singleInstance(string) (func(), error) { return func() {}, nil }

func daemonRunning(string) bool { return false }
