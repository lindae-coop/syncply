package pairing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeAdmin é un SFTPGo en memoria para o panel.
type fakeAdmin struct {
	pass    string
	users   map[string]*AdminUser
	pwds    map[string]string
	groups  map[string]*AdminGroup
	expires time.Time
	expired bool // o token xa non vale (401)
}

type statusErr int

func (e statusErr) Error() string   { return "http " + http.StatusText(int(e)) }
func (e statusErr) StatusCode() int { return int(e) }

func (f *fakeAdmin) Login(_ context.Context, user, pass, _ string) (AdminSession, error) {
	if user != "admin" || pass != f.pass {
		return nil, ErrBadLogin
	}
	return f, nil
}

func (f *fakeAdmin) Expires() time.Time { return f.expires }

func (f *fakeAdmin) check() error {
	if f.expired {
		return statusErr(http.StatusUnauthorized)
	}
	return nil
}

func (f *fakeAdmin) Users(context.Context) ([]AdminUser, error) {
	var out []AdminUser
	for _, u := range f.users {
		out = append(out, *u)
	}
	slices.SortFunc(out, func(a, b AdminUser) int { return strings.Compare(a.Name, b.Name) })
	return out, f.check()
}

func (f *fakeAdmin) Groups(context.Context) ([]AdminGroup, error) {
	var out []AdminGroup
	for _, g := range f.groups {
		g.Members = nil
		for _, u := range f.users {
			if slices.Contains(u.Groups, g.Name) {
				g.Members = append(g.Members, u.Name)
			}
		}
		out = append(out, *g)
	}
	return out, f.check()
}

func (f *fakeAdmin) CreateUser(_ context.Context, name, pass string, groups []string) error {
	f.users[name] = &AdminUser{Name: name, Enabled: true, Primary: "syncthing", Groups: groups}
	f.pwds[name] = pass
	return f.check()
}

func (f *fakeAdmin) SetPassword(_ context.Context, name, pass string) error {
	f.pwds[name] = pass
	return f.check()
}

func (f *fakeAdmin) SetGroups(_ context.Context, name string, groups []string) error {
	f.users[name].Groups = groups
	return f.check()
}

func (f *fakeAdmin) DeleteUser(_ context.Context, name string) error {
	delete(f.users, name)
	return f.check()
}

func (f *fakeAdmin) CreateGroup(_ context.Context, name, desc string) error {
	if f.groups[name] != nil {
		return ErrExists
	}
	f.groups[name] = &AdminGroup{Name: name, Description: desc, Managed: true,
		Folders: []AdminFolder{{Name: name, VirtualPath: "/" + name, Path: "/g/" + name}}}
	return f.check()
}

func (f *fakeAdmin) DeleteGroup(_ context.Context, name string) error {
	delete(f.groups, name)
	return f.check()
}

func newAdminEnv(t *testing.T) (*env, *fakeAdmin, http.Handler) {
	t.Helper()
	e := newEnv(t)
	t.Setenv("SYNCPLY_DEV", "1")
	w, err := NewWeb(e.svc, fakeAuth{}, WebOptions{PublicURL: "http://srv"})
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return e.clock }
	fa := &fakeAdmin{pass: "admin-segredo", pwds: map[string]string{},
		users:   map[string]*AdminUser{"alice": {Name: "alice", Enabled: true, Primary: "syncthing"}},
		groups:  map[string]*AdminGroup{},
		expires: e.clock.Add(20 * time.Minute)}
	w.EnableAdmin(fa)
	return e, fa, w.Handler()
}

func post(h http.Handler, path string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://srv"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func getPath(h http.Handler, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://srv"+path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func adminLogin(t *testing.T, h http.Handler) []*http.Cookie {
	t.Helper()
	rec := post(h, "/admin/login", url.Values{"user": {"admin"}, "password": {"admin-segredo"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login de admin: %d %s", rec.Code, rec.Body)
	}
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Path != "/admin/" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", c)
	}
	return c
}

func TestAdminLogin(t *testing.T) {
	_, _, h := newAdminEnv(t)
	if !strings.Contains(getPath(h, "/", nil).Body.String(), `href="/admin/"`) {
		t.Error("o login dos usuarios debería ligar ao panel")
	}
	if b := getPath(h, "/admin/", nil).Body.String(); !strings.Contains(b, `action="/admin/login"`) {
		t.Fatalf("sen sesión debería pedir credenciais:\n%s", b)
	}
	if rec := post(h, "/admin/login", url.Values{"user": {"admin"}, "password": {"mal"}}, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("contrasinal incorrecto: %d", rec.Code)
	}
	// Sen sesión de admin, as accións non fan nada.
	if rec := post(h, "/admin/groups", url.Values{"name": {"x"}}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("acción sen sesión: %d", rec.Code)
	}
	// A sesión dun usuario normal non serve para o panel (outra cookie).
	c := adminLogin(t, h)
	if b := getPath(h, "/admin/", c).Body.String(); !strings.Contains(b, "Usuarios (1)") {
		t.Fatalf("panel:\n%s", b)
	}
	// Orixe cruzada rexeitada.
	r := httptest.NewRequest(http.MethodPost, "http://srv/admin/groups", strings.NewReader("name=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://malo.example")
	r.AddCookie(c[0])
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("orixe cruzada: %d", rec.Code)
	}
}

func TestAdminLoginLockout(t *testing.T) {
	_, _, h := newAdminEnv(t)
	for range loginMaxFails {
		post(h, "/admin/login", url.Values{"user": {"admin"}, "password": {"mal"}}, nil)
	}
	if rec := post(h, "/admin/login", url.Values{"user": {"admin"}, "password": {"admin-segredo"}}, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("esperaba bloqueo: %d", rec.Code)
	}
}

func TestAdminSessionExpires(t *testing.T) {
	e, fa, h := newAdminEnv(t)
	c := adminLogin(t, h)
	e.clock = e.clock.Add(21 * time.Minute) // caduca o token de SFTPGo
	if b := getPath(h, "/admin/", c).Body.String(); !strings.Contains(b, `action="/admin/login"`) {
		t.Fatal("a sesión debería caducar co token de SFTPGo")
	}
	// Token rexeitado por SFTPGo antes de tempo.
	fa.expires = e.clock.Add(20 * time.Minute)
	c = adminLogin(t, h)
	fa.expired = true
	if b := getPath(h, "/admin/", c).Body.String(); !strings.Contains(b, "A sesión caducou") {
		t.Fatalf("token rexeitado:\n%s", b)
	}
}

func flash(t *testing.T, h http.Handler, c []*http.Cookie) string {
	t.Helper()
	b := getPath(h, "/admin/", c).Body.String()
	i := strings.Index(b, `<div class="msg`)
	if i < 0 {
		return ""
	}
	j := strings.Index(b[i:], "</div>")
	return b[i : i+j]
}

func TestAdminUsersAndGroups(t *testing.T) {
	e, fa, h := newAdminEnv(t)
	c := adminLogin(t, h)

	post(h, "/admin/groups", url.Values{"name": {"equipo"}, "description": {"O equipo"}}, c)
	if m := flash(t, h, c); !strings.Contains(m, "okmsg") || fa.groups["equipo"] == nil {
		t.Fatalf("crear grupo: %s", m)
	}
	for _, bad := range []string{"../x", "con espazo", "", ".oculto", strings.Repeat("a", 65)} {
		post(h, "/admin/groups", url.Values{"name": {bad}}, c)
		if m := flash(t, h, c); !strings.Contains(m, "errmsg") {
			t.Errorf("nome %q aceptado: %s", bad, m)
		}
	}

	// Usuario novo: contrasinal curto, contrasinais distintos, grupo descoñecido.
	for _, f := range []url.Values{
		{"name": {"bob"}, "password": {"curto"}, "password2": {"curto"}},
		{"name": {"bob"}, "password": {"longo-abcd"}, "password2": {"outro-abcd"}},
		{"name": {"bob"}, "password": {"longo-abcd"}, "password2": {"longo-abcd"}, "group": {"non"}},
		{"name": {"Alice"}, "password": {"longo-abcd"}, "password2": {"longo-abcd"}},
	} {
		post(h, "/admin/users", f, c)
		if m := flash(t, h, c); !strings.Contains(m, "errmsg") {
			t.Errorf("%v aceptado: %s", f, m)
		}
	}
	if fa.users["bob"] != nil {
		t.Fatal("bob creado cun formulario incorrecto")
	}

	// Un usuario borrado fóra do panel deixou equipos no rexistro: o novo
	// usuario co mesmo nome non os herda.
	if _, err := e.pair("bob", devB1); err != nil {
		t.Fatal(err)
	}
	post(h, "/admin/users", url.Values{"name": {"bob"}, "password": {"longo-abcd"}, "password2": {"longo-abcd"}, "group": {"equipo"}}, c)
	if m := flash(t, h, c); !strings.Contains(m, "okmsg") {
		t.Fatalf("crear bob: %s", m)
	}
	if u := fa.users["bob"]; u == nil || !slices.Equal(u.Groups, []string{"equipo"}) || fa.pwds["bob"] != "longo-abcd" {
		t.Fatalf("bob: %+v", u)
	}
	if devs, _ := e.svc.ListDevices(context.Background(), &Caller{User: "bob"}); len(devs) != 0 {
		t.Errorf("bob herdou equipos: %+v", devs)
	}

	// Grupos e contrasinal.
	post(h, "/admin/users/alice/groups", url.Values{"group": {"equipo"}}, c)
	if !slices.Equal(fa.users["alice"].Groups, []string{"equipo"}) {
		t.Fatalf("grupos de alice: %v", fa.users["alice"].Groups)
	}
	post(h, "/admin/users/alice/password", url.Values{"password": {"novo-contrasinal"}, "password2": {"novo-contrasinal"}}, c)
	if fa.pwds["alice"] != "novo-contrasinal" {
		t.Fatal("contrasinal de alice sen cambiar")
	}

	// Borrar o usuario esixe escribir o nome e borra os seus equipos.
	if _, err := e.pair("alice", devA1); err != nil {
		t.Fatal(err)
	}
	post(h, "/admin/users/alice/delete", url.Values{"confirm": {"non"}}, c)
	if fa.users["alice"] == nil {
		t.Fatal("alice borrada sen confirmar")
	}
	post(h, "/admin/users/alice/delete", url.Values{"confirm": {"alice"}}, c)
	if fa.users["alice"] != nil {
		t.Fatal("alice non borrada")
	}
	if devs, _ := e.svc.ListDevices(context.Background(), &Caller{User: "alice"}); len(devs) != 0 {
		t.Errorf("quedan equipos de alice: %+v", devs)
	}
	if _, ok := e.st.devices[devA1]; ok {
		t.Error("o equipo de alice segue no Syncthing do servidor")
	}

	post(h, "/admin/groups/equipo/delete", url.Values{"confirm": {"equipo"}}, c)
	if fa.groups["equipo"] != nil {
		t.Fatal("grupo sen borrar")
	}
}
