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
		Name         string              `json:"name"`
		Folders      map[string]string   `json:"folders"` // ruta virtual → cartafol
		Permissions  map[string][]string `json:"permissions"`
		FilePatterns []any               `json:"filePatterns"`
	} `json:"groups"`
	Users []demoUser `json:"users"`
}

type demoUser struct {
	Name         string              `json:"name"`
	Password     string              `json:"password"`
	TOTPSecret   string              `json:"totpSecret"`
	HomeDir      string              `json:"homeDir"`
	Permissions  map[string][]string `json:"permissions"`
	PrimaryGroup string              `json:"primaryGroup"`
	Groups       []string            `json:"groups"` // secundarios
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

// userAuth inicia sesión como usuario. Con TOTP activo, SFTPGo pide o código.
func userAuth(ctx context.Context, u *demoUser, withOTP bool) (string, error) {
	var hdr map[string]string
	if withOTP {
		code, err := devOTP(ctx, u.Name)
		if err != nil {
			return "", err
		}
		hdr = map[string]string{"X-SFTPGO-OTP": code}
	}
	return sftpgoToken(ctx, "/user/token", basic(u.Name, u.Password), hdr)
}

func waitSFTPGo(ctx context.Context) error {
	return waitFor(ctx, "SFTPGo dispoñible", func() (bool, error) {
		_, err := adminAuth(ctx)
		return err == nil, nil
	})
}

// cmdSFTPGoSetup crea en SFTPGo os cartafoles, grupos e usuarios de proba e
// activa o TOTP de cada usuario (como faría o propio usuario no WebClient).
// Os que xa existen non se tocan.
func cmdSFTPGoSetup(ctx context.Context) error {
	d, err := loadDemo()
	if err != nil {
		return err
	}
	if err := waitSFTPGo(ctx); err != nil {
		return err
	}
	admin, err := adminAuth(ctx)
	if err != nil {
		return err
	}
	create := func(kind, name string, obj any) (bool, error) {
		if err := sftpgoCall(ctx, http.MethodGet, "/"+kind+"/"+url.PathEscape(name), admin, nil, nil, nil); err == nil {
			return false, nil
		} else if !isStatus(err, 404) {
			return false, err
		}
		body, hdr := jsonBody(obj)
		if err := sftpgoCall(ctx, http.MethodPost, "/"+kind, admin, hdr, body, nil); err != nil {
			return false, err
		}
		fmt.Printf("sftpgo: creado %s %s\n", kind, name)
		return true, nil
	}
	local := map[string]any{"provider": 0}
	for _, f := range d.Folders {
		if _, err := create("folders", f.Name, map[string]any{"name": f.Name, "mapped_path": f.Path, "filesystem": local}); err != nil {
			return err
		}
	}
	for _, g := range d.Groups {
		var vfs []map[string]any
		for vp, name := range g.Folders {
			vfs = append(vfs, map[string]any{"name": name, "virtual_path": vp})
		}
		settings := map[string]any{"permissions": g.Permissions}
		if len(g.FilePatterns) > 0 {
			settings["filters"] = map[string]any{"file_patterns": g.FilePatterns}
		}
		if _, err := create("groups", g.Name, map[string]any{
			"name": g.Name, "virtual_folders": vfs, "user_settings": settings,
		}); err != nil {
			return err
		}
	}
	// Aviso a pairsrv cando cambian usuarios ou grupos. Os cartafoles non
	// xeran eventos de provedor: eses cambios chegan coa reconciliación periódica.
	if _, err := create("eventactions", "pairsrv-sync", map[string]any{
		"name": "pairsrv-sync", "type": 1,
		"description": "Pide a pairsrv que aplique en Syncthing os cambios de usuarios e grupos",
		"options": map[string]any{"http_config": map[string]any{
			"endpoint": "http://pairsrv:8081/internal/v1/sync", "method": "POST", "timeout": 10,
			"headers": []map[string]string{{"key": "Authorization", "value": "Bearer " + os.Getenv("PAIRSRV_HOOK_TOKEN")}},
		}},
	}); err != nil {
		return err
	}
	if _, err := create("eventrules", "pairsrv-sync", map[string]any{
		"name": "pairsrv-sync", "status": 1, "trigger": 2,
		"conditions": map[string]any{
			"provider_events": []string{"add", "update", "delete"},
			"options":         map[string]any{"provider_objects": []string{"user", "group"}},
		},
		"actions": []map[string]any{{"name": "pairsrv-sync", "order": 1}},
	}); err != nil {
		return err
	}

	for i := range d.Users {
		u := &d.Users[i]
		var groups []map[string]any
		if u.PrimaryGroup != "" {
			groups = append(groups, map[string]any{"name": u.PrimaryGroup, "type": 1})
		}
		for _, g := range u.Groups {
			groups = append(groups, map[string]any{"name": g, "type": 2}) // secundario
		}
		created, err := create("users", u.Name, map[string]any{
			"username": u.Name, "status": 1, "password": u.Password, "home_dir": u.HomeDir,
			"permissions": u.Permissions, "groups": groups, "filesystem": local,
		})
		if err != nil {
			return err
		}
		if !created || u.TOTPSecret == "" {
			continue
		}
		tok, err := userAuth(ctx, u, false)
		if err != nil {
			return err
		}
		body, hdr := jsonBody(map[string]any{
			"enabled": true, "config_name": "Default", "protocols": []string{"HTTP"},
			"secret": map[string]any{"status": "Plain", "payload": u.TOTPSecret},
		})
		if err := sftpgoCall(ctx, http.MethodPost, "/user/totp/save", tok, hdr, body, nil); err != nil {
			return err
		}
		fmt.Printf("sftpgo: TOTP activado para %s\n", u.Name)
	}
	return nil
}

// sftpgoUpload sobe un ficheiro como o usuario (con OTP), igual que desde o
// WebClient.
func sftpgoUpload(ctx context.Context, user, vpath, content string) error {
	d, err := loadDemo()
	if err != nil {
		return err
	}
	u := d.user(user)
	if u == nil {
		return fmt.Errorf("usuario de proba descoñecido: %s", user)
	}
	tok, err := userAuth(ctx, u, true)
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

// sftpgoList lista un directorio como o usuario (con OTP).
func sftpgoList(ctx context.Context, user, vpath string) ([]string, error) {
	d, err := loadDemo()
	if err != nil {
		return nil, err
	}
	u := d.user(user)
	if u == nil {
		return nil, fmt.Errorf("usuario de proba descoñecido: %s", user)
	}
	tok, err := userAuth(ctx, u, true)
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
