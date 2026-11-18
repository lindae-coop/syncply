package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DanBaDo/cld/internal/pairing"
	"github.com/DanBaDo/cld/internal/stapi"
)

const (
	srvID  = "SERVERA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	public = "http://127.0.0.1:8411"
)

// fakeSyncthing responde ao mínimo que usa App.
func fakeSyncthing(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]any{
		"/rest/system/status":      map[string]any{"myID": "CLIENTE-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"},
		"/rest/config/devices":     []any{map[string]any{"deviceID": srvID, "paused": false}},
		"/rest/system/connections": map[string]any{"connections": map[string]any{srvID: map[string]any{"connected": true}}},
		"/rest/config/folders":     []any{map[string]any{"id": "f1", "label": "Común <b>", "path": "/home/u/cld/Común"}},
		"/rest/db/status":          map[string]any{"state": "syncing", "needFiles": 3, "needBytes": 1536, "globalFiles": 10, "globalBytes": 5 << 20},
		"/rest/cluster/pending/folders": map[string]any{
			"f2": map[string]any{"offeredBy": map[string]any{srvID: map[string]any{"label": "Documentos"}}},
			"f3": map[string]any{"offeredBy": map[string]any{"OUTRO": map[string]any{"label": "Alleo"}}},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(v)
	}))
}

// fakePairsrv devolve un equipo para /devices.
func fakePairsrv(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "non autorizado"})
			return
		}
		json.NewEncoder(w).Encode([]pairing.DeviceInfo{
			{ID: "A", Name: "portátil", Current: true, AddedAt: time.Now()},
			{ID: "B", Name: "sobremesa <i>", LastSeen: time.Now()},
		})
	}))
}

func newTestWeb(t *testing.T, paired bool) (*Web, http.Handler) {
	t.Helper()
	st, ps := fakeSyncthing(t), fakePairsrv(t)
	t.Cleanup(st.Close)
	t.Cleanup(ps.Close)
	app, err := NewApp(&Config{PairsrvURL: ps.URL}, stapi.New("st", st.URL, "k"),
		filepath.Join(t.TempDir(), "state.json"), "/home/u/cld")
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		app.state = State{User: "alice", Token: "tok", ServerDeviceID: srvID, DeviceName: "portátil"}
	}
	w, err := NewWeb(app, public, "control")
	if err != nil {
		t.Fatal(err)
	}
	return w, w.Handler()
}

func do(h http.Handler, method, target string, hdr map[string]string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, public+target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// login fai o que fai `cld open` e devolve a cookie de sesión.
func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := do(h, http.MethodPost, "/_control/login-url", map[string]string{controlHdr: "control"}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("control: %d", rec.Code)
	}
	var out struct{ URL string }
	json.NewDecoder(rec.Body).Decode(&out)
	u, _ := url.Parse(out.URL)
	rec = do(h, http.MethodGet, "/login?"+u.RawQuery, nil, nil, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login: %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Error("cookie sen HttpOnly/SameSite=Strict")
			}
			// O token é dun só uso.
			if rec := do(h, http.MethodGet, "/login?"+u.RawQuery, nil, nil, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("token reutilizado: %d", rec.Code)
			}
			return c
		}
	}
	t.Fatal("sen cookie de sesión")
	return nil
}

func TestAccessControl(t *testing.T) {
	w, h := newTestWeb(t, true)
	if rec := do(h, http.MethodGet, "/", nil, nil, nil); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "acceso directo") {
		t.Errorf("sen sesión: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/_control/login-url", map[string]string{controlHdr: "mal"}, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("clave de control incorrecta: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/login?t=inventado", nil, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("token inventado: %d", rec.Code)
	}
	// Token caducado.
	u, _ := url.Parse(w.LoginURL())
	w.now = func() time.Time { return time.Now().Add(2 * loginTTL) }
	if rec := do(h, http.MethodGet, "/login?"+u.RawQuery, nil, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("token caducado: %d", rec.Code)
	}
	w.now = time.Now

	c := login(t, h)
	if rec := do(h, http.MethodGet, "/", nil, c, nil); rec.Code != http.StatusOK {
		t.Errorf("con sesión: %d", rec.Code)
	}

	// DNS rebinding: outro Host.
	r := httptest.NewRequest(http.MethodGet, "http://malo.example:8411/", nil)
	r.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("host alleo: %d", rec.Code)
	}
	// O navegador só envía o Origin real se a política de referrer o permite.
	if rp := do(h, http.MethodGet, "/", nil, c, nil).Header().Get("Referrer-Policy"); rp == "no-referrer" || rp == "" {
		t.Errorf("Referrer-Policy %q fai que o navegador envíe Origin: null", rp)
	}
	// POST propio, como o envía un navegador.
	browser := map[string]string{"Origin": public, "Sec-Fetch-Site": "same-origin"}
	if rec := do(h, http.MethodPost, "/pair", browser, c, url.Values{"user": {""}}); rec.Code != http.StatusSeeOther {
		t.Errorf("POST do navegador: %d", rec.Code)
	}
	// CSRF: POST desde outra orixe.
	form := url.Values{"for": {"1h"}}
	if rec := do(h, http.MethodPost, "/pause", map[string]string{"Origin": "https://malo.example"}, c, form); rec.Code != http.StatusForbidden {
		t.Errorf("orixe allea: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/pause", map[string]string{"Origin": "null"}, c, form); rec.Code != http.StatusForbidden {
		t.Errorf("orixe opaca: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/pause", map[string]string{"Sec-Fetch-Site": "cross-site"}, c, form); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", rec.Code)
	}
}

func TestPagesRender(t *testing.T) {
	_, h := newTestWeb(t, true)
	c := login(t, h)
	body := do(h, http.MethodGet, "/", nil, c, nil).Body.String()
	for _, want := range []string{
		"alice", "Conectado ao servidor", "faltan 3 ficheiros (1,5 KiB)", "5 MiB",
		"Común &lt;b&gt;", // escapado
		"Documentos", "/home/u/cld/Documentos",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index: falta %q", want)
		}
	}
	if strings.Contains(body, "Alleo") {
		t.Error("amósase unha oferta que non vén do servidor")
	}
	body = do(h, http.MethodGet, "/devices", nil, c, nil).Body.String()
	for _, want := range []string{"Tes 2 equipos", "portátil", "(este equipo)", "sobremesa &lt;i&gt;", "Revogar «sobremesa &lt;i&gt;»"} {
		if !strings.Contains(body, want) {
			t.Errorf("devices: falta %q", want)
		}
	}
}

func TestUnpairedShowsPairing(t *testing.T) {
	_, h := newTestWeb(t, false)
	c := login(t, h)
	body := do(h, http.MethodGet, "/", nil, c, nil).Body.String()
	if !strings.Contains(body, "Vincular este equipo") || strings.Contains(body, "Os meus equipos") {
		t.Error("sen vincular debería amosarse só o formulario de vinculación")
	}
	rec := do(h, http.MethodPost, "/pair", nil, c, url.Values{"user": {""}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.Contains(loc, "err=") {
		t.Errorf("formulario baleiro: %d %s", rec.Code, loc)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1,5 KiB", 5 << 20: "5 MiB", 3 << 30: "3 GiB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("%d: %q, quería %q", n, got, want)
		}
	}
}
