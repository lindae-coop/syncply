package stapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
)

// A API de Syncthing do cliente vai nun socket Unix.
func TestUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sen sockets Unix")
	}
	sock := filepath.Join(t.TempDir(), "gui.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "chave" || r.URL.Path != "/rest/system/status" {
			http.Error(w, "non", http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"myID": "ID-DO-SOCKET"})
	})}
	go srv.Serve(ln)
	defer srv.Close()

	id, err := New("proba", "unix://"+sock, "chave").MyID(context.Background())
	if err != nil || id != "ID-DO-SOCKET" {
		t.Fatalf("%q %v", id, err)
	}
}
