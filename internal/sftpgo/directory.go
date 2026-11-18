// Package sftpgo le usuarios, grupos e cartafoles directamente da BD de
// SFTPGo (SQLite) e tradúceos aos recursos que se sincronizan con Syncthing.
// As credenciais dos usuarios compróbanse coa API de SFTPGo (ver auth.go).
//
// Reproduce detalles internos de SFTPGo 2.7.6 (esquema da BD v33, aplicación
// de grupos e permisos). Isto é deliberado: o obxectivo final é
// integrar este código en SFTPGo, onde se usarían directamente os seus
// paquetes. Revisar ao cambiar de versión de SFTPGo.
package sftpgo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/DanBaDo/cld/internal/pairing"
)

// SchemaVersion é a versión do esquema da BD de SFTPGo coa que se probou.
const SchemaVersion = 33

type Config struct {
	DB          string `json:"db"`          // ruta a sftpgo.db
	TablePrefix string `json:"tablePrefix"` // data_provider.sql_tables_prefix
	// URL base do servidor HTTP de SFTPGo (p. ex. http://sftpgo:8080), para
	// comprobar o contrasinal dos usuarios.
	APIURL string `json:"apiURL"`
}

type Directory struct {
	cfg    Config
	db     *sql.DB
	apiURL *url.URL
	http   *http.Client
	now    func() time.Time
	logf   func(format string, args ...any)
}

var prefixRe = regexp.MustCompile(`^[A-Za-z0-9_]*$`)

func Open(cfg Config) (*Directory, error) {
	if cfg.DB == "" {
		return nil, fmt.Errorf("sftpgo: falta a ruta da BD")
	}
	if !prefixRe.MatchString(cfg.TablePrefix) {
		return nil, fmt.Errorf("sftpgo: prefixo de táboas non válido %q", cfg.TablePrefix)
	}
	d := &Directory{cfg: cfg, now: time.Now, logf: log.Printf, http: &http.Client{Timeout: 15 * time.Second}}
	if cfg.APIURL != "" {
		u, err := url.Parse(cfg.APIURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("sftpgo: apiURL non válida %q", cfg.APIURL)
		}
		d.apiURL = u
	}
	// Só lectura: SFTPGo é quen escribe. busy_timeout por se SFTPGo ten a BD
	// bloqueada nese momento.
	dsn := "file:" + (&url.URL{Path: cfg.DB}).EscapedPath() +
		"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.db = db
	return d, nil
}

func (d *Directory) Close() error { return d.db.Close() }

func (d *Directory) table(name string) string { return `"` + d.cfg.TablePrefix + name + `"` }

// CheckSchema avisa se a BD non ten a versión de esquema coñecida.
func (d *Directory) CheckSchema(ctx context.Context) error {
	var v int
	if err := d.db.QueryRowContext(ctx, "SELECT version FROM "+d.table("schema_version")).Scan(&v); err != nil {
		return fmt.Errorf("sftpgo: lendo a versión do esquema: %w", err)
	}
	if v != SchemaVersion {
		d.logf("AVISO: esquema da BD de SFTPGo v%d; este código probouse coa v%d", v, SchemaVersion)
	}
	return nil
}

type dbUser struct {
	rawUser
	id         int64
	status     int
	expiration int64 // ms; 0 = sen caducidade
}

// Snapshot implementa pairing.Directory.
func (d *Directory) Snapshot(ctx context.Context) (*pairing.Snapshot, error) {
	users, err := d.load(ctx)
	if err != nil {
		return nil, err
	}
	nowMs := d.now().UnixMilli()
	snap := &pairing.Snapshot{}
	folders := map[string]pairing.Folder{}
	for _, u := range users {
		if u.status != 1 || (u.expiration > 0 && u.expiration < nowMs) {
			continue
		}
		pu := pairing.User{Name: u.Username}
		for _, f := range resources(&u.rawUser) {
			if prev, ok := folders[f.ID]; ok && prev.Path != f.Path {
				d.logf("sftpgo: %s: ruta distinta para %s (%s / %s); ignórase", u.Username, f.ID, prev.Path, f.Path)
				continue
			}
			folders[f.ID] = f
			pu.Folders = append(pu.Folders, f.ID)
		}
		snap.Users = append(snap.Users, pu)
	}
	for _, f := range folders {
		snap.Folders = append(snap.Folders, f)
	}
	slices.SortFunc(snap.Folders, func(a, b pairing.Folder) int { return strings.Compare(a.ID, b.ID) })
	d.warnNested(snap.Folders)
	return snap, nil
}

// IDs das carpetas en Syncthing.
func homeID(user string) string   { return "sftpgo-home-" + user }
func folderID(name string) string { return "sftpgo-folder-" + name }

// resources devolve os recursos sincronizables do usuario: o directorio
// persoal e os cartafoles virtuais con permiso de escritura e en disco local.
func resources(u *rawUser) []pairing.Folder {
	e := u.effective()
	mounts := []string{}
	for _, f := range e.Folders {
		mounts = append(mounts, f.VirtualPath)
	}
	var out []pairing.Folder
	if e.Provider == localProvider && e.HomeDir != "" && !slices.Contains(mounts, "/") &&
		writableTree(e.Permissions, "/", mounts) {
		out = append(out, pairing.Folder{ID: homeID(u.Username), Label: u.Username + " (persoal)", Path: e.HomeDir})
	}
	for _, f := range e.Folders {
		if f.Provider != localProvider || f.Path == "" {
			continue
		}
		if writableTree(e.Permissions, f.VirtualPath, mounts) {
			out = append(out, pairing.Folder{ID: folderID(f.Name), Label: f.Name, Path: f.Path})
		}
	}
	return out
}

// warnNested avisa de carpetas aniñadas no disco, que Syncthing non xestiona ben.
func (d *Directory) warnNested(fs []pairing.Folder) {
	for _, a := range fs {
		for _, b := range fs {
			if a.ID != b.ID && strings.HasPrefix(b.Path, strings.TrimRight(a.Path, "/")+"/") {
				d.logf("AVISO: %s (%s) está dentro de %s (%s)", b.ID, b.Path, a.ID, a.Path)
			}
		}
	}
}

// load le nunha transacción de lectura usuarios, grupos e cartafoles.
func (d *Directory) load(ctx context.Context) ([]dbUser, error) {
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT u.id, u.username, u.status, u.expiration_date, u.home_dir,
		u.permissions, COALESCE(u.filesystem, ''), COALESCE(r.name, '')
		FROM `+d.table("users")+` u LEFT JOIN `+d.table("roles")+` r ON r.id = u.role_id
		WHERE u.deleted_at = 0 ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	var users []dbUser
	byID := map[int64]int{}
	for rows.Next() {
		var u dbUser
		var perms, fs string
		if err := rows.Scan(&u.id, &u.Username, &u.status, &u.expiration, &u.HomeDir,
			&perms, &fs, &u.Role); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(perms), &u.Permissions); err != nil {
			rows.Close()
			return nil, fmt.Errorf("usuario %s: permisos: %w", u.Username, err)
		}
		u.Provider = provider(fs)
		byID[u.id] = len(users)
		users = append(users, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cartafoles virtuais dos usuarios.
	err = queryFolders(ctx, tx, `SELECT m.user_id, m.virtual_path, f.name, COALESCE(f.path, ''), COALESCE(f.filesystem, '')
		FROM `+d.table("users_folders_mapping")+` m JOIN `+d.table("folders")+` f ON f.id = m.folder_id
		ORDER BY m.user_id, m.sort_order, m.id`, func(owner int64, f rawFolder) {
		if i, ok := byID[owner]; ok {
			users[i].Folders = append(users[i].Folders, f)
		}
	})
	if err != nil {
		return nil, err
	}

	// Grupos e os seus cartafoles.
	groups := map[int64]*rawGroup{}
	err = queryFolders(ctx, tx, `SELECT m.group_id, m.virtual_path, f.name, COALESCE(f.path, ''), COALESCE(f.filesystem, '')
		FROM `+d.table("groups_folders_mapping")+` m JOIN `+d.table("folders")+` f ON f.id = m.folder_id
		ORDER BY m.group_id, m.sort_order, m.id`, func(owner int64, f rawFolder) {
		g := groups[owner]
		if g == nil {
			g = &rawGroup{}
			groups[owner] = g
		}
		g.Folders = append(g.Folders, f)
	})
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT m.user_id, m.group_type, g.id, g.name, COALESCE(g.user_settings, '')
		FROM `+d.table("users_groups_mapping")+` m JOIN `+d.table("groups")+` g ON g.id = m.group_id
		ORDER BY m.user_id, m.sort_order, m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, groupID int64
		var typ int
		var name, settings string
		if err := rows.Scan(&userID, &typ, &groupID, &name, &settings); err != nil {
			return nil, err
		}
		i, ok := byID[userID]
		if !ok || typ == groupMembership {
			continue
		}
		g := rawGroup{Name: name, Type: typ}
		if gf := groups[groupID]; gf != nil {
			g.Folders = gf.Folders
		}
		if settings != "" {
			var s struct {
				HomeDir     string              `json:"home_dir"`
				Permissions map[string][]string `json:"permissions"`
				Filesystem  json.RawMessage     `json:"filesystem"`
			}
			if err := json.Unmarshal([]byte(settings), &s); err != nil {
				return nil, fmt.Errorf("grupo %s: %w", name, err)
			}
			g.HomeDir, g.Permissions, g.Provider = s.HomeDir, s.Permissions, provider(string(s.Filesystem))
		}
		users[i].Groups = append(users[i].Groups, g)
	}
	return users, rows.Err()
}

func queryFolders(ctx context.Context, tx *sql.Tx, q string, add func(owner int64, f rawFolder)) error {
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var owner int64
		var f rawFolder
		var fs string
		if err := rows.Scan(&owner, &f.VirtualPath, &f.Name, &f.Path, &fs); err != nil {
			return err
		}
		f.Provider = provider(fs)
		add(owner, f)
	}
	return rows.Err()
}

// provider extrae filesystem.provider; baleiro ou inválido conta como local
// salvo que non se poida ler, que se trata como non local (non se ofrece).
func provider(fs string) int {
	if fs == "" {
		return localProvider
	}
	var v struct {
		Provider int `json:"provider"`
	}
	if err := json.Unmarshal([]byte(fs), &v); err != nil {
		return -1
	}
	return v.Provider
}
