//go:build !windows && !linux

package main

import (
	"errors"
	"fmt"
	"runtime"
)

var errNoInstaller = errors.New("aínda non hai instalador para " + runtime.GOOS)

func defaultAction() error      { usage(); return nil }
func install([]string) error    { return errNoInstaller }
func uninstall([]string) error  { return errNoInstaller }
func ensureDaemon(string) error { return nil }
func info(text string)          { fmt.Println(text) }

// prepareSyncthing: só Linux instala o Syncthing embebido ao arrancar.
func prepareSyncthing() {}
