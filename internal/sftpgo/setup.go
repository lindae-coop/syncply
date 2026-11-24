package sftpgo

// Configuración de SFTPGo que necesita pairsrv, creada coa API de
// administración: o grupo primario que oculta os ficheiros de Syncthing e a
// regra do xestor de eventos que avisa a pairsrv. Úsana `pairsrv setup-sftpgo`
// (produción) e stproto (desenvolvemento).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SyncthingGroup é o grupo que hai que poñer como **primario** a todos os
// usuarios que sincronizan: os patróns de ficheiros de "/" só se herdan de
// grupos primarios.
const SyncthingGroup = "syncthing"

// HiddenPatterns son os ficheiros internos de Syncthing que se ocultan en SFTPGo.
var HiddenPatterns = []string{".stfolder", ".stversions", ".stignore", "~syncthing~*", ".syncthing.*.tmp"}

// HookName é o nome da acción e da regra de eventos que avisan a pairsrv.
const HookName = "pairsrv-sync"

// Admin é un cliente mínimo da API REST de administración de SFTPGo.
type Admin struct {
	base string // p. ex. http://sftpgo:8080/api/v2
	auth string // Authorization
	otp  string // X-SFTPGO-OTP (só ao pedir o token)
	http *http.Client
}

// APIError é unha resposta de erro da API.
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string { return fmt.Sprintf("sftpgo %d: %s", e.Code, e.Msg) }

// StatusCode é o código HTTP da resposta.
func (e *APIError) StatusCode() int { return e.Code }

// PublicMessage é a explicación que dá SFTPGo (campos "message" e "error"
// da resposta), para mostrala no panel.
func (e *APIError) PublicMessage() string {
	_, body, _ := strings.Cut(e.Msg, ": ")
	var v struct{ Message, Error string }
	if json.Unmarshal([]byte(body), &v) != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(v.Message, ".") + ": " + v.Error)
}

// IsStatus indica se err é unha resposta da API con ese código.
func IsStatus(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == code
}

// NewAdmin inicia sesión como administrador. apiURL é a URL base do servidor
// HTTP de SFTPGo (a mesma que Config.APIURL).
func NewAdmin(ctx context.Context, apiURL, user, pass string) (*Admin, error) {
	a, _, err := newAdmin(ctx, apiURL, user, pass, "")
	return a, err
}

// newAdmin inicia sesión (otp: segundo factor do administrador, se o ten) e
// devolve tamén a caducidade do token.
func newAdmin(ctx context.Context, apiURL, user, pass, otp string) (*Admin, time.Time, error) {
	a := &Admin{base: strings.TrimRight(apiURL, "/") + "/api/v2", http: &http.Client{Timeout: 30 * time.Second}}
	var t struct {
		AccessToken string    `json:"access_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	a.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	a.otp = otp
	err := a.Call(ctx, http.MethodGet, "/token", nil, &t)
	a.otp = ""
	if err != nil {
		return nil, time.Time{}, err
	}
	if t.AccessToken == "" {
		return nil, time.Time{}, errors.New("sftpgo: resposta sen token")
	}
	a.auth = "Bearer " + t.AccessToken
	return a, t.ExpiresAt, nil
}

// Call fai unha petición á API (path relativo a /api/v2).
func (a *Admin) Call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.auth)
	if a.otp != "" {
		req.Header.Set("X-SFTPGO-OTP", a.otp)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return &APIError{Code: resp.StatusCode, Msg: method + " " + path + ": " + strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Create crea un obxecto (kind: users, groups, folders, eventactions,
// eventrules) se non existe. Devolve true se o creou; os que xa existen non
// se tocan.
func (a *Admin) Create(ctx context.Context, kind, name string, obj any) (bool, error) {
	err := a.Call(ctx, http.MethodGet, "/"+kind+"/"+url.PathEscape(name), nil, nil)
	if err == nil {
		return false, nil
	}
	if !IsStatus(err, http.StatusNotFound) {
		return false, err
	}
	if err := a.Call(ctx, http.MethodPost, "/"+kind, obj, nil); err != nil {
		return false, err
	}
	return true, nil
}

// Setup crea en SFTPGo o grupo SyncthingGroup e a acción e regra de eventos
// que chaman a hookURL (o endpoint /internal/v1/sync de pairsrv) co token
// dado. Os obxectos que xa existen non se modifican. logf recibe unha liña
// por obxecto creado.
func (a *Admin) Setup(ctx context.Context, hookURL, hookToken string, logf func(string, ...any)) error {
	created := func(ok bool, err error, what string) error {
		if err == nil && ok {
			logf("sftpgo: creado %s", what)
		}
		return err
	}
	ok, err := a.Create(ctx, "groups", SyncthingGroup, map[string]any{
		"name":        SyncthingGroup,
		"description": "Grupo primario dos usuarios que sincronizan: oculta os ficheiros internos de Syncthing",
		"user_settings": map[string]any{"filters": map[string]any{"file_patterns": []map[string]any{{
			"path": "/", "denied_patterns": HiddenPatterns, "deny_policy": 1, // 1 = ocultar
		}}}},
	})
	if err := created(ok, err, "grupo "+SyncthingGroup); err != nil {
		return err
	}
	// Os cartafoles non xeran eventos de provedor: eses cambios chegan coa
	// reconciliación periódica de pairsrv.
	ok, err = a.Create(ctx, "eventactions", HookName, map[string]any{
		"name": HookName, "type": 1, // HTTP
		"description": "Pide a pairsrv que aplique en Syncthing os cambios de usuarios e grupos",
		"options": map[string]any{"http_config": map[string]any{
			"endpoint": hookURL, "method": "POST", "timeout": 10,
			"headers": []map[string]string{{"key": "Authorization", "value": "Bearer " + hookToken}},
		}},
	})
	if err := created(ok, err, "acción de eventos "+HookName); err != nil {
		return err
	}
	ok, err = a.Create(ctx, "eventrules", HookName, map[string]any{
		"name": HookName, "status": 1, "trigger": 2, // provedor
		"conditions": map[string]any{
			"provider_events": []string{"add", "update", "delete"},
			"options":         map[string]any{"provider_objects": []string{"user", "group"}},
		},
		"actions": []map[string]any{{"name": HookName, "order": 1}},
	})
	return created(ok, err, "regra de eventos "+HookName)
}
