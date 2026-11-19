//go:build !windows

package main

import (
	"errors"
	"fmt"
)

var errInstallerOnlyWindows = errors.New("en Linux usa install.sh do paquete")

func defaultAction() error      { usage(); return nil }
func install([]string) error    { return errInstallerOnlyWindows }
func uninstall([]string) error  { return errInstallerOnlyWindows }
func ensureDaemon(string) error { return nil }
func info(text string)          { fmt.Println(text) }
