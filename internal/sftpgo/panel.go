package sftpgo

// Operacións do panel de administración de pairsrv (pairing.AdminBackend):
// usuarios, grupos con un recurso compartido e pertenza aos grupos. Todo vai
// pola API REST de SFTPGo co token do administrador que entra no panel, así
// que se aplican os seus permisos e as validacións de SFTPGo.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"time"

	"github.com/lindae-coop/syncply/internal/pairing"
)

// PanelConfig: onde se crean no disco os directorios do panel. Teñen que
// estar no disco que comparten SFTPGo e o Syncthing do servidor.
type PanelConfig struct {
	APIURL string // URL base de SFTPGo (Config.APIURL)
	// Directorio persoal (sen permisos de escritura) dos usuarios creados no
	// panel: <HomesDir>/<usuario>.
	HomesDir string
	// Recurso de cada grupo creado no panel: <GroupsDir>/<grupo>.
	GroupsDir string
}

// Panel implementa pairing.AdminBackend.
type Panel struct{ cfg PanelConfig }

func NewPanel(cfg PanelConfig) (*Panel, error) {
	if cfg.APIURL == "" {
		return nil, errors.New("sftpgo: falta apiURL para o panel de administración")
	}
	for _, d := range []string{cfg.HomesDir, cfg.GroupsDir} {
		if !path.IsAbs(d) || path.Clean(d) != d || d == "/" {
			return nil, fmt.Errorf("panel: ruta non válida %q (ten que ser absoluta e limpa)", d)
		}
	}
	return &Panel{cfg: cfg}, nil
}

// Login inicia sesión en SFTPGo como administrador. Credenciais incorrectas
// → pairing.ErrBadLogin.
func (p *Panel) Login(ctx context.Context, user, password, otp string) (pairing.AdminSession, error) {
	a, exp, err := newAdmin(ctx, p.cfg.APIURL, user, password, otp)
	if IsStatus(err, http.StatusUnauthorized) || IsStatus(err, http.StatusForbidden) {
		return nil, pairing.ErrBadLogin
	}
	if err != nil {
		return nil, err
	}
	return &panelSession{cfg: p.cfg, a: a, expires: exp}, nil
}

type panelSession struct {
	cfg     PanelConfig
	a       *Admin
	expires time.Time
}

func (s *panelSession) Expires() time.Time { return s.expires }

// Obxectos da API de SFTPGo (só os campos que se usan).
type apiGroupRef struct {
	Name string `json:"name"`
	Type int    `json:"type"`
}

type apiUser struct {
	Username    string              `json:"username"`
	Status      int                 `json:"status"`
	HomeDir     string              `json:"home_dir"`
	Permissions map[string][]string `json:"permissions"`
	Groups      []apiGroupRef       `json:"groups"`
	LastLogin   int64               `json:"last_login"` // ms
}

type apiGroup struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Users          []string `json:"users"`
	VirtualFolders []struct {
		Name        string `json:"name"`
		VirtualPath string `json:"virtual_path"`
		MappedPath  string `json:"mapped_path"`
	} `json:"virtual_folders"`
}

// list le todas as páxinas dun listado da API (máximo 500 por páxina).
func list[T any](ctx context.Context, a *Admin, kind string) ([]T, error) {
	const page = 500
	var all []T
	for off := 0; ; off += page {
		var items []T
		if err := a.Call(ctx, http.MethodGet, fmt.Sprintf("/%s?limit=%d&offset=%d&order=ASC", kind, page, off), nil, &items); err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < page {
			return all, nil
		}
	}
}

func (s *panelSession) Users(ctx context.Context) ([]pairing.AdminUser, error) {
	users, err := list[apiUser](ctx, s.a, "users")
	if err != nil {
		return nil, err
	}
	out := make([]pairing.AdminUser, 0, len(users))
	for _, u := range users {
		au := pairing.AdminUser{Name: u.Username, Enabled: u.Status == 1}
		if u.LastLogin > 0 {
			au.LastLogin = time.UnixMilli(u.LastLogin)
		}
		for _, g := range u.Groups {
			switch g.Type {
			case groupPrimary:
				au.Primary = g.Name
			case groupSecondary:
				au.Groups = append(au.Groups, g.Name)
			default:
				au.Other = append(au.Other, g.Name)
			}
		}
		slices.Sort(au.Groups)
		out = append(out, au)
	}
	return out, nil
}

func (s *panelSession) Groups(ctx context.Context) ([]pairing.AdminGroup, error) {
	groups, err := list[apiGroup](ctx, s.a, "groups")
	if err != nil {
		return nil, err
	}
	var out []pairing.AdminGroup
	for _, g := range groups {
		if g.Name == SyncthingGroup {
			continue
		}
		ag := pairing.AdminGroup{Name: g.Name, Description: g.Description, Members: slices.Sorted(slices.Values(g.Users))}
		for _, f := range g.VirtualFolders {
			ag.Folders = append(ag.Folders, pairing.AdminFolder{Name: f.Name, VirtualPath: f.VirtualPath, Path: f.MappedPath})
		}
		ag.Managed = managedGroup(&ag)
		out = append(out, ag)
	}
	return out, nil
}

// managedGroup: grupo co formato dos que crea o panel (un único cartafol co
// mesmo nome montado en /<nome>). Só eses se poden borrar desde o panel.
func managedGroup(g *pairing.AdminGroup) bool {
	return len(g.Folders) == 1 && g.Folders[0].Name == g.Name && g.Folders[0].VirtualPath == "/"+g.Name
}

func (s *panelSession) hasGroup(ctx context.Context, name string) (bool, error) {
	err := s.a.Call(ctx, http.MethodGet, "/groups/"+url.PathEscape(name), nil, nil)
	if IsStatus(err, http.StatusNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CreateUser crea un usuario sen carpeta persoal sincronizable: o seu
// directorio persoal só ten permisos de lectura (pairsrv non o ofrece) e os
// recursos chégalle dos grupos. O grupo primario é SyncthingGroup (oculta os
// ficheiros internos de Syncthing) se existe.
func (s *panelSession) CreateUser(ctx context.Context, name, password string, groups []string) error {
	refs := []apiGroupRef{}
	ok, err := s.hasGroup(ctx, SyncthingGroup)
	if err != nil {
		return err
	}
	if ok {
		refs = append(refs, apiGroupRef{Name: SyncthingGroup, Type: groupPrimary})
	}
	for _, g := range groups {
		refs = append(refs, apiGroupRef{Name: g, Type: groupSecondary})
	}
	return s.a.Call(ctx, http.MethodPost, "/users", map[string]any{
		"username":    name,
		"status":      1,
		"password":    password,
		"home_dir":    path.Join(s.cfg.HomesDir, name),
		"permissions": map[string][]string{"/": {"list", "download"}},
		"groups":      refs,
		"filesystem":  map[string]any{"provider": localProvider},
		"description": "Creado no panel de Syncply",
	}, nil)
}

// updateUser le o usuario completo, aplícalle change e gárdao (a API de
// SFTPGo só ten PUT do obxecto enteiro; sen "password" mantén o actual).
func (s *panelSession) updateUser(ctx context.Context, name string, change func(map[string]any) error) error {
	p := "/users/" + url.PathEscape(name)
	var u map[string]any
	if err := s.a.Call(ctx, http.MethodGet, p, nil, &u); err != nil {
		return err
	}
	if err := change(u); err != nil {
		return err
	}
	return s.a.Call(ctx, http.MethodPut, p, u, nil)
}

func (s *panelSession) SetPassword(ctx context.Context, name, password string) error {
	return s.updateUser(ctx, name, func(u map[string]any) error {
		u["password"] = password
		return nil
	})
}

// SetGroups deixa groups como grupos secundarios do usuario. Os primarios e
// os de pertenza non se tocan.
func (s *panelSession) SetGroups(ctx context.Context, name string, groups []string) error {
	return s.updateUser(ctx, name, func(u map[string]any) error {
		cur, _ := u["groups"].([]any)
		keep := []any{}
		other := map[string]bool{}
		for _, g := range cur {
			m, _ := g.(map[string]any)
			typ, _ := m["type"].(float64)
			gname, _ := m["name"].(string)
			if int(typ) != groupSecondary {
				keep = append(keep, g)
				other[gname] = true
			}
		}
		for _, g := range groups {
			if !other[g] {
				keep = append(keep, map[string]any{"name": g, "type": groupSecondary})
			}
		}
		u["groups"] = keep
		return nil
	})
}

func (s *panelSession) DeleteUser(ctx context.Context, name string) error {
	return s.a.Call(ctx, http.MethodDelete, "/users/"+url.PathEscape(name), nil, nil)
}

// CreateGroup crea un cartafol virtual co nome do grupo en
// <GroupsDir>/<nome> e o grupo, que o monta en /<nome> con todos os
// permisos: os membros recíbeno como recurso sincronizable.
func (s *panelSession) CreateGroup(ctx context.Context, name, description string) error {
	if name == SyncthingGroup {
		return fmt.Errorf("%w: %q está reservado", pairing.ErrBadRequest, name)
	}
	for _, kind := range []string{"groups", "folders"} {
		err := s.a.Call(ctx, http.MethodGet, "/"+kind+"/"+url.PathEscape(name), nil, nil)
		if err == nil {
			return pairing.ErrExists
		}
		if !IsStatus(err, http.StatusNotFound) {
			return err
		}
	}
	if err := s.a.Call(ctx, http.MethodPost, "/folders", map[string]any{
		"name":        name,
		"mapped_path": path.Join(s.cfg.GroupsDir, name),
		"filesystem":  map[string]any{"provider": localProvider},
		"description": "Recurso do grupo " + name + " (panel de Syncply)",
	}, nil); err != nil {
		return err
	}
	vp := "/" + name
	err := s.a.Call(ctx, http.MethodPost, "/groups", map[string]any{
		"name":        name,
		"description": description,
		// Sen virtual_path o cartafol non se garda no grupo (visto en 2.7.6).
		"virtual_folders": []map[string]any{{"name": name, "virtual_path": vp, "quota_size": -1, "quota_files": -1}},
		"user_settings":   map[string]any{"permissions": map[string][]string{vp: {"*"}}},
	}, nil)
	if err != nil {
		// Non deixar o cartafol solto.
		_ = s.a.Call(context.WithoutCancel(ctx), http.MethodDelete, "/folders/"+url.PathEscape(name), nil, nil)
	}
	return err
}

// DeleteGroup borra un grupo creado no panel e o seu cartafol virtual (os
// ficheiros quedan no disco). Só se o grupo non ten membros.
func (s *panelSession) DeleteGroup(ctx context.Context, name string) error {
	var g apiGroup
	if err := s.a.Call(ctx, http.MethodGet, "/groups/"+url.PathEscape(name), nil, &g); err != nil {
		if IsStatus(err, http.StatusNotFound) {
			return pairing.ErrNoSuch
		}
		return err
	}
	ag := pairing.AdminGroup{Name: g.Name}
	for _, f := range g.VirtualFolders {
		ag.Folders = append(ag.Folders, pairing.AdminFolder{Name: f.Name, VirtualPath: f.VirtualPath})
	}
	if !managedGroup(&ag) || name == SyncthingGroup {
		return fmt.Errorf("%w: o grupo non o creou o panel; bórrao en SFTPGo", pairing.ErrBadRequest)
	}
	if len(g.Users) > 0 {
		return pairing.ErrGroupInUse
	}
	if err := s.a.Call(ctx, http.MethodDelete, "/groups/"+url.PathEscape(name), nil, nil); err != nil {
		return err
	}
	err := s.a.Call(ctx, http.MethodDelete, "/folders/"+url.PathEscape(name), nil, nil)
	if IsStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}
