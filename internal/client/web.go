package client

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lindae-coop/syncply/internal/brand"
)

// A web só escoita en loopback, pero calquera proceso ou usuario do equipo
// podería chegar a ela, e unha páxina web calquera podería intentalo desde o
// navegador. Por iso:
//
//   - Entrar require unha URL de acceso dun só uso (60 s) que só pode pedir
//     quen le o ficheiro privado do daemon (ui.json, 0600): `syncply open`.
//   - Despois, cookie de sesión HttpOnly, SameSite=Strict.
//   - Comprobación de Host (contra DNS rebinding) e de Origin nos POST
//     (contra CSRF).
const (
	loginTTL    = 60 * time.Second
	sessionTTL  = 12 * time.Hour
	cookieName  = "syncply_session"
	controlPath = "/_control/"
	controlHdr  = "X-Syncply-Control"
)

//go:embed templates/*.html
var templatesFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"bytes":  humanBytes,
	"localt": func(t time.Time) string { return t.Local().Format("02/01/2006 15:04") },
}).ParseFS(templatesFS, "templates/*.html"))

type Web struct {
	app        *App
	public     *url.URL // URL coa que o navegador accede (http://127.0.0.1:porto)
	controlKey string
	// OnShutdown, se se indica, atende a orde de control "shutdown" (o
	// instalador de Windows pecha así o daemon: alí non hai sinais).
	OnShutdown func()
	now        func() time.Time

	mu       sync.Mutex
	logins   map[string]time.Time // token dun só uso → caducidade
	sessions map[string]time.Time
}

func NewWeb(app *App, publicURL, controlKey string) (*Web, error) {
	u, err := url.Parse(strings.TrimRight(publicURL, "/"))
	if err != nil || u.Host == "" {
		return nil, errors.New("URL pública non válida")
	}
	return &Web{app: app, public: u, controlKey: controlKey, now: time.Now,
		logins: map[string]time.Time{}, sessions: map[string]time.Time{}}, nil
}

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// LoginURL crea unha URL de acceso dun só uso.
func (w *Web) LoginURL() string {
	t := randToken()
	w.mu.Lock()
	w.logins[t] = w.now().Add(loginTTL)
	w.mu.Unlock()
	return w.public.String() + "/login?t=" + t
}

func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+controlPath+"{action}", w.handleControl)
	mux.HandleFunc("GET /login", w.handleLogin)
	mux.HandleFunc("GET /{$}", w.page(w.handleIndex))
	mux.HandleFunc("GET /devices", w.page(w.handleDevices))
	mux.HandleFunc("GET /events", w.handleEvents)
	mux.HandleFunc("GET /static/live.js", serveLiveJS)
	mux.HandleFunc("POST /pair/check", w.form(w.handleCheckLink))
	mux.HandleFunc("POST /pair", w.form(w.handlePair))
	mux.HandleFunc("POST /accept", w.action(func(r *http.Request) (string, error) {
		return "Recurso engadido.", w.app.Accept(r.Context(), r.FormValue("id"), r.FormValue("path"))
	}))
	mux.HandleFunc("POST /unsync", w.action(func(r *http.Request) (string, error) {
		return "Xa non se sincroniza. Os ficheiros locais seguen no seu sitio.", w.app.Unsync(r.Context(), r.FormValue("id"))
	}))
	mux.HandleFunc("POST /pause", w.action(func(r *http.Request) (string, error) {
		d, err := time.ParseDuration(r.FormValue("for"))
		if err != nil || d < 0 || d > 7*24*time.Hour {
			return "", userErr("Duración non válida.")
		}
		return "Sincronización en pausa.", w.app.Pause(r.Context(), d)
	}))
	mux.HandleFunc("POST /resume", w.action(func(r *http.Request) (string, error) {
		return "Sincronización retomada.", w.app.Resume(r.Context())
	}))
	mux.HandleFunc("POST /stop", w.action(func(r *http.Request) (string, error) {
		return "Sincronización detida. Non se sincronizará nada ata que a arranques.", w.app.Stop(r.Context())
	}))
	mux.HandleFunc("POST /start", w.action(func(r *http.Request) (string, error) {
		return "Sincronización arrancada.", w.app.Start(r.Context())
	}))
	mux.HandleFunc("POST /revoke", w.action(func(r *http.Request) (string, error) {
		return "Equipo revogado.", w.app.Revoke(r.Context(), r.FormValue("id"), r.FormValue("otp"))
	}))
	brand.Register(mux)
	return w.guard(mux)
}

// guard aplica as comprobacións comúns a todas as rutas.
func (w *Web) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		h := rw.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		// Non "no-referrer": con esa política o navegador envía "Origin: null"
		// mesmo nos POST á mesma orixe (especificación Fetch) e a
		// comprobación de Origin rexeitaría os formularios propios.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		// A ruta de control vai protexida pola clave (só a coñece quen le
		// ui.json) e chámase pola URL local, non pola pública.
		isControl := strings.HasPrefix(r.URL.Path, controlPath)
		if r.Host != w.public.Host && !isControl {
			http.Error(rw, "host non permitido", http.StatusMisdirectedRequest)
			return
		}
		if r.Method == http.MethodPost && !isControl {
			o, s := r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site")
			if (o != "" && o != w.public.Scheme+"://"+w.public.Host) || (s != "" && s != "same-origin") {
				log.Printf("web: POST %s rexeitado: Origin=%q Sec-Fetch-Site=%q (esperado %s)", r.URL.Path, o, s, w.public.Scheme+"://"+w.public.Host)
				http.Error(rw, "orixe non permitida", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(rw, r)
	})
}

func (w *Web) handleControl(rw http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(controlHdr)), []byte(w.controlKey)) != 1 {
		http.Error(rw, "non autorizado", http.StatusUnauthorized)
		return
	}
	switch r.PathValue("action") {
	case "login-url":
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(map[string]string{"url": w.LoginURL()})
	case "shutdown":
		if w.OnShutdown == nil {
			http.NotFound(rw, r)
			return
		}
		rw.WriteHeader(http.StatusAccepted)
		go w.OnShutdown()
	default:
		http.NotFound(rw, r)
	}
}

func (w *Web) handleLogin(rw http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	w.mu.Lock()
	exp, ok := w.logins[t]
	delete(w.logins, t)
	w.mu.Unlock()
	if !ok || w.now().After(exp) {
		w.render(rw, http.StatusUnauthorized, "noaccess.html", nil)
		return
	}
	sid := randToken()
	w.mu.Lock()
	w.sessions[sid] = w.now().Add(sessionTTL)
	w.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: cookieName, Value: sid, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: w.public.Scheme == "https", MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (w *Web) authed(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, exp := range w.sessions { // limpeza
		if now.After(exp) {
			delete(w.sessions, k)
		}
	}
	for k, exp := range w.logins {
		if now.After(exp) {
			delete(w.logins, k)
		}
	}
	_, ok := w.sessions[c.Value]
	return ok
}

type pageData struct {
	Flash, Error string
	*Overview
	Confirm    *LinkCheck // vinculación: texto comprobado, falta confirmar e o código
	Devices    any
	DeviceErr  string
	ServerHost string
}

func (w *Web) page(h func(*http.Request, *pageData) (string, error)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if !w.authed(r) {
			w.render(rw, http.StatusUnauthorized, "noaccess.html", nil)
			return
		}
		d := &pageData{Flash: r.URL.Query().Get("ok"), Error: r.URL.Query().Get("err")}
		name, err := h(r, d)
		if err != nil {
			log.Printf("web: %v", err)
			d.Error = "Non se puido ler o estado: " + err.Error()
		}
		w.render(rw, http.StatusOK, name, d)
	}
}

func (w *Web) action(h func(*http.Request) (string, error)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if !w.authed(r) {
			w.render(rw, http.StatusUnauthorized, "noaccess.html", nil)
			return
		}
		r.Body = http.MaxBytesReader(rw, r.Body, 16<<10)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		ok, err := h(r.WithContext(ctx))
		back := "/"
		if ref := r.FormValue("back"); ref == "/devices" {
			back = ref
		}
		q := url.Values{}
		var ue *UserError
		switch {
		case errors.As(err, &ue):
			q.Set("err", ue.Msg)
		case err != nil:
			log.Printf("web %s: %v", r.URL.Path, err)
			q.Set("err", "Produciuse un erro: "+err.Error())
		default:
			q.Set("ok", ok)
		}
		http.Redirect(rw, r, back+"?"+q.Encode(), http.StatusSeeOther)
	}
}

// form envolve os POST que responden cunha páxina (non cunha redirección).
func (w *Web) form(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if !w.authed(r) {
			w.render(rw, http.StatusUnauthorized, "noaccess.html", nil)
			return
		}
		r.Body = http.MaxBytesReader(rw, r.Body, 16<<10)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		h(rw, r.WithContext(ctx))
	}
}

func redirectMsg(rw http.ResponseWriter, r *http.Request, key, msg string) {
	http.Redirect(rw, r, "/?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
}

func errMsg(path string, err error) string {
	var ue *UserError
	if errors.As(err, &ue) {
		return ue.Msg
	}
	log.Printf("web %s: %v", path, err)
	return "Produciuse un erro: " + err.Error()
}

// Paso 1 da vinculación: o usuario pegou o texto de vinculación. Se é
// válido, pídeselle confirmación (servidor e usuario) e o código.
func (w *Web) handleCheckLink(rw http.ResponseWriter, r *http.Request) {
	c, err := w.app.CheckLink(r.Context(), r.FormValue("linktext"))
	if err != nil {
		redirectMsg(rw, r, "err", errMsg(r.URL.Path, err))
		return
	}
	w.renderConfirm(rw, r, c, "")
}

// Paso 2: confirmado o servidor, vincular co código. Se falla (p. ex. código
// incorrecto), vólvese á confirmación sen ter que pegar de novo o texto.
func (w *Web) handlePair(rw http.ResponseWriter, r *http.Request) {
	text := r.FormValue("linktext")
	relink := w.app.snapshotState().paired()
	err := w.app.Pair(r.Context(), text, r.FormValue("otp"), r.FormValue("device"))
	switch {
	case err == nil && relink:
		http.Redirect(rw, r, "/devices?"+url.Values{"ok": {"Conexión co servidor actualizada."}}.Encode(), http.StatusSeeOther)
		return
	case err == nil:
		redirectMsg(rw, r, "ok", "Equipo vinculado. Escolle que recursos queres sincronizar.")
		return
	}
	msg := errMsg(r.URL.Path, err)
	c, cerr := w.app.CheckLink(r.Context(), text)
	if cerr != nil {
		redirectMsg(rw, r, "err", msg)
		return
	}
	w.renderConfirm(rw, r, c, msg)
}

func (w *Web) renderConfirm(rw http.ResponseWriter, r *http.Request, c *LinkCheck, errMsg string) {
	ov, err := w.app.Overview(r.Context())
	if err != nil || ov.Stopped || ov.Starting {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	w.render(rw, http.StatusOK, "index.html", &pageData{Overview: ov, Confirm: c, Error: errMsg})
}

func (w *Web) handleIndex(r *http.Request, d *pageData) (string, error) {
	ov, err := w.app.Overview(r.Context())
	if ov == nil {
		ov = &Overview{Paired: true}
	}
	d.Overview = ov
	return "index.html", err
}

func (w *Web) handleDevices(r *http.Request, d *pageData) (string, error) {
	if w.app.snapshotState().paired() {
		devs, err := w.app.Devices(r.Context())
		if err != nil {
			d.DeviceErr = err.Error()
		}
		d.Devices = devs
	}
	ov, err := w.app.Overview(r.Context())
	if err != nil {
		return "devices.html", err
	}
	d.Overview = ov
	if ov.Paired {
		d.ServerHost = hostport(w.app.snapshotState().PairsrvURL)
	}
	return "devices.html", nil
}

func hostport(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Host
}

func (w *Web) render(rw http.ResponseWriter, code int, name string, data any) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(code)
	if err := tmpl.ExecuteTemplate(rw, name, data); err != nil {
		log.Printf("web: plantilla %s: %v", name, err)
	}
}

// humanBytes formata un tamaño ao estilo galego: "1,5 MiB".
func humanBytes(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f, i := float64(n), 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	s := strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
	return strings.Replace(s, ".", ",", 1) + " " + units[i]
}
