//go:build !windows

package client

import (
	"os"
	"os/exec"
	"syscall"
)

// prepareCmd: Syncthing nun grupo de procesos propio, para poder matar
// tamén o seu proceso fillo.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// procTree: Syncthing 2.x lanza un proceso fillo; matar só o pai deixaríao orfo.
type procTree struct{ p *os.Process }

func newProcTree(p *os.Process) (*procTree, error) { return &procTree{p: p}, nil }

// Kill mata o grupo de procesos (o pai e os seus descendentes).
func (t *procTree) Kill() error {
	if err := syscall.Kill(-t.p.Pid, syscall.SIGKILL); err != nil {
		return t.p.Kill()
	}
	return nil
}

// Close mata o que quede do grupo (se o pai xa rematou, o fillo podería seguir).
func (t *procTree) Close() { syscall.Kill(-t.p.Pid, syscall.SIGKILL) }
