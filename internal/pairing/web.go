package pairing

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lindae-coop/syncply/internal/brand"
	"github.com/lindae-coop/syncply/internal/totp"
)

// PasswordChecker comproba as credenciais dun usuario (SFTPGo). otp é o
// segundo factor propio de SFTPGo, se o usuario o ten activado.
type PasswordChecker interface {
	CheckPassword(ctx context.Context, user, password, otp string) error
}

// WebOptions configura a web na que o usuario ve o seu código de vinculación.
type WebOptions struct {
	// Ligazóns de descarga do cliente.
	Downloads []Download `json:"downloads"`
	// URL base coa que os clientes chegan a pairsrv (a do proxy); vai no
	// texto de vinculación. Obrigatoria; https agás en desenvolvemento.
	PublicURL string `json:"publicURL"`
}

func (o *WebOptions) validate() error {
	if o.PublicURL == "" {
		return errors.New("web.publicURL: falta (p. ex. https://nube.example.org)")
	}
	if err := CheckPublicURL(o.PublicURL); err != nil {
		return fmt.Errorf("web.publicURL %q: %w", o.PublicURL, err)
	}
	o.PublicURL = strings.TrimRight(o.PublicURL, "/")
	return nil
}

// linkText xera o texto de vinculación do usuario.
func (w *Web) linkText(user string) string {
	return LinkData{URL: w.opts.PublicURL, User: user, Server: w.svc.ServerID()}.Encode()
}

// secureCookies: a web vai por HTTPS (detrás do proxy) agás en desenvolvemento.
func secureCookies() bool { return !DevMode() }

type Download struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

const (
	sessionCookie   = "pairsrv_session"
	sessionIdle     = 15 * time.Minute
	loginMaxFails   = 5
	loginLockFor    = 5 * time.Minute
	maxLoginField   = 256
	refreshMinDelay = 2 * time.Second
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/copy.js
var copyJS []byte

var webTmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"date": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
}).ParseFS(templatesFS, "templates/*.html"))

// Web é a páxina do servidor na que o usuario, identificado co seu usuario e
// contrasinal de SFTPGo, ve o código de vinculación (cambia cada 30 s), o
// texto de vinculación, os recursos que se lle ofrecen, os seus equipos e a
// descarga do cliente.
//
// Seguridade: sesión en memoria con cookie HttpOnly, SameSite=Strict e
// Secure (agás en desenvolvemento); comprobación de Origin/Sec-Fetch-Site
// nos POST; CSP que só permite o script propio do botón Copiar (a páxina
// funciona sen el); Cache-Control: no-store (a páxina leva o código);
// bloqueo temporal tras varios contrasinais incorrectos para o mesmo usuario.
type Web struct {
	svc  *Service
	auth PasswordChecker
	opts WebOptions
	now  func() time.Time

	admin AdminBackend // nil: sen panel de administración

	mu       sync.Mutex
	sessions map[string]*session
	admins   map[string]*adminSession
	fails    map[string]*loginFails
}

type session struct {
	user    string
	expires time.Time
}

type loginFails struct {
	n           int
	lockedUntil time.Time
}

func NewWeb(svc *Service, auth PasswordChecker, opts WebOptions) (*Web, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &Web{svc: svc, auth: auth, opts: opts, now: time.Now,
		sessions: map[string]*session{}, admins: map[string]*adminSession{}, fails: map[string]*loginFails{}}, nil
}

// EnableAdmin activa o panel de administración en /admin/. Hai que chamala
// antes de Handler.
func (w *Web) EnableAdmin(b AdminBackend) { w.admin = b }

func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.handleIndex)
	mux.HandleFunc("POST /login", w.handleLogin)
	mux.HandleFunc("POST /logout", w.handleLogout)
	mux.HandleFunc("GET /static/copy.js", func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		rw.Write(copyJS)
	})
	if w.admin != nil {
		w.adminHandler(mux)
	}
	brand.Register(mux)
	return w.guard(mux)
}

func (w *Web) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		h := rw.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Non "no-referrer": o navegador enviaría "Origin: null" nos POST.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost && !sameOrigin(r) {
			log.Printf("web: POST %s rexeitado: Origin=%q Sec-Fetch-Site=%q Host=%q",
				r.URL.Path, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"), r.Host)
			http.Error(rw, "orixe non permitida", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// sameOrigin acepta POST sen cabeceiras de orixe (clientes que non son
// navegadores) ou da mesma orixe. Detrás dun proxy, este debe conservar a
// cabeceira Host.
func sameOrigin(r *http.Request) bool {
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

type pageData struct {
	User      string
	Error     string
	Code      *Code
	CodeLeft  int // segundos ata o seguinte código
	CodeShare int // porcentaxe de tempo restante (0-100)
	Account   *Account
	Downloads []Download
	LinkText  string // texto de vinculación para pegar no cliente
	AdminLink bool   // hai panel de administración
}

func (w *Web) handleIndex(rw http.ResponseWriter, r *http.Request) {
	user := w.sessionUser(r)
	if user == "" {
		w.render(rw, http.StatusOK, "login", &pageData{})
		return
	}
	ctx := r.Context()
	code, err := w.svc.PairingCode(ctx, user)
	var acc *Account
	if err == nil {
		acc, err = w.svc.Account(ctx, user)
	}
	if errors.Is(err, ErrUnauthorized) {
		// O usuario xa non ten acceso (desactivado en SFTPGo).
		w.dropSession(rw, r)
		w.render(rw, http.StatusOK, "login", &pageData{Error: "A túa conta non ten acceso."})
		return
	}
	if err != nil {
		log.Printf("web: %s: %v", user, err)
		w.render(rw, http.StatusInternalServerError, "login", &pageData{Error: "Erro interno; proba de novo."})
		return
	}
	left := max(code.Until.Sub(w.now()), refreshMinDelay)
	w.render(rw, http.StatusOK, "index", &pageData{
		User:      user,
		Code:      code,
		CodeLeft:  int((left + time.Second - 1) / time.Second),
		CodeShare: min(100, int(100*left/totp.Step)),
		Account:   acc,
		Downloads: w.opts.Downloads,
		LinkText:  w.linkText(user),
	})
}

func (w *Web) handleLogin(rw http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(rw, r.Body, maxBody)
	user := strings.TrimSpace(r.PostFormValue("user"))
	pass := r.PostFormValue("password")
	otp := strings.TrimSpace(r.PostFormValue("otp"))
	if user == "" || pass == "" || len(user) > maxLoginField || len(pass) > maxLoginField || len(otp) > 16 {
		w.render(rw, http.StatusBadRequest, "login", &pageData{User: user, Error: "Indica usuario e contrasinal."})
		return
	}
	if w.locked(user) {
		w.render(rw, http.StatusTooManyRequests, "login", &pageData{User: user, Error: ErrLocked.Error() + "."})
		return
	}
	err := w.auth.CheckPassword(r.Context(), user, pass, otp)
	if errors.Is(err, ErrBadLogin) {
		w.failed(user)
		log.Printf("web: acceso rexeitado para %q", user)
		w.render(rw, http.StatusUnauthorized, "login", &pageData{User: user,
			Error: "Usuario ou contrasinal incorrecto (ou falta o código de segundo factor de SFTPGo)."})
		return
	}
	if err != nil {
		log.Printf("web: login %q: %v", user, err)
		w.render(rw, http.StatusBadGateway, "login", &pageData{User: user, Error: "Non se puido comprobar o contrasinal; proba de novo."})
		return
	}
	tok, err := randToken()
	if err != nil {
		http.Error(rw, "erro interno", http.StatusInternalServerError)
		return
	}
	w.mu.Lock()
	delete(w.fails, user)
	w.sessions[tok] = &session{user: user, expires: w.now().Add(sessionIdle)}
	w.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/",
		HttpOnly: true, Secure: secureCookies(), SameSite: http.SameSiteStrictMode})
	log.Printf("web: sesión iniciada por %q", user)
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (w *Web) handleLogout(rw http.ResponseWriter, r *http.Request) {
	w.dropSession(rw, r)
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

// sessionUser devolve o usuario da sesión (renovando a caducidade) ou "".
func (w *Web) sessionUser(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, s := range w.sessions { // limpeza das caducadas
		if now.After(s.expires) {
			delete(w.sessions, k)
		}
	}
	s := w.sessions[c.Value]
	if s == nil {
		return ""
	}
	s.expires = now.Add(sessionIdle)
	return s.user
}

func (w *Web) dropSession(rw http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		w.mu.Lock()
		delete(w.sessions, c.Value)
		w.mu.Unlock()
	}
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secureCookies(), SameSite: http.SameSiteStrictMode})
}

func (w *Web) locked(user string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	f := w.fails[user]
	return f != nil && w.now().Before(f.lockedUntil)
}

func (w *Web) failed(user string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f := w.fails[user]
	if f == nil {
		f = &loginFails{}
		w.fails[user] = f
	}
	f.n++
	if f.n >= loginMaxFails {
		f.n = 0
		f.lockedUntil = w.now().Add(loginLockFor)
	}
}

func (w *Web) render(rw http.ResponseWriter, code int, name string, d any) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(code)
	if p, ok := d.(*pageData); ok {
		p.AdminLink = w.admin != nil
	}
	if err := webTmpl.ExecuteTemplate(rw, name, d); err != nil {
		log.Printf("web: plantilla %s: %v", name, err)
	}
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
