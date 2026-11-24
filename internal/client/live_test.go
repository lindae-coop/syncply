package client

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lindae-coop/syncply/internal/pairing"
	"github.com/lindae-coop/syncply/internal/stapi"
)

func fastNotify(t *testing.T) {
	t.Helper()
	d, s := notifyDelay, notifySyncDelay
	notifyDelay, notifySyncDelay = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { notifyDelay, notifySyncDelay = d, s })
}

func waitKinds(t *testing.T, s *Sub) string {
	t.Helper()
	select {
	case <-s.C:
		return strings.Join(s.Take(), " ")
	case <-time.After(2 * time.Second):
		t.Fatal("sen aviso")
		return ""
	}
}

func TestHubMergesAndDelays(t *testing.T) {
	fastNotify(t)
	h := NewHub()
	s, cancel := h.Subscribe()
	defer cancel()
	// Un de sincronización só espera máis; un doutro tipo adianta o aviso
	// e lévaos todos xuntos.
	start := time.Now()
	h.Notify(ChangeSync)
	h.Notify(ChangeCluster)
	h.Notify(ChangeCluster)
	if got := waitKinds(t, s); got != "cluster sync" {
		t.Errorf("tipos = %q", got)
	}
	if time.Since(start) >= notifySyncDelay {
		t.Error("o aviso de cluster non adiantou o de sync")
	}
	start = time.Now()
	h.Notify(ChangeSync)
	if got := waitKinds(t, s); got != "sync" {
		t.Errorf("tipos = %q", got)
	}
	if time.Since(start) < notifySyncDelay {
		t.Error("o aviso de sync non agardou")
	}
	cancel()
	h.Notify(ChangeState) // sen subscritores: non bloquea
	time.Sleep(3 * notifyDelay)
}

func TestEventsSSE(t *testing.T) {
	fastNotify(t)
	e := newEnv(t, true)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	get := func(c *http.Cookie) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
		req.Host = "127.0.0.1:8411" // o Host da URL pública
		if c != nil {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := get(nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("sen sesión: %d", resp.StatusCode)
	}
	resp := get(login(t, e.h))
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	// Agardar a subscrición (despois do "retry") antes de avisar.
	for l := range lines {
		if strings.HasPrefix(l, "retry:") {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	e.web.app.Hub().Notify(ChangeState)
	want := []string{"event: cambio", "data: state"}
	for _, w := range want {
		select {
		case l := <-lines:
			for l == "" {
				l = <-lines
			}
			if l != w {
				t.Fatalf("liña %q, esperada %q", l, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("sen %q", w)
		}
	}
}

func TestLivePages(t *testing.T) {
	e := newEnv(t, true)
	c := login(t, e.h)
	rec := do(e.h, http.MethodGet, "/", nil, c, nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP: %s", csp)
	}
	body := rec.Body.String()
	for _, s := range []string{`id="vivo"`, `data-rexion="estado"`, `data-rexion="ofertas"`, `src="/static/live.js"`} {
		if !strings.Contains(body, s) {
			t.Errorf("a páxina principal non ten %s", s)
		}
	}
	if rec := do(e.h, http.MethodGet, "/devices", nil, c, nil); !strings.Contains(rec.Body.String(), `data-every="60"`) {
		t.Error("a páxina de equipos non se recarga periodicamente")
	}
	if rec := do(e.h, http.MethodGet, "/static/live.js", nil, nil, nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Errorf("live.js: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	// A confirmación da vinculación é a resposta dun POST: non se pode
	// volver pedir, así que non se actualiza soa.
	e.web.app.state = State{}
	rec = do(e.h, http.MethodPost, "/pair/check", map[string]string{"Origin": public}, c,
		map[string][]string{"linktext": {pairing.LinkData{URL: e.ps.URL, User: "alice", Server: srvID}.Encode()}})
	if b := rec.Body.String(); !strings.Contains(b, `action="/pair"`) || strings.Contains(b, `id="vivo"`) {
		t.Errorf("confirmación: %d", rec.Code)
	}
}

// eventsST é un Syncthing falso para WatchEvents: responde a /rest/events
// coa seguinte resposta da lista e anota o since de cada petición.
type eventsST struct {
	mu      sync.Mutex
	replies [][]stapi.Event
	since   []string
	start   string
	down    bool
}

func (f *eventsST) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "abaixo", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Path {
	case "/rest/system/status":
		json.NewEncoder(w).Encode(map[string]string{"startTime": f.start})
	case "/rest/events":
		f.since = append(f.since, r.URL.Query().Get("since"))
		var evs []stapi.Event
		if len(f.replies) > 0 {
			evs, f.replies = f.replies[0], f.replies[1:]
		} else {
			time.Sleep(5 * time.Millisecond) // espera do long polling
		}
		json.NewEncoder(w).Encode(evs)
	}
}

func TestWatchEvents(t *testing.T) {
	fastNotify(t)
	p, r := eventsPoll, eventsRetry
	eventsPoll, eventsRetry = time.Second, 10*time.Millisecond
	t.Cleanup(func() { eventsPoll, eventsRetry = p, r })

	f := &eventsST{start: "t1", replies: [][]stapi.Event{
		{{ID: 4, Type: "Starting"}, {ID: 5, Type: "ConfigSaved"}}, // ao conectar: só "state"
		{{ID: 6, Type: "PendingFoldersChanged"}},
		{{ID: 7, Type: "FolderSummary"}},
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	app, err := NewApp(stapi.New("st", srv.URL, "k"), filepath.Join(t.TempDir(), "state.json"), "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	sub, cancel := app.Hub().Subscribe()
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go app.WatchEvents(ctx)

	got := map[string]bool{}
	for len(got) < 3 {
		for _, k := range strings.Split(waitKinds(t, sub), " ") {
			got[k] = true
		}
	}
	if !got[ChangeState] || !got[ChangeCluster] || !got[ChangeSync] {
		t.Errorf("tipos: %v", got)
	}

	// Reinicio rápido: cambia startTime sen cortar a conexión → volver a since=0.
	f.mu.Lock()
	f.start = "t2"
	f.mu.Unlock()
	if k := waitKinds(t, sub); k != ChangeState {
		t.Errorf("tras reiniciar: %q", k)
	}
	// Caído: avisa unha vez.
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	if k := waitKinds(t, sub); k != ChangeState {
		t.Errorf("caído: %q", k)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.since[0] != "0" || f.since[1] != "5" || f.since[2] != "6" {
		t.Errorf("since = %v", f.since[:3])
	}
	reset := false
	for _, s := range f.since[3:] {
		if s == "0" {
			reset = true
		}
	}
	if !reset {
		t.Error("non volveu a since=0 tras o reinicio")
	}
}
