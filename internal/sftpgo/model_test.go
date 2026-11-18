package sftpgo

import (
	"slices"
	"testing"
)

var rw = []string{"*"}
var ro = []string{"list", "download"}

func TestCanWrite(t *testing.T) {
	cases := []struct {
		perms []string
		want  bool
	}{
		{[]string{"*"}, true},
		{ro, false},
		{[]string{"list", "download", "upload", "overwrite", "create_dirs", "delete", "rename"}, true},
		{[]string{"list", "download", "upload", "overwrite", "create_dirs", "delete_files", "delete_dirs", "rename_files", "rename_dirs"}, true},
		{[]string{"list", "download", "upload", "overwrite", "create_dirs", "delete_files", "rename"}, false},
		{[]string{"list", "download", "upload", "create_dirs", "delete", "rename"}, false}, // sen overwrite
		{[]string{"upload", "overwrite", "create_dirs", "delete", "rename"}, false},        // sen lectura
	}
	for _, c := range cases {
		if got := canWrite(c.perms); got != c.want {
			t.Errorf("%v: %v", c.perms, got)
		}
	}
}

func TestPermissionsFor(t *testing.T) {
	perms := map[string][]string{"/": ro, "/a": rw, "/p/*": {"list"}}
	for p, want := range map[string][]string{
		"/": ro, "/a": rw, "/a/b/c": rw, "/b": ro, "/p/x": {"list"}, "/p/x/y": {"list"}, "/p": ro,
	} {
		if got := permissionsFor(perms, p); !slices.Equal(got, want) {
			t.Errorf("%s: %v, quería %v", p, got, want)
		}
	}
}

func TestWritableTree(t *testing.T) {
	mounts := []string{"/comun"}
	cases := []struct {
		name  string
		perms map[string][]string
		root  string
		want  bool
	}{
		{"todo escritura", map[string][]string{"/": rw}, "/", true},
		{"raíz só lectura", map[string][]string{"/": ro}, "/", false},
		{"subdirectorio só lectura", map[string][]string{"/": rw, "/docs/arquivo": ro}, "/", false},
		{"só lectura noutro montaxe", map[string][]string{"/": rw, "/comun": ro}, "/", true},
		{"montaxe con escritura", map[string][]string{"/": ro, "/comun": rw}, "/comun", true},
		{"montaxe herda só lectura", map[string][]string{"/": ro}, "/comun", false},
		{"patrón dentro", map[string][]string{"/": rw, "/comun/*": ro}, "/comun", false},
		{"patrón que pode coincidir", map[string][]string{"/": rw, "/*/priv": ro}, "/comun", false},
		{"patrón alleo", map[string][]string{"/": rw, "/outro/*": ro}, "/comun", true},
	}
	for _, c := range cases {
		if got := writableTree(c.perms, c.root, mounts); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestEffectiveGroups(t *testing.T) {
	u := rawUser{
		Username:    "alice",
		HomeDir:     "/srv/alice",
		Permissions: map[string][]string{"/": ro, "/x": rw},
		Groups: []rawGroup{
			{Name: "sec", Type: groupSecondary,
				Permissions: map[string][]string{"/": rw, "/x": ro, "/comun": rw},
				Folders: []rawFolder{
					{Name: "comun", Path: "/srv/comun", VirtualPath: "/comun"},
					{Name: "raiz", Path: "/srv/raiz", VirtualPath: "/"}, // ignorado en secundarios
				}},
			{Name: "prim", Type: groupPrimary, HomeDir: "/srv/home/%username%",
				Permissions: map[string][]string{"/": rw, "/%username%-priv": ro},
				Folders:     []rawFolder{{Name: "p", Path: "/srv/p/%username%", VirtualPath: "/p"}}},
			{Name: "mem", Type: groupMembership, Permissions: map[string][]string{"/m": rw}},
		},
	}
	e := u.effective()
	if e.HomeDir != "/srv/home/alice" {
		t.Errorf("home: %s", e.HomeDir)
	}
	want := map[string][]string{"/": rw, "/x": rw, "/alice-priv": ro, "/comun": rw}
	if len(e.Permissions) != len(want) {
		t.Errorf("permisos: %v", e.Permissions)
	}
	for k, v := range want {
		if !slices.Equal(e.Permissions[k], v) {
			t.Errorf("permiso %s: %v, quería %v", k, e.Permissions[k], v)
		}
	}
	var vps []string
	for _, f := range e.Folders {
		vps = append(vps, f.VirtualPath+"="+f.Path)
	}
	if !slices.Equal(vps, []string{"/p=/srv/p/alice", "/comun=/srv/comun"}) {
		t.Errorf("cartafoles: %v", vps)
	}
}

func TestResources(t *testing.T) {
	u := rawUser{
		Username:    "bob",
		HomeDir:     "/srv/bob",
		Permissions: map[string][]string{"/": ro, "/comun": rw, "/s3": rw},
		Folders: []rawFolder{
			{Name: "comun", Path: "/srv/comun", VirtualPath: "/comun"},
			{Name: "arquivo", Path: "/srv/arquivo", VirtualPath: "/arquivo"},
			{Name: "s3", Provider: 1, VirtualPath: "/s3"},
		},
	}
	var ids []string
	for _, f := range resources(&u) {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, []string{"sftpgo-folder-comun"}) {
		t.Errorf("bob: %v", ids)
	}
	u.Permissions["/"] = rw
	u.Provider = 4 // cifrado
	ids = nil
	for _, f := range resources(&u) {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, []string{"sftpgo-folder-comun", "sftpgo-folder-arquivo"}) {
		t.Errorf("bob con home cifrado: %v", ids)
	}
}
