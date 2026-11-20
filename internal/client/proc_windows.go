package client

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepareCmd: Syncthing é unha aplicación de consola e o daemon de Windows non
// ten consola propia; sen isto abriría unha xanela.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// procTree: Syncthing 2.x lanza un proceso fillo, e en Windows matar o pai
// non mata o fillo. Visto nunha VM con Windows 11: o fillo quedaba orfo,
// sincronizando e bloqueando syncthing.exe (non se podía desinstalar nin
// actualizar). Un Job Object con KILL_ON_JOB_CLOSE mata a árbore enteira, e
// tamén se o daemon remata sen pechar Syncthing.
//
// O proceso asígnase ao job xusto despois de arrancar: o fillo de Syncthing
// créase despois e herda o job.
type procTree struct {
	p   *os.Process
	job windows.Handle
}

func newProcTree(p *os.Process) (*procTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &procTree{p: p, job: job}, nil
}

// Kill mata o proceso e os seus descendentes.
func (t *procTree) Kill() error {
	if t.job == 0 {
		return t.p.Kill()
	}
	return windows.TerminateJobObject(t.job, 1)
}

// Close mata o que quede (KILL_ON_JOB_CLOSE) e libera o job.
func (t *procTree) Close() {
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
