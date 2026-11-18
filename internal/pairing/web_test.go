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

var codeRe = regexp.MustCompile(`id="code">(\d{6})<`)

func newWebEnv(t *testing.T) (*env, http.Handler) {
	t.Helper()
	e := newEnv(t)
	w := NewWeb(e.svc, fakeAuth{"alice": "segredo", "eve": "x"}, WebOptions{
		Downloads: []Download{{Label: "Linux", URL: "/descargas/cld.tar.gz"}},
	})
	w.now = func() time.Time { return e.clock }
	return e, w.Handler()
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
	for _, want := range []string{"/descargas/cld.tar.gz", "Común", "(0 de 2)"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("falta %q na páxina", want)
		}
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
