package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "dev", false},
		{"v0.2.0", "v0.1.0-3-gabcdef", false},
		{"v0.2.0-rc1", "v0.1.0", false},
		{"v01.2.0", "v0.1.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestReleaseKeysParse(t *testing.T) {
	if len(TrustedKeys()) != len(releaseKeys) {
		t.Fatal("hai claves de releaseKeys que non se poden ler")
	}
}

type fixture struct {
	srv  *httptest.Server
	dir  string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	pkg  string
}

func newFixture(t *testing.T, version string) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir()}
	f.pub, f.priv, _ = ed25519.GenerateKey(rand.Reader)
	f.pkg = filepath.Join(f.dir, PackageFiles["linux-amd64"])
	os.WriteFile(f.pkg, []byte("paquete "+version), 0o644)
	f.sign(t, version)
	f.srv = httptest.NewServer(http.StripPrefix("/descargas/", http.FileServer(http.Dir(f.dir))))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) sign(t *testing.T, version string) {
	man, err := BuildManifest(version, map[string]string{"linux-amd64": f.pkg})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.dir, ManifestFile), man, 0o644)
	os.WriteFile(filepath.Join(f.dir, SigFile), Sign(f.priv, man), 0o644)
}

func (f *fixture) client() *Client {
	return &Client{Keys: []ed25519.PublicKey{f.pub}, Platform: "linux-amd64"}
}

func TestCheckAndDownload(t *testing.T) {
	f := newFixture(t, "v0.2.0")
	ctx := context.Background()
	m, p, err := f.client().Check(ctx, f.srv.URL, "v0.1.0")
	if err != nil || p == nil || m.Version != "v0.2.0" {
		t.Fatalf("Check: %v %v %v", m, p, err)
	}
	dst := filepath.Join(t.TempDir(), "pkg")
	if err := f.client().Download(ctx, f.srv.URL, p, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "paquete v0.2.0" {
		t.Fatalf("contido %q", b)
	}

	// Mesma versión ou anterior: sen actualización (non se baixa de versión).
	for _, cur := range []string{"v0.2.0", "v0.3.0"} {
		if _, p, err := f.client().Check(ctx, f.srv.URL, cur); err != nil || p != nil {
			t.Fatalf("Check desde %s: %v %v", cur, p, err)
		}
	}
	// Outra plataforma sen paquete.
	c := f.client()
	c.Platform = "darwin-arm64"
	if _, p, err := c.Check(ctx, f.srv.URL, "v0.1.0"); err != nil || p != nil {
		t.Fatalf("plataforma sen paquete: %v %v", p, err)
	}
}

func TestRejects(t *testing.T) {
	ctx := context.Background()

	t.Run("outra clave", func(t *testing.T) {
		f := newFixture(t, "v0.2.0")
		other, _, _ := ed25519.GenerateKey(rand.Reader)
		c := &Client{Keys: []ed25519.PublicKey{other}, Platform: "linux-amd64"}
		if _, _, err := c.Check(ctx, f.srv.URL, "v0.1.0"); err == nil || !strings.Contains(err.Error(), "sinatura") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("manifesto modificado", func(t *testing.T) {
		f := newFixture(t, "v0.2.0")
		p := filepath.Join(f.dir, ManifestFile)
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "v0.2.0", "v0.9.0", 1)), 0o644)
		if _, _, err := f.client().Check(ctx, f.srv.URL, "v0.1.0"); err == nil {
			t.Fatal("aceptou un manifesto modificado")
		}
	})
	t.Run("paquete modificado", func(t *testing.T) {
		f := newFixture(t, "v0.2.0")
		_, p, err := f.client().Check(ctx, f.srv.URL, "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(f.pkg, []byte("paquete v0.2.X"), 0o644) // mesmo tamaño
		dst := filepath.Join(t.TempDir(), "pkg")
		if err := f.client().Download(ctx, f.srv.URL, p, dst); err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(dst); err == nil {
			t.Fatal("quedou o paquete descargado")
		}
	})
	t.Run("sen manifesto", func(t *testing.T) {
		f := newFixture(t, "v0.2.0")
		os.Remove(filepath.Join(f.dir, ManifestFile))
		if _, _, err := f.client().Check(ctx, f.srv.URL, "v0.1.0"); err != ErrNoManifest {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("sen claves", func(t *testing.T) {
		f := newFixture(t, "v0.2.0")
		c := &Client{Platform: "linux-amd64"}
		if _, _, err := c.Check(ctx, f.srv.URL, "v0.1.0"); err == nil {
			t.Fatal("sen claves non debería aceptar nada")
		}
	})
	t.Run("ficheiro con ruta", func(t *testing.T) {
		f := newFixture(t, "v0.2.0")
		man := []byte(`{"product":"syncply","version":"v0.2.0","packages":{"linux-amd64":{"file":"../x","sha256":"` +
			strings.Repeat("0", 64) + `","size":1}}}`)
		os.WriteFile(filepath.Join(f.dir, ManifestFile), man, 0o644)
		os.WriteFile(filepath.Join(f.dir, SigFile), Sign(f.priv, man), 0o644)
		if _, _, err := f.client().Check(ctx, f.srv.URL, "v0.1.0"); err == nil {
			t.Fatal("aceptou un nome de ficheiro con ruta")
		}
	})
	t.Run("versión de desenvolvemento", func(t *testing.T) {
		if _, err := BuildManifest("v0.1.0-2-gabc", nil); err == nil {
			t.Fatal("asinou unha versión de desenvolvemento")
		}
	})
}
