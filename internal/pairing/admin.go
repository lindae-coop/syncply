package pairing

// Panel de administración (/admin/): xestionar usuarios, grupos e a súa
// pertenza sen entrar en SFTPGo. O administrador entra coas súas credenciais
// de SFTPGo e cada operación faise coa API de SFTPGo co seu token (ver
// internal/sftpgo/panel.go), así que valen os seus permisos e validacións.
//
// Seguridade: igual que a web dos usuarios (guard: CSP, comprobación de
// orixe nos POST, no-store) e cookie propia con Path=/admin/, HttpOnly,
// SameSite=Strict e Secure. A sesión dura como moito o que o token de
// SFTPGo (20 min en 2.7.6) e caduca antes se non se usa.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	ErrExists     = errors.New("xa existe un grupo ou cartafol con ese nome")
	ErrNoSuch     = errors.New("non existe")
	ErrGroupInUse = errors.New("o grupo aínda ten membros; quítaos antes")
)

// AdminBackend inicia sesións de administración (SFTPGo).
type AdminBackend interface {
	// Login: credenciais incorrectas → ErrBadLogin.
	Login(ctx context.Context, user, password, otp string) (AdminSession, error)
}

// AdminSession son as operacións dun administrador autenticado.
type AdminSession interface {
	Expires() time.Time
	Users(ctx context.Context) ([]AdminUser, error)
	Groups(ctx context.Context) ([]AdminGroup, error)
	// CreateUser crea un usuario sen carpeta persoal, membro de groups.
	CreateUser(ctx context.Context, name, password string, groups []string) error
	SetPassword(ctx context.Context, name, password string) error
	// SetGroups deixa groups como grupos secundarios do usuario.
	SetGroups(ctx context.Context, name string, groups []string) error
	DeleteUser(ctx context.Context, name string) error
	// CreateGroup crea un grupo cun único recurso compartido co seu nome.
	CreateGroup(ctx context.Context, name, description string) error
	DeleteGroup(ctx context.Context, name string) error
}

type AdminUser struct {
	Name      string
	Enabled   bool
	Primary   string   // grupo primario
	Groups    []string // grupos secundarios (os que xestiona o panel)
	Other     []string // grupos de pertenza (non achegan recursos)
	LastLogin time.Time
}

type AdminGroup struct {
	Name        string
	Description string
	Folders     []AdminFolder
	Members     []string
	Managed     bool // creado polo panel (un único recurso co mesmo nome)
}

type AdminFolder struct {
	Name        string
	VirtualPath string
	Path        string
}

// UserSync é o que ve pairsrv dun usuario: se pode sincronizar, os recursos
// que se lle ofrecen e cantos equipos ten vinculados.
type UserSync struct {
	Allowed bool
	Folders []string // etiquetas
	Devices int
}

// SyncOverview resume a sincronización de todos os usuarios para o panel.
func (s *Service) SyncOverview(ctx context.Context) (map[string]UserSync, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]UserSync{}
	for name, us := range s.state.Users {
		out[name] = UserSync{Devices: len(us.Devices)}
	}
	for _, u := range snap.Users {
		o := out[u.Name]
		o.Allowed = true
		for _, id := range u.Folders {
			if f := snap.folder(id); f != nil {
				o.Folders = append(o.Folders, f.Label)
			}
		}
		out[u.Name] = o
	}
	return out, nil
}

// ForgetUser borra o rexistro de pairsrv dun usuario eliminado (equipos e
// segredo do código de vinculación): se despois se crea outro usuario co
// mesmo nome, non herda os seus equipos.
func (s *Service) ForgetUser(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Users[name]; !ok {
		return nil
	}
	delete(s.state.Users, name)
	if err := s.state.save(s.statePath); err != nil {
		return err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return err
	}
	return s.reconcileLocked(ctx, snap)
}

const (
	adminCookie     = "pairsrv_admin"
	adminPath       = "/admin/"
	minPasswordLen  = 8
	maxDescription  = 200
	adminLoginLabel = "admin:" // prefixo das claves de bloqueo por intentos
)

// Nomes de usuarios e grupos: ASCII sen espazos (tamén van en rutas do disco).
var adminNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type adminSession struct {
	admin   string
	api     AdminSession
	expires time.Time // inactividade
	flash   string
	flashOK bool
}

type adminPage struct {
	Admin   string
	User    string // formulario de entrada
	Error   string
	Flash   string
	FlashOK bool
	Minutes int // minutos ata que caduque o acceso a SFTPGo (0: sen dato)
	Users   []adminUserRow
	Groups  []AdminGroup
	// Grupos que se poden asignar (secundarios).
	GroupNames []string
}

type adminUserRow struct {
	AdminUser
	Sync UserSync
	// Grupos asignables con marca de pertenza.
	Choices []groupChoice
}

type groupChoice struct {
	Name    string
	Checked bool
	Fixed   bool // primario ou de pertenza: non se cambia aquí
}

func (w *Web) adminHandler(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/{$}", w.adminIndex)
	mux.HandleFunc("GET /admin", func(rw http.ResponseWriter, r *http.Request) {
		http.Redirect(rw, r, adminPath, http.StatusMovedPermanently)
	})
	mux.HandleFunc("POST /admin/login", w.adminLogin)
	mux.HandleFunc("POST /admin/logout", func(rw http.ResponseWriter, r *http.Request) {
		w.dropAdmin(rw, r)
		http.Redirect(rw, r, adminPath, http.StatusSeeOther)
	})
	mux.HandleFunc("POST /admin/users", w.adminAction(w.adminCreateUser))
	mux.HandleFunc("POST /admin/users/{name}/password", w.adminAction(w.adminSetPassword))
	mux.HandleFunc("POST /admin/users/{name}/groups", w.adminAction(w.adminSetGroups))
	mux.HandleFunc("POST /admin/users/{name}/delete", w.adminAction(w.adminDeleteUser))
	mux.HandleFunc("POST /admin/groups", w.adminAction(w.adminCreateGroup))
	mux.HandleFunc("POST /admin/groups/{name}/delete", w.adminAction(w.adminDeleteGroup))
}

func (w *Web) adminIndex(rw http.ResponseWriter, r *http.Request) {
	s := w.adminSession(r)
	if s == nil {
		w.render(rw, http.StatusOK, "admin_login", &adminPage{})
		return
	}
	ctx := r.Context()
	d := &adminPage{Admin: s.admin}
	if exp := s.api.Expires(); !exp.IsZero() {
		d.Minutes = max(1, int(exp.Sub(w.now())/time.Minute))
	}
	w.mu.Lock()
	d.Flash, d.FlashOK, s.flash = s.flash, s.flashOK, ""
	w.mu.Unlock()

	users, err := s.api.Users(ctx)
	var groups []AdminGroup
	if err == nil {
		groups, err = s.api.Groups(ctx)
	}
	var sync map[string]UserSync
	if err == nil {
		sync, err = w.svc.SyncOverview(ctx)
	}
	if err != nil {
		if w.adminExpired(rw, r, err) {
			return
		}
		log.Printf("admin %s: %v", s.admin, err)
		d.Error = "Non se puido ler a información de SFTPGo: " + publicMessage(err)
		w.render(rw, http.StatusBadGateway, "admin", d)
		return
	}
	d.Groups = groups
	for _, g := range groups {
		d.GroupNames = append(d.GroupNames, g.Name)
	}
	for _, u := range users {
		row := adminUserRow{AdminUser: u, Sync: sync[u.Name]}
		for _, g := range groups {
			c := groupChoice{Name: g.Name, Checked: slices.Contains(u.Groups, g.Name)}
			if g.Name == u.Primary || slices.Contains(u.Other, g.Name) {
				c.Checked, c.Fixed = true, true
			}
			row.Choices = append(row.Choices, c)
		}
		d.Users = append(d.Users, row)
	}
	w.render(rw, http.StatusOK, "admin", d)
}

func (w *Web) adminLogin(rw http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(rw, r.Body, maxBody)
	user := strings.TrimSpace(r.PostFormValue("user"))
	pass := r.PostFormValue("password")
	otp := strings.TrimSpace(r.PostFormValue("otp"))
	if user == "" || pass == "" || len(user) > maxLoginField || len(pass) > maxLoginField || len(otp) > 16 {
		w.render(rw, http.StatusBadRequest, "admin_login", &adminPage{User: user, Error: "Indica usuario e contrasinal."})
		return
	}
	key := adminLoginLabel + user
	if w.locked(key) {
		w.render(rw, http.StatusTooManyRequests, "admin_login", &adminPage{User: user, Error: ErrLocked.Error() + "."})
		return
	}
	api, err := w.admin.Login(r.Context(), user, pass, otp)
	if errors.Is(err, ErrBadLogin) {
		w.failed(key)
		log.Printf("admin: acceso rexeitado para %q", user)
		w.render(rw, http.StatusUnauthorized, "admin_login", &adminPage{User: user,
			Error: "Usuario ou contrasinal de administrador incorrecto (ou falta o código de segundo factor)."})
		return
	}
	if err != nil {
		log.Printf("admin: login %q: %v", user, err)
		w.render(rw, http.StatusBadGateway, "admin_login", &adminPage{User: user, Error: "Non se puido contactar con SFTPGo; proba de novo."})
		return
	}
	tok, err := randToken()
	if err != nil {
		http.Error(rw, "erro interno", http.StatusInternalServerError)
		return
	}
	w.mu.Lock()
	delete(w.fails, key)
	w.admins[tok] = &adminSession{admin: user, api: api, expires: w.now().Add(sessionIdle)}
	w.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: adminCookie, Value: tok, Path: adminPath,
		HttpOnly: true, Secure: secureCookies(), SameSite: http.SameSiteStrictMode})
	log.Printf("admin: sesión iniciada por %q", user)
	http.Redirect(rw, r, adminPath, http.StatusSeeOther)
}

// adminSession devolve a sesión de administración válida (renovando a
// caducidade por inactividade) ou nil.
func (w *Web) adminSession(r *http.Request) *adminSession {
	c, err := r.Cookie(adminCookie)
	if err != nil {
		return nil
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, s := range w.admins {
		if now.After(s.expires) || (!s.api.Expires().IsZero() && now.After(s.api.Expires())) {
			delete(w.admins, k)
		}
	}
	s := w.admins[c.Value]
	if s == nil {
		return nil
	}
	s.expires = now.Add(sessionIdle)
	return s
}

func (w *Web) dropAdmin(rw http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookie); err == nil {
		w.mu.Lock()
		delete(w.admins, c.Value)
		w.mu.Unlock()
	}
	http.SetCookie(rw, &http.Cookie{Name: adminCookie, Value: "", Path: adminPath, MaxAge: -1,
		HttpOnly: true, Secure: secureCookies(), SameSite: http.SameSiteStrictMode})
}

// adminExpired: se SFTPGo xa non acepta o token, pecha a sesión e volve ao
// formulario de entrada.
func (w *Web) adminExpired(rw http.ResponseWriter, r *http.Request, err error) bool {
	var sc interface{ StatusCode() int }
	if !errors.As(err, &sc) || sc.StatusCode() != http.StatusUnauthorized {
		return false
	}
	w.dropAdmin(rw, r)
	w.render(rw, http.StatusUnauthorized, "admin_login", &adminPage{Error: "A sesión caducou; entra de novo."})
	return true
}

// adminAction executa unha operación do panel e volve á páxina cunha mensaxe.
func (w *Web) adminAction(f func(ctx context.Context, s *adminSession, r *http.Request) (string, error)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		s := w.adminSession(r)
		if s == nil {
			http.Redirect(rw, r, adminPath, http.StatusSeeOther)
			return
		}
		r.Body = http.MaxBytesReader(rw, r.Body, maxBody)
		if err := r.ParseForm(); err != nil {
			http.Error(rw, "petición non válida", http.StatusBadRequest)
			return
		}
		msg, err := f(r.Context(), s, r)
		if err != nil {
			if w.adminExpired(rw, r, err) {
				return
			}
			log.Printf("admin %s: %s %s: %v", s.admin, r.Method, r.URL.Path, err)
			msg = publicMessage(err)
		} else {
			log.Printf("admin %s: %s %s: OK", s.admin, r.Method, r.URL.Path)
			w.svc.RequestReconcile()
		}
		w.mu.Lock()
		s.flash, s.flashOK = msg, err == nil
		w.mu.Unlock()
		http.Redirect(rw, r, adminPath, http.StatusSeeOther)
	}
}

// publicMessage é o texto dun erro que se pode mostrar no panel.
func publicMessage(err error) string {
	for _, e := range []error{ErrBadRequest, ErrExists, ErrNoSuch, ErrGroupInUse} {
		if errors.Is(err, e) {
			return strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + "."
		}
	}
	var pm interface{ PublicMessage() string }
	if errors.As(err, &pm) {
		if m := pm.PublicMessage(); m != "" && m != ":" {
			return "SFTPGo: " + m
		}
	}
	return "Erro na operación; mira o rexistro de pairsrv."
}

func badRequest(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadRequest}, a...)...)
}

func checkName(kind, name string) error {
	if !adminNameRe.MatchString(name) {
		return badRequest("%s non válido %q (letras, números, «.», «_» e «-»; ata 64 caracteres)", kind, name)
	}
	return nil
}

func checkPassword(p, again string) error {
	if len([]rune(p)) < minPasswordLen || len(p) > maxLoginField {
		return badRequest("o contrasinal ten que ter polo menos %d caracteres", minPasswordLen)
	}
	if p != again {
		return badRequest("os contrasinais non coinciden")
	}
	return nil
}

// formGroups: grupos marcados no formulario, que teñen que existir.
func formGroups(ctx context.Context, s *adminSession, f url.Values) ([]string, error) {
	sel := f["group"]
	if len(sel) == 0 {
		return nil, nil
	}
	groups, err := s.api.Groups(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range sel {
		if !slices.ContainsFunc(groups, func(g AdminGroup) bool { return g.Name == name }) {
			return nil, badRequest("grupo descoñecido %q", name)
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

func (w *Web) adminCreateUser(ctx context.Context, s *adminSession, r *http.Request) (string, error) {
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if err := checkName("nome de usuario", name); err != nil {
		return "", err
	}
	pass := r.PostForm.Get("password")
	if err := checkPassword(pass, r.PostForm.Get("password2")); err != nil {
		return "", err
	}
	groups, err := formGroups(ctx, s, r.PostForm)
	if err != nil {
		return "", err
	}
	users, err := s.api.Users(ctx)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(users, func(u AdminUser) bool { return strings.EqualFold(u.Name, name) }) {
		return "", badRequest("xa existe o usuario %q", name)
	}
	// Por se houbo antes un usuario co mesmo nome borrado fóra do panel: que
	// o novo non herde os seus equipos.
	if err := w.svc.ForgetUser(ctx, name); err != nil {
		return "", err
	}
	if err := s.api.CreateUser(ctx, name, pass, groups); err != nil {
		return "", err
	}
	return fmt.Sprintf("Creado o usuario %s.", name), nil
}

func (w *Web) adminSetPassword(ctx context.Context, s *adminSession, r *http.Request) (string, error) {
	name := r.PathValue("name")
	pass := r.PostForm.Get("password")
	if err := checkPassword(pass, r.PostForm.Get("password2")); err != nil {
		return "", err
	}
	if err := s.api.SetPassword(ctx, name, pass); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cambiado o contrasinal de %s.", name), nil
}

func (w *Web) adminSetGroups(ctx context.Context, s *adminSession, r *http.Request) (string, error) {
	name := r.PathValue("name")
	groups, err := formGroups(ctx, s, r.PostForm)
	if err != nil {
		return "", err
	}
	if err := s.api.SetGroups(ctx, name, groups); err != nil {
		return "", err
	}
	return fmt.Sprintf("Gardados os grupos de %s.", name), nil
}

func (w *Web) adminDeleteUser(ctx context.Context, s *adminSession, r *http.Request) (string, error) {
	name := r.PathValue("name")
	if r.PostForm.Get("confirm") != name {
		return "", badRequest("para borrar o usuario escribe o seu nome")
	}
	if err := s.api.DeleteUser(ctx, name); err != nil {
		return "", err
	}
	if err := w.svc.ForgetUser(ctx, name); err != nil {
		return "", err
	}
	return fmt.Sprintf("Borrado o usuario %s; os seus equipos xa non sincronizan. Os ficheiros quedan no disco.", name), nil
}

func (w *Web) adminCreateGroup(ctx context.Context, s *adminSession, r *http.Request) (string, error) {
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if err := checkName("nome de grupo", name); err != nil {
		return "", err
	}
	desc := strings.TrimSpace(r.PostForm.Get("description"))
	if len([]rune(desc)) > maxDescription {
		return "", badRequest("descrición demasiado longa")
	}
	if err := s.api.CreateGroup(ctx, name, desc); err != nil {
		return "", err
	}
	return fmt.Sprintf("Creado o grupo %s co seu recurso compartido.", name), nil
}

func (w *Web) adminDeleteGroup(ctx context.Context, s *adminSession, r *http.Request) (string, error) {
	name := r.PathValue("name")
	if r.PostForm.Get("confirm") != name {
		return "", badRequest("para borrar o grupo escribe o seu nome")
	}
	if err := s.api.DeleteGroup(ctx, name); err != nil {
		return "", err
	}
	return fmt.Sprintf("Borrado o grupo %s. Os ficheiros do recurso quedan no disco.", name), nil
}
