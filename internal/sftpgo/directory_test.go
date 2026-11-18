package sftpgo

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DanBaDo/cld/internal/totp"
)

// Subconxunto do esquema v33 de SFTPGo (internal/dataprovider/sqlite.go)
// coas columnas que se len.
const testSchema = `
CREATE TABLE "schema_version" ("id" integer PRIMARY KEY, "version" integer NOT NULL);
CREATE TABLE "roles" ("id" integer PRIMARY KEY, "name" varchar(255) NOT NULL UNIQUE);
CREATE TABLE "folders" ("id" integer PRIMARY KEY, "name" varchar(255) NOT NULL UNIQUE, "path" text NULL, "filesystem" text NULL);
CREATE TABLE "groups" ("id" integer PRIMARY KEY, "name" varchar(255) NOT NULL UNIQUE, "user_settings" text NULL);
CREATE TABLE "users" ("id" integer PRIMARY KEY, "username" varchar(255) NOT NULL UNIQUE, "status" integer NOT NULL,
  "expiration_date" bigint NOT NULL, "home_dir" text NOT NULL, "permissions" text NOT NULL, "filters" text NULL,
  "filesystem" text NULL, "deleted_at" bigint NOT NULL, "role_id" integer NULL);
CREATE TABLE "groups_folders_mapping" ("id" integer PRIMARY KEY, "folder_id" integer NOT NULL, "group_id" integer NOT NULL,
  "virtual_path" text NOT NULL, "sort_order" integer DEFAULT 0 NOT NULL);
CREATE TABLE "users_groups_mapping" ("id" integer PRIMARY KEY, "user_id" integer NOT NULL, "group_id" integer NOT NULL,
  "group_type" integer NOT NULL, "sort_order" integer DEFAULT 0 NOT NULL);
CREATE TABLE "users_folders_mapping" ("id" integer PRIMARY KEY, "user_id" integer NOT NULL, "folder_id" integer NOT NULL,
  "virtual_path" text NOT NULL, "sort_order" integer DEFAULT 0 NOT NULL);
INSERT INTO schema_version VALUES (1, 33);
INSERT INTO roles VALUES (1, 'oficina');
INSERT INTO folders VALUES (1, 'comun', '/srv/comun', '{"provider":0}'), (2, 'nube', NULL, '{"provider":1}');
INSERT INTO groups VALUES (1, 'equipo', '{"permissions":{"/comun":["*"],"/nube":["*"]},"filesystem":{"provider":0}}');
INSERT INTO groups_folders_mapping VALUES (1, 1, 1, '/comun', 0), (2, 2, 1, '/nube', 0);
-- proba: TOTP real gardado por SFTPGo (ver kms_test.go)
INSERT INTO users VALUES
 (1, 'proba', 1, 0, '/srv/proba', '{"/":["*"]}',
  '{"totp_config":{"enabled":true,"config_name":"Default","secret":{"status":"Secretbox","payload":"7NxO9jd6gfty19kPVgswtX3Oez3Q8SlOXVnKUmIKP49CVuHyPVTvj+cVFab+cJ7F0+DBkipD5xT9dJp1rHqbcM2WlKUHGwSA","key":"497de61470c09fdc7bc63bd2ff00b17b2b102aff244137c6295a47f0a2910d22","additional_data":"proba"},"protocols":["HTTP"]}}',
  '{"provider":0}', 0, 1),
 (2, 'lector', 1, 0, '/srv/lector', '{"/":["list","download"]}', NULL, NULL, 0, NULL),
 (3, 'desactivado', 0, 0, '/srv/d', '{"/":["*"]}', NULL, NULL, 0, NULL),
 (4, 'caducado', 1, 1000, '/srv/c', '{"/":["*"]}', NULL, NULL, 0, NULL),
 (5, 'borrado', 1, 0, '/srv/b', '{"/":["*"]}', NULL, NULL, 1000, NULL);
INSERT INTO users_groups_mapping VALUES (1, 1, 1, 2, 0), (2, 2, 1, 2, 0);
`

func TestDirectorySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sftpgo.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	w.Close()

	d, err := Open(Config{DB: path})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	ctx := context.Background()
	if err := d.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	snap, err := d.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.Validate(); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, u := range snap.Users {
		names = append(names, u.Name)
	}
	if !slices.Equal(names, []string{"lector", "proba"}) {
		t.Fatalf("usuarios: %v", names)
	}
	lector, proba := snap.Users[0], snap.Users[1]
	if lector.TOTP != nil || !slices.Equal(lector.Folders, []string{"sftpgo-folder-comun"}) {
		t.Errorf("lector: %+v", lector)
	}
	if !slices.Equal(proba.Folders, []string{"sftpgo-home-proba", "sftpgo-folder-comun"}) {
		t.Errorf("proba: carpetas %v", proba.Folders)
	}
	if proba.TOTP == nil || proba.TOTP.Algo != totp.SHA1 {
		t.Fatalf("proba: TOTP %+v", proba.TOTP)
	}
	want, _ := totp.DecodeSecret("S3RZ2OZWS4PTKU2ZAOFSJ567FM6YUIEP")
	if !slices.Equal(proba.TOTP.Secret, want) {
		t.Error("proba: segredo TOTP incorrecto")
	}
	if len(snap.Folders) != 2 || snap.Folders[0].Path != "/srv/comun" || snap.Folders[1].Path != "/srv/proba" {
		t.Errorf("carpetas: %+v", snap.Folders)
	}
}
