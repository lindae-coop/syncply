package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Supervisor arranca e vixía o proceso de Syncthing do cliente.
//
// Syncthing execútase con --no-restart: é o supervisor quen o volve
// arrancar se remata (de inmediato se pediu reiniciarse, código 3; con
// espera crecente se fallou). Deter é parar o proceso de verdade.
type Supervisor struct {
	Bin        string // binario de syncthing
	Home       string // directorio de configuración e datos de Syncthing
	GUIAddress string // "unix:///ruta.sock" ou "host:porto"
	APIKey     string
	Log        io.Writer // saída de Syncthing (por defecto, stderr)
	// Shutdown pídelle a Syncthing que remate limpamente (API REST).
	Shutdown func(ctx context.Context) error
	// OnStart chámase cada vez que se lanza o proceso (p. ex. para agardar
	// a que responda e aplicar opcións).
	OnStart func(ctx context.Context)

	mu      sync.Mutex
	want    bool
	proc    *os.Process
	changed chan struct{}
}

const exitRestart = 3 // Syncthing: reinicio pedido pola API ou por cambio de configuración

func (s *Supervisor) init() {
	if s.changed == nil {
		s.changed = make(chan struct{}, 1)
	}
}

func (s *Supervisor) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Running indica se o proceso está en marcha.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc != nil
}

// Wanted indica se debería estar en marcha (non se detivo).
func (s *Supervisor) Wanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.want
}

// Start pide que Syncthing estea en marcha.
func (s *Supervisor) Start() {
	s.mu.Lock()
	s.init()
	s.want = true
	s.mu.Unlock()
	s.notify()
}

// Stop detén Syncthing: primeiro pola API e, se non remata, matándoo.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.init()
	s.want = false
	p := s.proc
	s.mu.Unlock()
	s.notify()
	if p == nil {
		return nil
	}
	if s.Shutdown != nil {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = s.Shutdown(sctx)
		cancel()
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !s.Running() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	log.Printf("syncthing non rematou a tempo; mátase")
	return p.Kill()
}

// Run mantén o proceso segundo o desexado ata que se cancele ctx; ao
// cancelar, detén Syncthing.
func (s *Supervisor) Run(ctx context.Context) {
	s.mu.Lock()
	s.init()
	s.mu.Unlock()
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if !s.Wanted() {
			select {
			case <-ctx.Done():
				return
			case <-s.changed:
				continue
			}
		}
		started := time.Now()
		code, err := s.runOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case !s.Wanted():
			log.Printf("syncthing detido")
			continue
		case code == exitRestart:
			log.Printf("syncthing pediu reiniciarse")
			backoff = time.Second
			continue
		default:
			log.Printf("syncthing rematou (código %d, %v); reinicio en %s", code, err, backoff)
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second // estivo ben un tempo: non penalizar
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		case <-s.changed:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (s *Supervisor) runOnce(ctx context.Context) (int, error) {
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return -1, err
	}
	// Un socket que quedou dunha execución anterior impediría escoitar.
	if sock, ok := strings.CutPrefix(s.GUIAddress, "unix://"); ok {
		if fi, err := os.Lstat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			os.Remove(sock)
		}
	}
	cmd := exec.Command(s.Bin, "serve",
		"--home="+s.Home,
		"--gui-address="+s.GUIAddress,
		"--no-browser", "--no-restart", "--no-upgrade",
	)
	cmd.Env = append(os.Environ(),
		"STGUIAPIKEY="+s.APIKey, // por env para que non se vexa na lista de procesos
		"STNODEFAULTFOLDER=1",
		"STVERSIONEXTRA=syncply",
	)
	out := s.Log
	if out == nil {
		out = os.Stderr
	}
	cmd.Stdout, cmd.Stderr = out, out
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("arrancando %s: %w", s.Bin, err)
	}
	s.mu.Lock()
	s.proc = cmd.Process
	s.mu.Unlock()
	log.Printf("syncthing arrancado (pid %d)", cmd.Process.Pid)

	octx, cancel := context.WithCancel(ctx)
	if s.OnStart != nil {
		go s.OnStart(octx)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		// Peche do daemon: deter limpamente.
		if s.Shutdown != nil {
			sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Shutdown(sctx)
			c()
		}
		select {
		case err = <-done:
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
			err = <-done
		}
	}
	cancel()
	s.mu.Lock()
	s.proc = nil
	s.mu.Unlock()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), err
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
