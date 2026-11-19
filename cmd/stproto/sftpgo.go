package main

// Cliente mínimo da API REST de SFTPGo para preparar e manipular os datos de
// proba (dev/sftpgo-demo.json).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/DanBaDo/cld/internal/sftpgo"
)

const (
	sftpgoAPI  = "http://sftpgo:8080/api/v2"
	demoConfig = "dev/sftpgo-demo.json"
)

type demoData struct {
	Folders []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"folders"`
	Groups []struct {
		Name        string              `json:"name"`
		Folders     map[string]string   `json:"folders"` // ruta virtual → cartafol
		Permissions map[string][]string `json:"permissions"`
	} `json:"groups"`
	Users []demoUser `json:"users"`
}

type demoUser struct {
	Name        string              `json:"name"`
	Password    string              `json:"password"`
	HomeDir     string              `json:"homeDir"`
	Permissions map[string][]string `json:"permissions"`
	Groups      []string            `json:"groups"` // secundarios; o primario é sempre syncthing
}

func loadDemo() (*demoData, error) {
	b, err := os.ReadFile(demoConfig)
	if err != nil {
		return nil, err
	}
	var d demoData
	return &d, json.Unmarshal(b, &d)
}

func (d *demoData) user(name string) *demoUser {
	for i := range d.Users {
		if d.Users[i].Name == name {
			return &d.Users[i]
		}
	}
	return nil
}

// sftpgoCall fai unha petición á API. auth é a cabeceira Authorization.
func sftpgoCall(ctx context.Context, method, path, auth string, hdr map[string]string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, sftpgoAPI+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return &apiError{Code: resp.StatusCode, Msg: "sftpgo " + method + " " + path + ": " + strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func basic(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func jsonBody(v any) (io.Reader, map[string]string) {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b), map[string]string{"Content-Type": "application/json"}
}

func sftpgoToken(ctx context.Context, path, auth string, hdr map[string]string) (string, error) {
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := sftpgoCall(ctx, http.MethodGet, path, auth, hdr, nil, &t); err != nil {
		return "", err
	}
	return "Bearer " + t.AccessToken, nil
}

func adminAuth(ctx context.Context) (string, error) {
	return sftpgoToken(ctx, "/token", basic(os.Getenv("SFTPGO_ADMIN_USER"), os.Getenv("SFTPGO_ADMIN_PASSWORD")), nil)
}

// userAuth inicia sesión como usuario.
func userAuth(ctx context.Context, u *demoUser) (string, error) {
	return sftpgoToken(ctx, "/user/token", basic(u.Name, u.Password), nil)
}

func waitSFTPGo(ctx context.Context) error {
	return waitFor(ctx, "SFTPGo dispoñible", func() (bool, error) {
		_, err := adminAuth(ctx)
		return err == nil, nil
	})
}

// cmdSFTPGoSetup crea en SFTPGo o que necesita pairsrv (grupo syncthing e
// regra de eventos, igual que `pairsrv setup-sftpgo`) e os cartafoles, grupos
// e usuarios de proba. Os que xa existen non se tocan.
func cmdSFTPGoSetup(ctx context.Context) error {
	d, err := loadDemo()
	if err != nil {
		return err
	}
	if err := waitSFTPGo(ctx); err != nil {
		return err
	}
	admin, err := sftpgo.NewAdmin(ctx, strings.TrimSuffix(sftpgoAPI, "/api/v2"),
		os.Getenv("SFTPGO_ADMIN_USER"), os.Getenv("SFTPGO_ADMIN_PASSWORD"))
	if err != nil {
		return err
	}
	logf := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }
	if err := admin.Setup(ctx, "http://pairsrv:8081/internal/v1/sync", os.Getenv("PAIRSRV_HOOK_TOKEN"), logf); err != nil {
		return err
	}
	create := func(kind, name string, obj any) error {
		ok, err := admin.Create(ctx, kind, name, obj)
		if ok {
			logf("sftpgo: creado %s %s", kind, name)
		}
		return err
	}
	local := map[string]any{"provider": 0}
	for _, f := range d.Folders {
		if err := create("folders", f.Name, map[string]any{"name": f.Name, "mapped_path": f.Path, "filesystem": local}); err != nil {
			return err
		}
	}
	for _, g := range d.Groups {
		var vfs []map[string]any
		for vp, name := range g.Folders {
			vfs = append(vfs, map[string]any{"name": name, "virtual_path": vp})
		}
		if err := create("groups", g.Name, map[string]any{
			"name": g.Name, "virtual_folders": vfs, "user_settings": map[string]any{"permissions": g.Permissions},
		}); err != nil {
			return err
		}
	}
	for i := range d.Users {
		u := &d.Users[i]
		groups := []map[string]any{{"name": sftpgo.SyncthingGroup, "type": 1}} // primario
		for _, g := range u.Groups {
			groups = append(groups, map[string]any{"name": g, "type": 2}) // secundario
		}
		if err := create("users", u.Name, map[string]any{
			"username": u.Name, "status": 1, "password": u.Password, "home_dir": u.HomeDir,
			"permissions": u.Permissions, "groups": groups, "filesystem": local,
		}); err != nil {
			return err
		}
	}
	return nil
}

// sftpgoUpload sobe un ficheiro como o usuario, igual que desde o WebClient.
func sftpgoUpload(ctx context.Context, user, vpath, content string) error {
	d, err := loadDemo()
	if err != nil {
		return err
	}
	u := d.user(user)
	if u == nil {
		return fmt.Errorf("usuario de proba descoñecido: %s", user)
	}
	tok, err := userAuth(ctx, u)
	if err != nil {
		return err
	}
	q := url.Values{"path": {vpath}, "mkdir_parents": {"true"}}
	return sftpgoCall(ctx, http.MethodPost, "/user/files/upload?"+q.Encode(), tok,
		map[string]string{"Content-Type": "application/octet-stream"}, strings.NewReader(content), nil)
}

// sftpgoSetStatus activa (1) ou desactiva (0) un usuario.
func sftpgoSetStatus(ctx context.Context, user string, status int) error {
	admin, err := adminAuth(ctx)
	if err != nil {
		return err
	}
	var u map[string]any
	if err := sftpgoCall(ctx, http.MethodGet, "/users/"+url.PathEscape(user), admin, nil, nil, &u); err != nil {
		return err
	}
	u["status"] = status
	body, hdr := jsonBody(u)
	return sftpgoCall(ctx, http.MethodPut, "/users/"+url.PathEscape(user), admin, hdr, body, nil)
}

// sftpgoList lista un directorio como o usuario.
func sftpgoList(ctx context.Context, user, vpath string) ([]string, error) {
	d, err := loadDemo()
	if err != nil {
		return nil, err
	}
	u := d.user(user)
	if u == nil {
		return nil, fmt.Errorf("usuario de proba descoñecido: %s", user)
	}
	tok, err := userAuth(ctx, u)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name string `json:"name"`
	}
	if err := sftpgoCall(ctx, http.MethodGet, "/user/dirs?"+url.Values{"path": {vpath}}.Encode(), tok, nil, nil, &entries); err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names, nil
}
