package pairing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type fakeAuth map[string]string // usuario → contrasinal

func (f fakeAuth) CheckPassword(_ context.Context, user, pass, _ string) error {
	if p, ok := f[user]; ok && p == pass {
		return nil
	}
	return ErrBadLogin
}

var (
	codeRe = regexp.MustCompile(`id="code">(\d{6})<`)
	linkRe = regexp.MustCompile(`id="linktext"[^>]*>(syncply1:[A-Za-z0-9_-]+)<`)
)

func newWebEnv(t *testing.T) (*env, http.Handler) {
	t.Helper()
	e := newEnv(t)
	t.Setenv("SYNCPLY_DEV", "1") // http://srv
	w, err := NewWeb(e.svc, fakeAuth{"alice": "segredo", "eve": "x"}, WebOptions{
		PublicURL: "http://srv",
		Downloads: []Download{{Label: "Linux", URL: "/descargas/syncply.tar.gz"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return e.clock }
	return e, w.Handler()
}

func TestWebRequiresHTTPS(t *testing.T) {
	e := newEnv(t)
	t.Setenv("SYNCPLY_DEV", "")
	for _, u := range []string{"", "http://nube.example.org", "nube.example.org"} {
		if _, err := NewWeb(e.svc, fakeAuth{}, WebOptions{PublicURL: u}); err == nil {
			t.Errorf("publicURL %q aceptada fóra de desenvolvemento", u)
		}
	}
	if _, err := NewWeb(e.svc, fakeAuth{}, WebOptions{PublicURL: "https://nube.example.org/"}); err != nil {
		t.Error(err)
	}
}

func login(t *testing.T, h http.Handler, user, pass string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"user": {user}, "password": {pass}}
	r := httptest.NewRequest(http.MethodPost, "http://srv/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func get(h http.Handler, cookies []*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://srv/", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestWebShowsPairingCode(t *testing.T) {
	e, h := newWebEnv(t)
	if rec := get(h, nil); codeRe.MatchString(rec.Body.String()) || !strings.Contains(rec.Body.String(), `action="/login"`) {
		t.Fatal("sen sesión debería pedir o contrasinal")
	}
	if rec := login(t, h, "alice", "mal", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("contrasinal incorrecto: %d", rec.Code)
	}
	rec := login(t, h, "alice", "segredo", map[string]string{"Origin": "http://srv", "Sec-Fetch-Site": "same-origin"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", cookies)
	}

	page := get(h, cookies)
	m := codeRe.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatalf("sen código na páxina:\n%s", page.Body)
	}
	if page.Header().Get("Cache-Control") != "no-store" {
		t.Error("a páxina co código non debe gardarse en caché")
	}
	for _, want := range []string{"/descargas/syncply.tar.gz", "Común", "(0 de 2)"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("falta %q na páxina", want)
		}
	}
	// O texto de vinculación leva o servidor, o usuario e o Device ID.
	lm := linkRe.FindStringSubmatch(page.Body.String())
	if lm == nil {
		t.Fatalf("sen texto de vinculación:\n%s", page.Body)
	}
	ld, err := ParseLinkText(lm[1])
	if err != nil {
		t.Fatal(err)
	}
	if ld.URL != "http://srv" || ld.User != "alice" || ld.Server != e.svc.ServerID() {
		t.Errorf("texto de vinculación: %+v", ld)
	}
	// O código mostrado vale para vincular.
	if _, err := e.svc.Pair(context.Background(), PairRequest{User: "alice", OTP: m[1], DeviceID: devA1, DeviceName: "x"}); err != nil {
		t.Fatalf("emparellar co código da web: %v", err)
	}

	// Pechar sesión invalida a cookie.
	r := httptest.NewRequest(http.MethodPost, "http://srv/logout", nil)
	r.AddCookie(cookies[0])
	h.ServeHTTP(httptest.NewRecorder(), r)
	if codeRe.MatchString(get(h, cookies).Body.String()) {
		t.Error("a sesión segue activa tras saír")
	}
}

func TestWebRejectsCrossOrigin(t *testing.T) {
	_, h := newWebEnv(t)
	for _, hdr := range []map[string]string{
		{"Origin": "http://malo.example"},
		{"Origin": "null"},
		{"Sec-Fetch-Site": "cross-site"},
	} {
		if rec := login(t, h, "alice", "segredo", hdr); rec.Code != http.StatusForbidden {
			t.Errorf("%v: %d", hdr, rec.Code)
		}
	}
}

func TestWebLoginLockout(t *testing.T) {
	e, h := newWebEnv(t)
	for range loginMaxFails {
		login(t, h, "alice", "mal", nil)
	}
	if rec := login(t, h, "alice", "segredo", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("esperaba bloqueo: %d", rec.Code)
	}
	e.clock = e.clock.Add(loginLockFor)
	if rec := login(t, h, "alice", "segredo", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("tras o bloqueo: %d", rec.Code)
	}
}

// Un usuario co contrasinal correcto en SFTPGo pero sen acceso en pairsrv
// (p. ex. desactivado entre tanto) non ve código.
func TestWebUserWithoutAccess(t *testing.T) {
	_, h := newWebEnv(t)
	rec := login(t, h, "eve", "x", nil)
	page := get(h, rec.Result().Cookies())
	if codeRe.MatchString(page.Body.String()) || !strings.Contains(page.Body.String(), "non ten acceso") {
		t.Fatalf("eve:\n%s", page.Body)
	}
}

func TestWebRevokeDevice(t *testing.T) {
	e, h := newWebEnv(t)
	for _, p := range []struct{ user, dev string }{{"alice", devA1}, {"alice", devA2}, {"bob", devB1}} {
		if _, err := e.pair(p.user, p.dev); err != nil {
			t.Fatal(err)
		}
	}
	revoke := func(c []*http.Cookie, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://srv/devices/revoke", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range c {
			r.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	devices := func(user string) int {
		d, _ := e.svc.ListDevices(context.Background(), &Caller{User: user})
		return len(d)
	}

	// Sen sesión non fai nada.
	revoke(nil, url.Values{"device": {devA1}, "confirm": {"1"}})
	if devices("alice") != 2 {
		t.Fatal("revogado sen sesión")
	}
	c := login(t, h, "alice", "segredo", nil).Result().Cookies()
	if !strings.Contains(get(h, c).Body.String(), `action="/devices/revoke"`) {
		t.Fatal("a páxina non ofrece revogar")
	}
	// Primeiro a confirmación, sen recarga automática.
	rec := revoke(c, url.Values{"device": {devA1}})
	if b := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(b, `name="confirm" value="1"`) || strings.Contains(b, "refresh") {
		t.Fatalf("confirmación: %d\n%s", rec.Code, b)
	}
	if devices("alice") != 2 {
		t.Fatal("revogado sen confirmar")
	}
	// Un equipo doutro usuario non se pode revogar.
	revoke(c, url.Values{"device": {devB1}, "confirm": {"1"}})
	if devices("bob") != 1 {
		t.Fatal("alice revogou un equipo de bob")
	}
	if rec := revoke(c, url.Values{"device": {devA1}, "confirm": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("revogar: %d", rec.Code)
	}
	if devices("alice") != 1 {
		t.Fatal("equipo sen revogar")
	}
	if _, ok := e.st.devices[devA1]; ok {
		t.Error("o equipo revogado segue no Syncthing do servidor")
	}
	page := get(h, c).Body.String()
	if !strings.Contains(page, "okmsg") || !strings.Contains(page, "Revogado") {
		t.Errorf("sen mensaxe tras revogar:\n%s", page)
	}
	if strings.Contains(get(h, c).Body.String(), "Revogado") {
		t.Error("a mensaxe debe mostrarse só unha vez")
	}
}

func TestWebFilesLink(t *testing.T) {
	e := newEnv(t)
	t.Setenv("SYNCPLY_DEV", "")
	t.Setenv("SYNCPLY_FILES_URL", "")
	if _, err := NewWeb(e.svc, fakeAuth{}, WebOptions{PublicURL: "https://a.example", FilesURL: "http://b.example/web/client"}); err == nil {
		t.Error("filesURL http aceptada fóra de desenvolvemento")
	}
	// Sen opción, úsase SYNCPLY_FILES_URL (instalacións anteriores).
	t.Setenv("SYNCPLY_FILES_URL", "https://ficheiros.example/web/client")
	w, err := NewWeb(e.svc, fakeAuth{"alice": "segredo"}, WebOptions{PublicURL: "https://a.example"})
	if err != nil {
		t.Fatal(err)
	}
	h := w.Handler()
	c := login(t, h, "alice", "segredo", nil).Result().Cookies()
	if b := get(h, c).Body.String(); !strings.Contains(b, `href="https://ficheiros.example/web/client"`) {
		t.Errorf("sen ligazón aos ficheiros:\n%s", b)
	}
	// Sen ningunha das dúas, non hai ligazón.
	t.Setenv("SYNCPLY_FILES_URL", "")
	w, _ = NewWeb(e.svc, fakeAuth{"alice": "segredo"}, WebOptions{PublicURL: "https://a.example"})
	h = w.Handler()
	c = login(t, h, "alice", "segredo", nil).Result().Cookies()
	if strings.Contains(get(h, c).Body.String(), "os meus ficheiros na nube") {
		t.Error("ligazón sen filesURL")
	}
}
