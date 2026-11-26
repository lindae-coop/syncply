package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "update.json"), []byte(`{"version":"v1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 32<<20) // máis ca os búferes do socket
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	mux := http.NewServeMux()
	mux.Handle("GET /descargas/", http.StripPrefix("/descargas/", filesOnly(http.FileServer(http.Dir(dir)))))
	// Coma o servidor real: un WriteTimeout curto non debe cortar as descargas.
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	get := func(path string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get("/descargas/update.json", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("update.json: %d, Cache-Control %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	// A revalidación funciona: sen cambios, 304.
	lm := resp.Header.Get("Last-Modified")
	if r := get("/descargas/update.json", map[string]string{"If-Modified-Since": lm}); r.StatusCode != http.StatusNotModified {
		t.Errorf("revalidación: %d", r.StatusCode)
	}
	for _, p := range []string{"/descargas/", "/descargas/sub/"} {
		if r := get(p, nil); r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d (sen listaxes)", p, r.StatusCode)
		}
	}
	// Lectura lenta: máis ca o WriteTimeout do servidor.
	resp = get("/descargas/big.bin", nil)
	time.Sleep(300 * time.Millisecond)
	n, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err != nil || n != int64(len(big)) {
		t.Errorf("descarga lenta: %d de %d bytes, %v", n, len(big), err)
	}
}
