package client

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeSyncthing: anota cada arranque; o primeiro remata co código 3
// (reinicio pedido); os seguintes agardan ata que exista o ficheiro "stop"
// (o que fai Shutdown).
const fakeScript = `#!/bin/sh
d=$(dirname "$0")
echo "$*" >> "$d/starts"
[ "$(wc -l < "$d/starts")" -eq 1 ] && exit 3
while [ ! -f "$d/stop" ]; do sleep 0.05; done
rm -f "$d/stop"
exit 0
`

func TestSupervisor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script de shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "syncthing")
	if err := os.WriteFile(bin, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	starts := func() []string {
		b, _ := os.ReadFile(filepath.Join(dir, "starts"))
		return strings.Fields(strings.TrimSpace(string(b)))
	}
	count := func() int {
		b, _ := os.ReadFile(filepath.Join(dir, "starts"))
		return strings.Count(string(b), "\n")
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("esgotado o tempo: %s", what)
	}

	s := &Supervisor{
		Bin: bin, Home: filepath.Join(dir, "home"), GUIAddress: "unix://" + filepath.Join(dir, "gui.sock"),
		APIKey: "k", Log: io.Discard,
		Shutdown: func(context.Context) error {
			return os.WriteFile(filepath.Join(dir, "stop"), nil, 0o644)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	if s.Running() || count() != 0 {
		t.Fatal("non debería arrancar sen Start")
	}
	s.Start()
	waitFor("reinicio inmediato tras o código 3", func() bool { return count() == 2 && s.Running() })
	if a := starts(); a[0] != "serve" || !strings.Contains(strings.Join(a, " "), "--no-restart") {
		t.Errorf("argumentos: %v", a)
	}

	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Running() || s.Wanted() {
		t.Fatal("segue en marcha tras Stop")
	}
	time.Sleep(200 * time.Millisecond)
	if count() != 2 {
		t.Fatalf("volveu arrancar estando detido: %d", count())
	}

	s.Start()
	waitFor("arranca de novo", func() bool { return count() == 3 && s.Running() })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run non rematou ao cancelar")
	}
	if s.Running() {
		t.Fatal("o proceso segue tras cancelar")
	}
}
