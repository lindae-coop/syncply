package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/lindae-coop/syncply/internal/stapi"
)

// folderST é un Syncthing falso coa configuración das carpetas: anota cada
// PUT dunha carpeta (paused, path) e, mentres a carpeta está en pausa,
// comproba que os ficheiros non estean a medio mover.
type folderST struct {
	mu      sync.Mutex
	folders map[string]map[string]any
	puts    []string
}

func (f *folderST) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/rest/config/folders":
		var out []any
		for _, v := range f.folders {
			out = append(out, v)
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(r.URL.Path, "/rest/config/folders/"):
		id := strings.TrimPrefix(r.URL.Path, "/rest/config/folders/")
		cur, ok := f.folders[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPut {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.folders[id] = body
			p, _ := body["path"].(string)
			f.puts = append(f.puts, map[bool]string{true: "pausa", false: "marcha"}[body["paused"] == true]+" "+filepath.Base(p))
			return
		}
		json.NewEncoder(w).Encode(cur)
	case r.URL.Path == "/rest/db/status":
		state := "idle"
		if f.folders[r.URL.Query().Get("folder")]["paused"] == true {
			state = "" // coma Syncthing 2.1.7
		}
		json.NewEncoder(w).Encode(map[string]any{"state": state})
	default:
		http.NotFound(w, r)
	}
}

func newMoveEnv(t *testing.T) (*App, *folderST, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "Común")
	for _, d := range []string{src + "/.stfolder", src + "/sub", root + "/outro"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("ola"), 0o644)
	f := &folderST{folders: map[string]map[string]any{
		"f1": {"id": "f1", "label": "Común", "path": src, "paused": false},
		"f2": {"id": "f2", "label": "Outro", "path": root + "/outro", "paused": false},
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	app, err := NewApp(stapi.New("st", srv.URL, "k"), filepath.Join(t.TempDir(), "state.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	return app, f, root
}

func TestMove(t *testing.T) {
	app, f, root := newMoveEnv(t)
	dst := filepath.Join(root, "novo", "dentro", "Común")
	if err := app.Move(context.Background(), "f1", dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "sub", "a.txt")); err != nil || string(b) != "ola" {
		t.Fatalf("ficheiro movido: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".stfolder")); err != nil {
		t.Error("o marcador .stfolder non se moveu")
	}
	if _, err := os.Stat(filepath.Join(root, "Común")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a carpeta vella segue aí")
	}
	// Primeiro pausa (coa ruta vella) e só despois a ruta nova en marcha.
	if got := strings.Join(f.puts, ", "); got != "pausa Común, marcha Común" || f.folders["f1"]["path"] != dst {
		t.Errorf("PUT: %s; path %v", got, f.folders["f1"]["path"])
	}

	// Destino baleiro existente: vale.
	empty := filepath.Join(root, "baleira")
	os.Mkdir(empty, 0o755)
	if err := app.Move(context.Background(), "f1", empty); err != nil {
		t.Fatalf("destino baleiro: %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, "sub", "a.txt")); err != nil {
		t.Error("non se moveu á carpeta baleira")
	}
}

func TestMoveRejects(t *testing.T) {
	app, f, root := newMoveEnv(t)
	src := filepath.Join(root, "Común")
	full := filepath.Join(root, "chea")
	os.MkdirAll(filepath.Join(full, "x"), 0o755)
	cases := map[string]string{
		"relativa":        "Común2",
		"a mesma":         src,
		"dentro da vella": filepath.Join(src, "sub", "novo"),
		"non baleira":     full,
		"outro recurso":   filepath.Join(root, "outro", "Común"),
		"contén outro":    root,
	}
	for name, dst := range cases {
		err := app.Move(context.Background(), "f1", dst)
		var ue *UserError
		if !errors.As(err, &ue) {
			t.Errorf("%s: %v (esperaba un erro para o usuario)", name, err)
		}
	}
	if err := app.Move(context.Background(), "non-existe", filepath.Join(root, "x")); err == nil {
		t.Error("recurso inexistente")
	}
	if len(f.puts) != 0 {
		t.Errorf("non debería tocar a configuración: %v", f.puts)
	}
	if _, err := os.Stat(filepath.Join(src, "sub", "a.txt")); err != nil {
		t.Error("os ficheiros non deberían moverse")
	}
}

func TestIsCrossDevice(t *testing.T) {
	if !isCrossDevice(&os.LinkError{Op: "rename", Err: syscall.EXDEV}) && os.PathSeparator == '/' {
		t.Error("EXDEV")
	}
	if isCrossDevice(&os.LinkError{Op: "rename", Err: syscall.ENOENT}) || isCrossDevice(errors.New("x")) {
		t.Error("falso positivo")
	}
}

func TestBrowseDir(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"beta", "Alfa", ".oculta", "gamma/dentro"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "ficheiro.txt"), nil, 0o644)
	os.Symlink(filepath.Join(root, "gamma"), filepath.Join(root, "ligazón"))
	app, _ := NewApp(nil, filepath.Join(t.TempDir(), "s.json"), root)
	b := app.BrowseDir(root)
	if got := strings.Join(b.Subdirs, ","); got != "Alfa,beta,gamma,ligazón" {
		t.Errorf("subcarpetas: %s", got)
	}
	if b.Parent != filepath.Dir(root) || b.Err != "" {
		t.Errorf("parent %q err %q", b.Parent, b.Err)
	}
	if b := app.BrowseDir(filepath.Join(root, "non-existe")); b.Err == "" {
		t.Error("carpeta inexistente sen erro")
	}
	for in, want := range map[[2]string]string{
		{root, "Novo"}: filepath.Join(root, "Novo"),
		{root, ""}:     root,
		{root, "a/b"}:  "",
		{root, ".."}:   "",
		{"rel", "x"}:   "",
	} {
		got, ok := pickedPath(in[0], in[1])
		if (want == "") == ok || got != want {
			t.Errorf("pickedPath(%q, %q) = %q, %v", in[0], in[1], got, ok)
		}
	}
}

func TestBrowsePages(t *testing.T) {
	e := newEnv(t, true)
	c := login(t, e.h)
	if rec := do(e.h, http.MethodGet, "/browse?elixir=f2&modo=aceptar", nil, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("sen sesión: %d", rec.Code)
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Escritorio"), 0o755)
	// A suxerida aínda non existe: empeza na primeira por riba que exista.
	rec := do(e.h, http.MethodGet, "/browse?elixir=f2&modo=aceptar&dir="+filepath.Join(dir, "syncply", "Documentos"), nil, c, nil)
	b := rec.Body.String()
	if !strings.Contains(b, "«Documentos»") || !strings.Contains(b, ">Escritorio</a>") || !strings.Contains(b, `name="nome" value="Documentos"`) {
		t.Errorf("explorador: %d\n%s", rec.Code, b)
	}
	if rec := do(e.h, http.MethodGet, "/browse?elixir=xx&modo=aceptar", nil, c, nil); !strings.Contains(rec.Body.String(), "xa non está dispoñible") {
		t.Error("recurso descoñecido")
	}
	// Volta á páxina principal coa carpeta escollida.
	rec = do(e.h, http.MethodGet, "/?elixir=f2&modo=aceptar&dir="+dir+"&nome=Docs", nil, c, nil)
	if b := rec.Body.String(); !strings.Contains(b, `value="`+filepath.Join(dir, "Docs")+`" spellcheck="false" required autofocus`) {
		t.Errorf("oferta sen a ruta escollida:\n%s", b)
	}
	rec = do(e.h, http.MethodGet, "/?elixir=f1&modo=mover&dir="+dir+"&nome=Común", nil, c, nil)
	if b := rec.Body.String(); !strings.Contains(b, "<details open>") || !strings.Contains(b, `value="`+filepath.Join(dir, "Común")+`"`) {
		t.Errorf("mover sen a ruta escollida:\n%s", b)
	}
	if rec := do(e.h, http.MethodGet, "/?elixir=f2&modo=aceptar&dir="+dir+"&nome=a/b", nil, c, nil); !strings.Contains(rec.Body.String(), "non pode levar") {
		t.Error("nome con barra aceptado")
	}
}
