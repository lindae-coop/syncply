package client

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Actualización en tempo real da web local.
//
// O daemon segue os eventos do seu Syncthing (/rest/events, long polling)
// e avisa ás páxinas abertas por SSE (GET /events). Cada aviso leva os
// tipos de cambio; a páxina volve pedir o seu HTML e substitúe as rexións
// que cambiaron (static/live.js). Case todo o que cambia no servidor chega
// así: ofertas novas, recursos retirados, conexións, equipos revogados.
// A lista de equipos vén de pairsrv: a páxina de equipos recárgase cos
// cambios de conexións e configuración e, ademais, periodicamente.

// Tipos de cambio.
const (
	ChangeState   = "state"   // Syncthing arrancou ou parou, pausa, vinculación…
	ChangeCluster = "cluster" // conexións, ofertas, configuración
	ChangeSync    = "sync"    // estado e progreso das carpetas
)

// Retardo antes de avisar: agrupa ráfagas de eventos. Os de sincronización
// chegan seguidos mentres se copian ficheiros e cada recarga consulta o
// estado de todas as carpetas, por iso esperan máis. (Variables para as probas.)
var (
	notifyDelay     = 500 * time.Millisecond
	notifySyncDelay = 2 * time.Second
)

var watchedEvents = map[string]string{
	"ConfigSaved":           ChangeCluster,
	"DeviceConnected":       ChangeCluster,
	"DeviceDisconnected":    ChangeCluster,
	"DevicePaused":          ChangeCluster,
	"DeviceResumed":         ChangeCluster,
	"PendingFoldersChanged": ChangeCluster,
	"FolderPaused":          ChangeSync,
	"FolderResumed":         ChangeSync,
	"FolderSummary":         ChangeSync,
	"FolderErrors":          ChangeSync,
	"StateChanged":          ChangeSync,
}

// Hub reparte os avisos de cambio entre as páxinas abertas.
type Hub struct {
	mu       sync.Mutex
	subs     map[*Sub]struct{}
	pending  map[string]bool
	timer    *time.Timer
	deadline time.Time
}

// Sub é unha subscrición: C avisa de que hai cambios e Take devólveos.
type Sub struct {
	C     <-chan struct{}
	c     chan struct{}
	mu    sync.Mutex
	kinds map[string]bool
}

func NewHub() *Hub { return &Hub{subs: map[*Sub]struct{}{}, pending: map[string]bool{}} }

func (h *Hub) Subscribe() (*Sub, func()) {
	s := &Sub{c: make(chan struct{}, 1), kinds: map[string]bool{}}
	s.C = s.c
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s, func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}
}

// Take devolve (ordenados) e borra os tipos de cambio acumulados.
func (s *Sub) Take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.kinds))
	for k := range s.kinds {
		out = append(out, k)
	}
	clear(s.kinds)
	slices.Sort(out)
	return out
}

// Notify anota un cambio; avísase despois dun pequeno retardo.
func (h *Hub) Notify(kind string) {
	if h == nil {
		return
	}
	d := notifyDelay
	if kind == ChangeSync {
		d = notifySyncDelay
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending[kind] = true
	at := time.Now().Add(d)
	switch {
	case h.timer == nil:
		h.timer = time.AfterFunc(d, h.flush)
	case at.Before(h.deadline) && h.timer.Stop():
		h.timer.Reset(d)
	default:
		return
	}
	h.deadline = at
}

func (h *Hub) flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timer = nil
	for s := range h.subs {
		s.mu.Lock()
		for k := range h.pending {
			s.kinds[k] = true
		}
		s.mu.Unlock()
		select {
		case s.c <- struct{}{}:
		default:
		}
	}
	clear(h.pending)
}

// Hub devolve o repartidor de avisos da App.
func (a *App) Hub() *Hub { return a.hub }

// WatchEvents segue os eventos de Syncthing ata que se cancele ctx e
// avisa dos cambios. Tamén avisa cando Syncthing deixa de responder ou
// volve facelo (detido, reiniciado).
func (a *App) WatchEvents(ctx context.Context) {
	types := make([]string, 0, len(watchedEvents))
	for t := range watchedEvents {
		types = append(types, t)
	}
	slices.Sort(types)
	since, started, up := 0, "", false
	for ctx.Err() == nil {
		evs, err := a.st.Events(ctx, since, types, eventsPoll)
		if err != nil {
			if up {
				up = false
				a.hub.Notify(ChangeState)
			}
			since = 0
			select {
			case <-ctx.Done():
			case <-time.After(eventsRetry):
			}
			continue
		}
		if !up {
			// Volve responder: os eventos anteriores xa non importan (a
			// páxina recárgase enteira); seguir desde o último.
			up = true
			a.hub.Notify(ChangeState)
			if n := len(evs); n > 0 {
				since = evs[n-1].ID
			}
			started, _ = a.st.StartTime(ctx)
			continue
		}
		for _, e := range evs {
			if k, ok := watchedEvents[e.Type]; ok {
				a.hub.Notify(k)
			}
			since = max(since, e.ID)
		}
		if len(evs) == 0 {
			// Un reinicio rápido pode non cortar a espera: os IDs volverían
			// empezar e nunca chegarían ao noso since.
			if st, err := a.st.StartTime(ctx); err == nil && st != started {
				up, since = false, 0
			}
		}
	}
}

// Valores de WatchEvents (variables para as probas). eventsPoll ten que
// ser menor có timeout do cliente HTTP de stapi (15 s).
var (
	eventsPoll  = 10 * time.Second
	eventsRetry = 2 * time.Second
)

//go:embed static/live.js
var liveJS []byte

const sseKeepAlive = 25 * time.Second

// handleEvents envía os avisos de cambio á páxina por SSE.
func (w *Web) handleEvents(rw http.ResponseWriter, r *http.Request) {
	if !w.authed(r) {
		http.Error(rw, "non autorizado", http.StatusUnauthorized)
		return
	}
	rc := http.NewResponseController(rw)
	h := rw.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	rw.WriteHeader(http.StatusOK)
	// retry: se se corta (daemon reiniciado), o navegador volve tentalo.
	fmt.Fprint(rw, "retry: 3000\n\n")
	if err := rc.Flush(); err != nil {
		return
	}
	sub, cancel := w.app.hub.Subscribe()
	defer cancel()
	t := time.NewTicker(sseKeepAlive)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.C:
			fmt.Fprintf(rw, "event: cambio\ndata: %s\n\n", strings.Join(sub.Take(), " "))
		case <-t.C:
			if !w.authed(r) { // sesión caducada
				return
			}
			fmt.Fprint(rw, ": ping\n\n")
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func serveLiveJS(rw http.ResponseWriter, _ *http.Request) {
	rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	rw.Write(liveJS)
}
