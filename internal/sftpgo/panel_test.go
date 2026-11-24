package sftpgo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lindae-coop/syncply/internal/pairing"
)

// fakeAPI imita os obxectos da API REST de SFTPGo que usa o panel.
type fakeAPI struct {
	mu      sync.Mutex
	objs    map[string]map[string]map[string]any // kind → nome → obxecto
	calls   []string
	failing string // "MÉTODO /ruta" que falla con 400
}

func newFakeAPI(t *testing.T) (*fakeAPI, *Panel) {
	f := &fakeAPI{objs: map[string]map[string]map[string]any{"users": {}, "groups": {}, "folders": {}}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := NewPanel(PanelConfig{APIURL: srv.URL, HomesDir: "/srv/data/usuarios", GroupsDir: "/srv/data/grupos"})
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v2/")
	f.calls = append(f.calls, r.Method+" /"+p)
	if f.failing == r.Method+" /"+p {
		http.Error(w, `{"error":"fallo","message":"Non"}`, http.StatusBadRequest)
		return
	}
	if p == "token" {
		if u, pw, _ := r.BasicAuth(); u != "admin" || pw != "pw" {
			http.Error(w, `{"error":"bad"}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_at": "2030-01-01T00:00:00Z"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "{}", http.StatusUnauthorized)
		return
	}
	kind, name, _ := strings.Cut(p, "/")
	m := f.objs[kind]
	switch {
	case r.Method == http.MethodGet && name == "":
		out := []map[string]any{}
		for _, o := range m {
			out = append(out, o)
		}
		json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet:
		if o := m[name]; o != nil {
			json.NewEncoder(w).Encode(o)
		} else {
			http.Error(w, "{}", http.StatusNotFound)
		}
	case r.Method == http.MethodPost:
		var o map[string]any
		json.NewDecoder(r.Body).Decode(&o)
		key := "name"
		if kind == "users" {
			key = "username"
		}
		m[o[key].(string)] = o
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPut:
		var o map[string]any
		json.NewDecoder(r.Body).Decode(&o)
		m[name] = o
	case r.Method == http.MethodDelete:
		if m[name] == nil {
			http.Error(w, "{}", http.StatusNotFound)
			return
		}
		delete(m, name)
	}
}

func login(t *testing.T, p *Panel) pairing.AdminSession {
	t.Helper()
	if _, err := p.Login(context.Background(), "admin", "mal", ""); !errors.Is(err, pairing.ErrBadLogin) {
		t.Fatalf("contrasinal incorrecto: %v", err)
	}
	s, err := p.Login(context.Background(), "admin", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Expires().Year() != 2030 {
		t.Errorf("caducidade: %v", s.Expires())
	}
	return s
}

func TestPanelCreateUser(t *testing.T) {
	f, p := newFakeAPI(t)
	s := login(t, p)
	ctx := context.Background()
	f.objs["groups"][SyncthingGroup] = map[string]any{"name": SyncthingGroup}
	if err := s.CreateUser(ctx, "bea", "contrasinal", []string{"equipo"}); err != nil {
		t.Fatal(err)
	}
	u := f.objs["users"]["bea"]
	b, _ := json.Marshal(u)
	for _, want := range []string{`"home_dir":"/srv/data/usuarios/bea"`, `"permissions":{"/":["list","download"]}`,
		`{"name":"syncthing","type":1}`, `{"name":"equipo","type":2}`, `"password":"contrasinal"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("falta %s en %s", want, b)
		}
	}
	// O directorio persoal sen escritura non se ofrece para sincronizar.
	raw := rawUser{Username: "bea", HomeDir: "/srv/data/usuarios/bea", Permissions: map[string][]string{"/": {"list", "download"}}}
	if r := resources(&raw); len(r) != 0 {
		t.Errorf("o usuario do panel ofrece %v", r)
	}
}

func TestPanelSetGroupsKeepsOtherTypes(t *testing.T) {
	f, p := newFakeAPI(t)
	s := login(t, p)
	f.objs["users"]["ana"] = map[string]any{"username": "ana", "status": 1, "home_dir": "/h/ana", "quota_size": 100,
		"groups": []any{
			map[string]any{"name": SyncthingGroup, "type": 1},
			map[string]any{"name": "vello", "type": 2},
			map[string]any{"name": "lista", "type": 3},
		}}
	if err := s.SetGroups(context.Background(), "ana", []string{"novo", "lista"}); err != nil {
		t.Fatal(err)
	}
	u := f.objs["users"]["ana"]
	b, _ := json.Marshal(u["groups"])
	if string(b) != `[{"name":"syncthing","type":1},{"name":"lista","type":3},{"name":"novo","type":2}]` {
		t.Errorf("grupos: %s", b)
	}
	if u["quota_size"] != float64(100) || u["password"] != nil {
		t.Errorf("o PUT debe conservar o resto do usuario e non tocar o contrasinal: %v", u)
	}
	if err := s.SetPassword(context.Background(), "ana", "outro-segredo"); err != nil {
		t.Fatal(err)
	}
	if f.objs["users"]["ana"]["password"] != "outro-segredo" {
		t.Error("contrasinal sen cambiar")
	}

	users, err := s.Users(context.Background())
	if err != nil || len(users) != 1 {
		t.Fatal(users, err)
	}
	if a := users[0]; a.Primary != SyncthingGroup || len(a.Groups) != 1 || a.Groups[0] != "novo" || a.Other[0] != "lista" || !a.Enabled {
		t.Errorf("usuario: %+v", a)
	}
}

func TestPanelGroups(t *testing.T) {
	f, p := newFakeAPI(t)
	s := login(t, p)
	ctx := context.Background()
	if err := s.CreateGroup(ctx, SyncthingGroup, ""); !errors.Is(err, pairing.ErrBadRequest) {
		t.Errorf("grupo reservado: %v", err)
	}
	if err := s.CreateGroup(ctx, "equipo", "Equipo"); err != nil {
		t.Fatal(err)
	}
	fo := f.objs["folders"]["equipo"]
	if fo["mapped_path"] != "/srv/data/grupos/equipo" {
		t.Errorf("cartafol: %v", fo)
	}
	g := f.objs["groups"]["equipo"]
	b, _ := json.Marshal(g)
	for _, want := range []string{`"virtual_path":"/equipo"`, `"permissions":{"/equipo":["*"]}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("falta %s en %s", want, b)
		}
	}
	if err := s.CreateGroup(ctx, "equipo", ""); !errors.Is(err, pairing.ErrExists) {
		t.Errorf("duplicado: %v", err)
	}
	// Se falla o grupo, non queda o cartafol solto.
	f.failing = "POST /groups"
	if err := s.CreateGroup(ctx, "outro", ""); err == nil {
		t.Fatal("esperaba erro")
	} else if m := err.(*APIError).PublicMessage(); m != "Non: fallo" {
		t.Errorf("mensaxe: %q", m)
	}
	f.failing = ""
	if f.objs["folders"]["outro"] != nil {
		t.Error("quedou o cartafol do grupo fallido")
	}

	// O listado (a API devolve mapped_path dentro de virtual_folders).
	g["users"] = []any{"ana"}
	g["virtual_folders"] = []any{map[string]any{"name": "equipo", "virtual_path": "/equipo", "mapped_path": "/srv/data/grupos/equipo"}}
	f.objs["groups"]["allea"] = map[string]any{"name": "allea", "virtual_folders": []any{
		map[string]any{"name": "x", "virtual_path": "/y"}}}
	f.objs["groups"][SyncthingGroup] = map[string]any{"name": SyncthingGroup}
	groups, err := s.Groups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatal(groups, err)
	}
	for _, g := range groups {
		if (g.Name == "equipo") != g.Managed {
			t.Errorf("%s: managed=%v", g.Name, g.Managed)
		}
	}
	if err := s.DeleteGroup(ctx, "equipo"); !errors.Is(err, pairing.ErrGroupInUse) {
		t.Errorf("grupo con membros: %v", err)
	}
	if err := s.DeleteGroup(ctx, "allea"); !errors.Is(err, pairing.ErrBadRequest) {
		t.Errorf("grupo non creado polo panel: %v", err)
	}
	g["users"] = []any{}
	if err := s.DeleteGroup(ctx, "equipo"); err != nil {
		t.Fatal(err)
	}
	if f.objs["groups"]["equipo"] != nil || f.objs["folders"]["equipo"] != nil {
		t.Error("grupo ou cartafol sen borrar")
	}
}
