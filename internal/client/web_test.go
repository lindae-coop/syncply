package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DanBaDo/cld/internal/pairing"
	"github.com/DanBaDo/cld/internal/stapi"
)

const (
	srvID  = "SERVERA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	public = "http://127.0.0.1:8411"
)

// fakeST é un Syncthing falso: responde ao mínimo que usa App e garda as
// escrituras (PUT/PATCH) por "MÉTODO ruta".
type fakeST struct {
	*httptest.Server
	mu     sync.Mutex
	writes map[string]map[string]any
}

func (f *fakeST) write(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[key]
}

func fakeSyncthing(t *testing.T) *fakeST {
	t.Helper()
	routes := map[string]any{
		"/rest/system/status":          map[string]any{"myID": "CLIENTE-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"},
		"/rest/config/devices":         []any{map[string]any{"deviceID": srvID, "paused": false}},
		"/rest/config/defaults/device": map[string]any{"addresses": []any{"dynamic"}},
		"/rest/system/connections":     map[string]any{"connections": map[string]any{srvID: map[string]any{"connected": true}}},
		"/rest/config/folders":         []any{map[string]any{"id": "f1", "label": "Común <b>", "path": "/home/u/cld/Común"}},
		"/rest/db/status":              map[string]any{"state": "syncing", "needFiles": 3, "needBytes": 1536, "globalFiles": 10, "globalBytes": 5 << 20},
		"/rest/cluster/pending/folders": map[string]any{
			"f2": map[string]any{"offeredBy": map[string]any{srvID: map[string]any{"label": "Documentos"}}},
			"f3": map[string]any{"offeredBy": map[string]any{"OUTRO": map[string]any{"label": "Alleo"}}},
		},
	}
	f := &fakeST{writes: map[string]map[string]any{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || r.Method == http.MethodPatch {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.writes[r.Method+" "+r.URL.Path] = body
			f.mu.Unlock()
			return
		}
		v, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(v)
	}))
	return f
}

const goodOTP = "123456"

// pairsrvHandler é un pairsrv falso: emparella a alice co código goodOTP e
// devolve dous equipos en /devices.
func pairsrvHandler(serverID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/pair", func(w http.ResponseWriter, r *http.Request) {
		var req pairing.PairRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.User != "alice" || req.OTP != goodOTP {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "usuario ou código de vinculación incorrecto"})
			return
		}
		json.NewEncoder(w).Encode(pairing.PairResponse{ServerDeviceID: serverID, ServerAddresses: []string{"tcp://srv:22000"},
			Token: "tok", SyncthingOptions: map[string]any{"relaysEnabled": true}})
	})
	mux.HandleFunc("GET /api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "non autorizado"})
			return
		}
		json.NewEncoder(w).Encode([]pairing.DeviceInfo{
			{ID: "A", Name: "portátil", Current: true, AddedAt: time.Now()},
			{ID: "B", Name: "sobremesa <i>", LastSeen: time.Now()},
		})
	})
	return mux
}

type testEnv struct {
	web *Web
	h   http.Handler
	st  *fakeST
	ps  *httptest.Server
}

func newEnv(t *testing.T, paired bool) *testEnv {
	t.Helper()
	t.Setenv("CLD_DEV", "1") // pairsrv falso por http
	st, ps := fakeSyncthing(t), httptest.NewServer(pairsrvHandler(srvID))
	t.Cleanup(st.Close)
	t.Cleanup(ps.Close)
	app, err := NewApp(stapi.New("st", st.URL, "k"), filepath.Join(t.TempDir(), "state.json"), "/home/u/cld")
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		app.state = State{PairsrvURL: ps.URL, User: "alice", Token: "tok", ServerDeviceID: srvID, DeviceName: "portátil"}
	}
	w, err := NewWeb(app, public, "control")
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{web: w, h: w.Handler(), st: st, ps: ps}
}

func newTestWeb(t *testing.T, paired bool) (*Web, http.Handler) {
	e := newEnv(t, paired)
	return e.web, e.h
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
	if rec := do(h, http.MethodPost, "/pair/check", browser, c, url.Values{"linktext": {""}}); rec.Code != http.StatusSeeOther {
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
	if !strings.Contains(body, "texto de vinculación") || !strings.Contains(body, `action="/pair/check"`) || strings.Contains(body, "Os meus equipos") {
		t.Error("sen vincular debería amosarse só o formulario de vinculación")
	}
	for _, text := range []string{"", "123456", "cld1:estragado"} {
		rec := do(h, http.MethodPost, "/pair/check", nil, c, url.Values{"linktext": {text}})
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.Contains(loc, "err=") {
			t.Errorf("texto %q: %d %s", text, rec.Code, loc)
		}
	}
}

func TestPairWithLinkText(t *testing.T) {
	e := newEnv(t, false)
	c := login(t, e.h)
	link := pairing.LinkData{URL: e.ps.URL, User: "alice", Server: srvID}.Encode()

	// Paso 1: confirmación co servidor e o usuario.
	rec := do(e.h, http.MethodPost, "/pair/check", nil, c, url.Values{"linktext": {"  " + link + "\n"}})
	body := rec.Body.String()
	host := strings.TrimPrefix(e.ps.URL, "http://")
	for _, want := range []string{"Vas vincular", host, "<strong>alice</strong>", link, `action="/pair"`, "non vai cifrada"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmación: falta %q", want)
		}
	}
	if strings.Contains(body, "non se pode contactar") {
		t.Error("o servidor responde")
	}

	// Código incorrecto: volve á confirmación co erro.
	rec = do(e.h, http.MethodPost, "/pair", nil, c, url.Values{"linktext": {link}, "otp": {"000000"}, "device": {"portátil"}})
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "Usuario ou código") || !strings.Contains(body, "Vas vincular") {
		t.Fatalf("código incorrecto: %d\n%s", rec.Code, body)
	}
	if e.web.app.snapshotState().paired() {
		t.Fatal("vinculado cun código incorrecto")
	}

	rec = do(e.h, http.MethodPost, "/pair", nil, c, url.Values{"linktext": {link}, "otp": {goodOTP}, "device": {"portátil"}})
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "ok=") {
		t.Fatalf("vincular: %d %s", rec.Code, loc)
	}
	s := e.web.app.snapshotState()
	if !s.paired() || s.PairsrvURL != e.ps.URL || s.User != "alice" || s.Token != "tok" || s.ServerDeviceID != srvID {
		t.Fatalf("estado: %+v", s)
	}
	if d := e.st.write("PUT /rest/config/devices/" + srvID); d == nil || d["introducer"] != true {
		t.Errorf("servidor como introducer: %v", d)
	}
	// Opcións: as seguras por defecto e, por riba, as do servidor.
	o := e.st.write("PATCH /rest/config/options")
	if o["relaysEnabled"] != true || o["globalAnnounceEnabled"] != false || o["urAccepted"] != float64(-1) {
		t.Errorf("opcións aplicadas: %v", o)
	}
	// Xa vinculado, a API do servidor gardado funciona.
	if body := do(e.h, http.MethodGet, "/devices", nil, c, nil).Body.String(); !strings.Contains(body, "Tes 2 equipos") {
		t.Error("equipos tras vincular")
	}
}

func TestPairRejectsOtherServer(t *testing.T) {
	e := newEnv(t, false)
	other := "OUTROSR-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	link := pairing.LinkData{URL: e.ps.URL, User: "alice", Server: other}.Encode()
	err := e.web.app.Pair(t.Context(), link, goodOTP, "portátil")
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("esperaba rexeitamento: %v", err)
	}
	if e.web.app.snapshotState().paired() || e.st.write("PUT /rest/config/devices/"+srvID) != nil {
		t.Error("non debería gardar nin engadir o servidor")
	}
}

// Fóra de desenvolvemento só se acepta https, cun certificado válido para
// as CA do sistema.
func TestPairsrvHTTPS(t *testing.T) {
	e := newEnv(t, false)
	t.Setenv("CLD_DEV", "")
	ts := httptest.NewTLSServer(pairsrvHandler(srvID))
	defer ts.Close()
	httpLink := pairing.LinkData{URL: e.ps.URL, User: "alice", Server: srvID}.Encode()
	if _, err := e.web.app.CheckLink(t.Context(), httpLink); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("http fóra de desenvolvemento: %v", err)
	}
	link := pairing.LinkData{URL: ts.URL, User: "alice", Server: srvID}.Encode()
	// Certificado dunha CA que o sistema non coñece: non responde.
	if c, err := e.web.app.CheckLink(t.Context(), link); err != nil || c.ReachErr == "" || c.Insecure {
		t.Errorf("certificado descoñecido: %+v %v", c, err)
	}
	// Coa CA instalada (como faría o sistema), vincula.
	old := pairsrvHTTP
	pairsrvHTTP = ts.Client()
	defer func() { pairsrvHTTP = old }()
	if err := e.web.app.Pair(t.Context(), link, goodOTP, "portátil"); err != nil {
		t.Fatalf("vincular por https: %v", err)
	}
	if s := e.web.app.snapshotState(); s.PairsrvURL != ts.URL {
		t.Errorf("URL gardada: %q", s.PairsrvURL)
	}
}

// Estando vinculado pódese actualizar o enderezo do servidor (mesmo
// servidor e usuario).
func TestRelink(t *testing.T) {
	e := newEnv(t, true)
	app := e.web.app
	ps2 := httptest.NewServer(pairsrvHandler(srvID))
	defer ps2.Close()
	link := func(u, user, server string) string {
		return pairing.LinkData{URL: u, User: user, Server: server}.Encode()
	}
	if _, err := app.CheckLink(t.Context(), link(ps2.URL, "bob", srvID)); err == nil || !strings.Contains(err.Error(), "vinculado como «alice»") {
		t.Errorf("texto doutro usuario: %v", err)
	}
	other := "OUTROSR-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	if _, err := app.CheckLink(t.Context(), link(ps2.URL, "alice", other)); err == nil || !strings.Contains(err.Error(), "doutro servidor") {
		t.Errorf("texto doutro servidor: %v", err)
	}
	c := login(t, e.h)
	body := do(e.h, http.MethodPost, "/pair/check", nil, c, url.Values{"linktext": {link(ps2.URL, "alice", srvID)}}).Body.String()
	if !strings.Contains(body, "Vas actualizar a conexión") || !strings.Contains(body, `value="portátil"`) {
		t.Errorf("confirmación de actualizar:\n%s", body)
	}
	rec := do(e.h, http.MethodPost, "/pair", nil, c, url.Values{"linktext": {link(ps2.URL, "alice", srvID)}, "otp": {goodOTP}, "device": {"portátil"}})
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/devices?ok=") {
		t.Fatalf("actualizar: %d %s", rec.Code, loc)
	}
	if s := app.snapshotState(); s.PairsrvURL != ps2.URL {
		t.Errorf("URL gardada: %q", s.PairsrvURL)
	}
	// Ao actualizar non se quita a pausa do servidor.
	if d := e.st.write("PUT /rest/config/devices/" + srvID); d == nil || d["paused"] != nil {
		t.Errorf("dispositivo servidor: %v", d)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1,5 KiB", 5 << 20: "5 MiB", 3 << 30: "3 GiB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("%d: %q, quería %q", n, got, want)
		}
	}
}

func TestStoppedState(t *testing.T) {
	w, h := newTestWeb(t, true)
	w.app.SetSupervisor(&Supervisor{}) // sen Start: detido
	c := login(t, h)
	body := do(h, http.MethodGet, "/", nil, c, nil).Body.String()
	if !strings.Contains(body, "Sincronización detida") || !strings.Contains(body, "Arrancar") {
		t.Error("non se amosa o estado detido")
	}
	if strings.Contains(body, "Dispoñibles no servidor") {
		t.Error("detido non debería consultar recursos")
	}
	rec := do(h, http.MethodPost, "/pause", nil, c, url.Values{"for": {"1h"}})
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "detida") {
		t.Errorf("pausar estando detido: %s", loc)
	}
}
