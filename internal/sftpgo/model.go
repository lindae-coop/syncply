package sftpgo

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Tipos de grupo de SFTPGo (sdk.GroupType*).
const (
	groupPrimary    = 1
	groupSecondary  = 2
	groupMembership = 3
)

// Provedor de sistema de ficheiros local (sdk.LocalFilesystemProvider).
// Syncthing só pode traballar sobre disco local: S3, GCS, Azure, SFTP remoto
// e cifrado (cryptfs) quedan fóra.
const localProvider = 0

type rawFolder struct {
	Name        string
	Path        string // mapped_path
	Provider    int
	VirtualPath string
}

// Restricións de acceso (filters de SFTPGo) que deciden se o usuario pode
// entrar no WebClient.
type accessFilters struct {
	DeniedProtocols    []string `json:"denied_protocols"`
	DeniedLoginMethods []string `json:"denied_login_methods"`
	IsAnonymous        bool     `json:"is_anonymous"`
}

type rawGroup struct {
	Name        string
	Type        int
	HomeDir     string
	Provider    int
	Permissions map[string][]string
	Folders     []rawFolder
	Filters     accessFilters
}

type rawUser struct {
	Username    string
	Role        string
	HomeDir     string
	Provider    int
	Permissions map[string][]string
	Folders     []rawFolder
	Filters     accessFilters
	Groups      []rawGroup // na orde de SFTPGo
}

// effectiveUser é o usuario despois de aplicar os grupos.
type effectiveUser struct {
	HomeDir     string
	Provider    int
	Permissions map[string][]string
	Folders     []rawFolder
	Filters     accessFilters
}

// effective reproduce User.applyGroupSettings de SFTPGo 2.7.6 nas
// propiedades que importan aquí: directorio persoal, sistema de ficheiros,
// cartafoles virtuais, permisos e restricións de acceso. O grupo primario aplícase primeiro e pode
// cambiar o directorio persoal e os permisos de "/"; os secundarios só
// engaden cartafoles e permisos doutras rutas; os de pertenza non achegan nada.
func (u *rawUser) effective() effectiveUser {
	r := strings.NewReplacer("%username%", u.Username, "%role%", u.Role)
	e := effectiveUser{
		HomeDir:     u.HomeDir,
		Provider:    u.Provider,
		Permissions: map[string][]string{},
		Folders:     slices.Clone(u.Folders),
		Filters: accessFilters{
			DeniedProtocols:    slices.Clone(u.Filters.DeniedProtocols),
			DeniedLoginMethods: slices.Clone(u.Filters.DeniedLoginMethods),
			IsAnonymous:        u.Filters.IsAnonymous,
		},
	}
	for k, v := range u.Permissions {
		e.Permissions[k] = v
	}
	merge := func(g *rawGroup, primary bool) {
		have := map[string]bool{}
		for _, f := range e.Folders {
			have[f.VirtualPath] = true
		}
		for _, f := range g.Folders {
			if f.VirtualPath == "/" && !primary {
				continue
			}
			f.VirtualPath = r.Replace(f.VirtualPath)
			if !have[f.VirtualPath] {
				f.Path = r.Replace(f.Path)
				e.Folders = append(e.Folders, f)
				have[f.VirtualPath] = true
			}
		}
		for k, v := range g.Permissions {
			if k == "/" {
				if primary {
					e.Permissions[k] = v
				}
				continue
			}
			k = r.Replace(k)
			if _, ok := e.Permissions[k]; !ok {
				e.Permissions[k] = v
			}
		}
		// mergeAdditiveProperties: as restricións súmanse (primario e secundarios).
		e.Filters.DeniedProtocols = append(e.Filters.DeniedProtocols, g.Filters.DeniedProtocols...)
		e.Filters.DeniedLoginMethods = append(e.Filters.DeniedLoginMethods, g.Filters.DeniedLoginMethods...)
	}
	for i := range u.Groups {
		if g := &u.Groups[i]; g.Type == groupPrimary {
			if g.HomeDir != "" {
				e.HomeDir = filepath.Clean(r.Replace(g.HomeDir))
			}
			if g.Provider != localProvider {
				e.Provider = g.Provider
			}
			merge(g, true)
			break
		}
	}
	for i := range u.Groups {
		if g := &u.Groups[i]; g.Type == groupSecondary {
			merge(g, false)
		}
	}
	return e
}

// webClientAllowed reproduce as comprobacións de checkHTTPClientUser
// (httpd/api_utils.go) para o login con contrasinal: protocolo HTTP permitido
// e método "password" permitido. Os usuarios anónimos teñen HTTP denegado
// (setAnonymousSettings). Decisión do proxecto: quen non pode entrar no
// WebClient non ve o código de vinculación, así que queda fóra de Syncthing.
// Ao integrar en SFTPGo, un usuario que entre só por OIDC tamén contaría.
func (e *effectiveUser) webClientAllowed() bool {
	return !e.Filters.IsAnonymous &&
		!slices.Contains(e.Filters.DeniedProtocols, "HTTP") &&
		!slices.Contains(e.Filters.DeniedLoginMethods, "password")
}

// permissionsFor reproduce User.GetPermissionsForPath de SFTPGo.
func permissionsFor(perms map[string][]string, p string) []string {
	var fallback []string
	if v, ok := perms["/"]; ok {
		if len(perms) == 1 {
			return v
		}
		fallback = v
	}
	for _, dir := range dirsFor(p) {
		if v, ok := perms[dir]; ok {
			return v
		}
		for pattern, v := range perms {
			if ok, err := path.Match(pattern, dir); err == nil && ok {
				return v
			}
		}
	}
	return fallback
}

// dirsFor devolve p e os seus pais ata "/": "/a/b" → ["/a/b", "/a", "/"].
func dirsFor(p string) []string {
	p = path.Clean("/" + p)
	out := []string{p}
	for p != "/" {
		p = path.Dir(p)
		out = append(out, p)
	}
	return out
}

// canWrite: Syncthing escribe sen pasar por SFTPGo, así que se esixe todo o
// que pode facer a sincronización: ler, subir, sobrescribir, borrar,
// renomear e crear directorios.
func canWrite(perms []string) bool {
	has := func(p string) bool { return slices.Contains(perms, p) }
	if has("*") {
		return true
	}
	return has("list") && has("download") && has("upload") && has("overwrite") && has("create_dirs") &&
		(has("delete") || (has("delete_files") && has("delete_dirs"))) &&
		(has("rename") || (has("rename_files") && has("rename_dirs")))
}

// writableTree indica se hai permiso de escritura en todo o recurso montado
// en root: na propia raíz e en calquera ruta con permisos propios dentro del
// (salvo as que pertencen a outros cartafoles virtuais montados dentro).
func writableTree(perms map[string][]string, root string, mounts []string) bool {
	if !canWrite(permissionsFor(perms, root)) {
		return false
	}
	for k, v := range perms {
		dir := k
		overlaps := within(dir, root)
		if i := strings.IndexAny(k, "*?["); i >= 0 {
			// Patrón: pode coincidir con rutas dentro de root se o seu
			// prefixo fixo está dentro de root ou o contén (conservador).
			dir = path.Dir(k[:i] + "x")
			overlaps = within(dir, root) || within(root, dir)
		}
		if !overlaps {
			continue
		}
		inOther := false
		for _, m := range mounts {
			if m != root && within(m, root) && within(dir, m) {
				inOther = true
				break
			}
		}
		if !inOther && !canWrite(v) {
			return false
		}
	}
	return true
}

// within indica se p é root ou está dentro de root.
func within(p, root string) bool {
	return root == "/" || p == root || strings.HasPrefix(p, root+"/")
}
