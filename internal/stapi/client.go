// Package stapi é un cliente mínimo da API REST de Syncthing.
//
// Os obxectos de configuración trátanse como mapas (Obj) para conservar
// todos os campos que devolve Syncthing ao facer PUT, aínda que este
// código non os coñeza.
package stapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Obj = map[string]any

var ErrNotFound = errors.New("non atopado")

type Client struct {
	Name string
	Base string
	Key  string
	HTTP *http.Client
}

func New(name, base, key string) *Client {
	return &Client{Name: name, Base: strings.TrimRight(base, "/"), Key: key,
		HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.Key)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s %s: %s: %s", c.Name, method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// WaitHealthy agarda a que o nodo responda.
func (c *Client) WaitHealthy(ctx context.Context) error {
	for {
		var h Obj
		if err := c.Do(ctx, http.MethodGet, "/rest/noauth/health", nil, &h); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s non responde: %w", c.Name, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func (c *Client) MyID(ctx context.Context) (string, error) {
	var s struct {
		MyID string `json:"myID"`
	}
	err := c.Do(ctx, http.MethodGet, "/rest/system/status", nil, &s)
	return s.MyID, err
}

func (c *Client) PatchOptions(ctx context.Context, patch Obj) error {
	return c.Do(ctx, http.MethodPatch, "/rest/config/options", patch, nil)
}

func (c *Client) Devices(ctx context.Context) ([]Obj, error) {
	var out []Obj
	err := c.Do(ctx, http.MethodGet, "/rest/config/devices", nil, &out)
	return out, err
}

func (c *Client) Folders(ctx context.Context) ([]Obj, error) {
	var out []Obj
	err := c.Do(ctx, http.MethodGet, "/rest/config/folders", nil, &out)
	return out, err
}

func (c *Client) Folder(ctx context.Context, id string) (Obj, error) {
	var out Obj
	err := c.Do(ctx, http.MethodGet, "/rest/config/folders/"+url.PathEscape(id), nil, &out)
	return out, err
}

// EnsureDevice crea ou actualiza un dispositivo. Se non existe, parte dos
// valores por defecto de Syncthing; en ambos casos aplica patch por riba.
func (c *Client) EnsureDevice(ctx context.Context, id string, patch Obj) error {
	path := "/rest/config/devices/" + url.PathEscape(id)
	return c.ensure(ctx, path, "/rest/config/defaults/device", Obj{"deviceID": id}, patch)
}

func (c *Client) DeleteDevice(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/rest/config/devices/"+url.PathEscape(id), nil, nil)
}

// EnsureFolder crea ou actualiza unha carpeta (mesma semántica que EnsureDevice).
func (c *Client) EnsureFolder(ctx context.Context, id string, patch Obj) error {
	path := "/rest/config/folders/" + url.PathEscape(id)
	return c.ensure(ctx, path, "/rest/config/defaults/folder", Obj{"id": id}, patch)
}

// ShareFolder engade deviceID á lista de dispositivos da carpeta.
func (c *Client) ShareFolder(ctx context.Context, folderID, deviceID string) error {
	f, err := c.Folder(ctx, folderID)
	if err != nil {
		return err
	}
	devs, _ := f["devices"].([]any)
	for _, d := range devs {
		if m, ok := d.(map[string]any); ok && m["deviceID"] == deviceID {
			return nil
		}
	}
	f["devices"] = append(devs, Obj{"deviceID": deviceID})
	return c.Do(ctx, http.MethodPut, "/rest/config/folders/"+url.PathEscape(folderID), f, nil)
}

func (c *Client) ensure(ctx context.Context, path, defaultsPath string, ident, patch Obj) error {
	var cur Obj
	err := c.Do(ctx, http.MethodGet, path, nil, &cur)
	if errors.Is(err, ErrNotFound) {
		err = c.Do(ctx, http.MethodGet, defaultsPath, nil, &cur)
	}
	if err != nil {
		return err
	}
	for k, v := range ident {
		cur[k] = v
	}
	for k, v := range patch {
		cur[k] = v
	}
	return c.Do(ctx, http.MethodPut, path, cur, nil)
}

// PendingFolder: oferta dunha carpeta por parte doutro dispositivo.
type PendingFolder struct {
	OfferedBy map[string]struct {
		Time  time.Time `json:"time"`
		Label string    `json:"label"`
	} `json:"offeredBy"`
}

func (c *Client) PendingFolders(ctx context.Context) (map[string]PendingFolder, error) {
	out := map[string]PendingFolder{}
	err := c.Do(ctx, http.MethodGet, "/rest/cluster/pending/folders", nil, &out)
	return out, err
}

func (c *Client) PauseDevice(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodPost, "/rest/system/pause?device="+url.QueryEscape(id), nil, nil)
}

func (c *Client) ResumeDevice(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodPost, "/rest/system/resume?device="+url.QueryEscape(id), nil, nil)
}

// Reconnect pecha e reabre a conexión cun dispositivo. Serve para que o
// introducer volva enviar a súa ClusterConfig despois de aceptar unha
// carpeta: Syncthing só presenta pares en carpetas xa compartidas co
// introducer, e o servidor non reenvía a ClusterConfig cando o cliente
// acepta a carpeta.
func (c *Client) Reconnect(ctx context.Context, id string) error {
	if err := c.PauseDevice(ctx, id); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
	}
	return c.ResumeDevice(ctx, id)
}

type Connection struct {
	Connected bool   `json:"connected"`
	Address   string `json:"address"`
	Type      string `json:"type"`
}

type DeviceStats struct {
	LastSeen time.Time `json:"lastSeen"`
}

func (c *Client) DeviceStats(ctx context.Context) (map[string]DeviceStats, error) {
	out := map[string]DeviceStats{}
	err := c.Do(ctx, http.MethodGet, "/rest/stats/device", nil, &out)
	return out, err
}

func (c *Client) Connections(ctx context.Context) (map[string]Connection, error) {
	var out struct {
		Connections map[string]Connection `json:"connections"`
	}
	err := c.Do(ctx, http.MethodGet, "/rest/system/connections", nil, &out)
	return out.Connections, err
}
