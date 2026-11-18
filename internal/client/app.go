// Package client é o cliente de escritorio: fala co Syncthing local e con
// pairsrv, e serve a web de configuración (ver web.go).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DanBaDo/cld/internal/pairing"
	"github.com/DanBaDo/cld/internal/stapi"
)

// Config vén co instalador (o que o usuario descarga de SFTPGo xa leva o
// enderezo do servidor e, idealmente, o seu Device ID).
type Config struct {
	PairsrvURL string `json:"pairsrvURL"`
	// Se se indica, o emparellamento só se acepta se o servidor responde con
	// este Device ID (a "clave do servidor preinstalada").
	ServerDeviceID string `json:"serverDeviceID,omitempty"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.PairsrvURL == "" {
		return nil, fmt.Errorf("%s: falta pairsrvURL", path)
	}
	c.PairsrvURL = strings.TrimRight(c.PairsrvURL, "/")
	return &c, nil
}

// State é o que garda o cliente tras emparellar.
type State struct {
	User           string    `json:"user,omitempty"`
	Token          string    `json:"token,omitempty"`
	ServerDeviceID string    `json:"serverDeviceID,omitempty"`
	DeviceName     string    `json:"deviceName,omitempty"`
	ResumeAt       time.Time `json:"resumeAt,omitzero"` // pausa temporal
}

func (s *State) paired() bool { return s.Token != "" && s.ServerDeviceID != "" }

type App struct {
	cfg         *Config
	st          *stapi.Client
	statePath   string
	foldersBase string // onde se suxire crear as carpetas locais
	http        *http.Client
	now         func() time.Time

	mu    sync.Mutex
	state State
}

func NewApp(cfg *Config, st *stapi.Client, statePath, foldersBase string) (*App, error) {
	a := &App{cfg: cfg, st: st, statePath: statePath, foldersBase: foldersBase,
		http: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
	b, err := os.ReadFile(statePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &a.state); err != nil {
			return nil, fmt.Errorf("%s: %w", statePath, err)
		}
	}
	return a, nil
}

func (a *App) saveLocked() error {
	b, err := json.MarshalIndent(&a.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.statePath), 0o700); err != nil {
		return err
	}
	tmp := a.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.statePath)
}

func (a *App) snapshotState() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// UserError é un erro que se lle pode amosar ao usuario tal cal.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func userErr(format string, args ...any) error { return &UserError{fmt.Sprintf(format, args...)} }

// --- pairsrv -----------------------------------------------------------------

func (a *App) pairsrv(ctx context.Context, method, path, token string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.cfg.PairsrvURL+path, &body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return userErr("Non se pode contactar co servidor: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		if resp.StatusCode == http.StatusInternalServerError {
			return fmt.Errorf("servidor: %s", e.Error)
		}
		return userErr("%s", upperFirst(e.Error))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

// Pair vincula este equipo coa conta do usuario: envía usuario, OTP e o
// Device ID local a pairsrv e engade o servidor como introducer.
func (a *App) Pair(ctx context.Context, user, otp, deviceName string) error {
	user, otp, deviceName = strings.TrimSpace(user), strings.TrimSpace(otp), strings.TrimSpace(deviceName)
	if user == "" || otp == "" || deviceName == "" {
		return userErr("Enche o usuario, o código e o nome do equipo.")
	}
	myID, err := a.st.MyID(ctx)
	if err != nil {
		return err
	}
	var resp pairing.PairResponse
	if err := a.pairsrv(ctx, http.MethodPost, "/api/v1/pair", "", pairing.PairRequest{
		User: user, OTP: otp, DeviceID: myID, DeviceName: deviceName,
	}, &resp); err != nil {
		return err
	}
	if a.cfg.ServerDeviceID != "" && resp.ServerDeviceID != a.cfg.ServerDeviceID {
		return fmt.Errorf("o servidor respondeu co Device ID %s e esperábase %s", resp.ServerDeviceID, a.cfg.ServerDeviceID)
	}
	if err := a.st.EnsureDevice(ctx, resp.ServerDeviceID, stapi.Obj{
		"name":       "Servidor",
		"addresses":  resp.ServerAddresses,
		"introducer": true,
		"paused":     false,
	}); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = State{User: user, Token: resp.Token, ServerDeviceID: resp.ServerDeviceID, DeviceName: deviceName}
	return a.saveLocked()
}

func (a *App) Devices(ctx context.Context) ([]pairing.DeviceInfo, error) {
	s := a.snapshotState()
	var out []pairing.DeviceInfo
	err := a.pairsrv(ctx, http.MethodGet, "/api/v1/devices", s.Token, nil, &out)
	return out, err
}

func (a *App) Revoke(ctx context.Context, deviceID, otp string) error {
	s := a.snapshotState()
	if strings.TrimSpace(otp) == "" {
		return userErr("Fai falta un código OTP para revogar un equipo.")
	}
	return a.pairsrv(ctx, http.MethodDelete, "/api/v1/devices/"+deviceID, s.Token,
		map[string]string{"otp": strings.TrimSpace(otp)}, nil)
}

// --- Recursos ----------------------------------------------------------------

type FolderView struct {
	ID, Label, Path string
	Status          stapi.FolderStatus
	StatusErr       string
}

type OfferView struct {
	ID, Label     string
	SuggestedPath string
}

type Overview struct {
	Paired          bool
	Hostname        string // nome suxerido para o equipo
	User            string
	DeviceName      string
	ServerConnected bool
	Paused          bool
	ResumeAt        time.Time
	Folders         []FolderView
	Offers          []OfferView
}

func (a *App) Overview(ctx context.Context) (*Overview, error) {
	s := a.snapshotState()
	ov := &Overview{Paired: s.paired(), User: s.User, DeviceName: s.DeviceName, ResumeAt: s.ResumeAt}
	if !ov.Paired {
		ov.Hostname, _ = os.Hostname()
		return ov, nil
	}
	devs, err := a.st.Devices(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range devs {
		if d["deviceID"] == s.ServerDeviceID {
			ov.Paused, _ = d["paused"].(bool)
		}
	}
	conns, err := a.st.Connections(ctx)
	if err != nil {
		return nil, err
	}
	ov.ServerConnected = conns[s.ServerDeviceID].Connected

	folders, err := a.st.Folders(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, f := range folders {
		id, _ := f["id"].(string)
		label, _ := f["label"].(string)
		path, _ := f["path"].(string)
		have[id] = true
		fv := FolderView{ID: id, Label: label, Path: path}
		if fv.Status, err = a.st.FolderStatus(ctx, id); err != nil {
			fv.StatusErr = err.Error()
		}
		ov.Folders = append(ov.Folders, fv)
	}
	pending, err := a.st.PendingFolders(ctx)
	if err != nil {
		return nil, err
	}
	for id, p := range pending {
		o, ok := p.OfferedBy[s.ServerDeviceID] // só ofertas do servidor
		if !ok || have[id] {
			continue
		}
		label := o.Label
		if label == "" {
			label = id
		}
		ov.Offers = append(ov.Offers, OfferView{ID: id, Label: label, SuggestedPath: a.suggestPath(label)})
	}
	slices.SortFunc(ov.Folders, func(x, y FolderView) int { return strings.Compare(x.Label, y.Label) })
	slices.SortFunc(ov.Offers, func(x, y OfferView) int { return strings.Compare(x.Label, y.Label) })
	return ov, nil
}

// suggestPath propón unha carpeta local a partir da etiqueta.
func (a *App) suggestPath(label string) string {
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < ' ' {
			return '-'
		}
		return r
	}, label)
	name = strings.Trim(strings.TrimSpace(name), ".")
	if name == "" {
		name = "carpeta"
	}
	return filepath.Join(a.foldersBase, name)
}

// Accept acepta un recurso ofrecido polo servidor e sincronízao en path.
func (a *App) Accept(ctx context.Context, id, path string) error {
	s := a.snapshotState()
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return userErr("A carpeta local ten que ser unha ruta completa (p. ex. %s).", a.suggestPath("Documentos"))
	}
	pending, err := a.st.PendingFolders(ctx)
	if err != nil {
		return err
	}
	o, ok := pending[id].OfferedBy[s.ServerDeviceID]
	if !ok {
		return userErr("O servidor xa non ofrece ese recurso.")
	}
	folders, err := a.st.Folders(ctx)
	if err != nil {
		return err
	}
	for _, f := range folders {
		p, _ := f["path"].(string)
		p = filepath.Clean(p)
		if p == path || strings.HasPrefix(path, p+string(filepath.Separator)) || strings.HasPrefix(p, path+string(filepath.Separator)) {
			label, _ := f["label"].(string)
			return userErr("Esa carpeta coincide ou está dentro doutra que xa se sincroniza (%s).", label)
		}
	}
	label := o.Label
	if label == "" {
		label = id
	}
	if err := a.st.EnsureFolder(ctx, id, stapi.Obj{
		"label":   label,
		"path":    path,
		"type":    "sendreceive",
		"devices": []stapi.Obj{{"deviceID": s.ServerDeviceID}},
	}); err != nil {
		return err
	}
	// O introducer só presenta os pares dunha carpeta cando xa está
	// compartida con el; forzar a reconexión para recibilos xa (ver
	// stapi.Reconnect). Se está en pausa, xa se fará ao retomar.
	ov, err := a.Overview(ctx)
	if err == nil && !ov.Paused {
		return a.st.Reconnect(ctx, s.ServerDeviceID)
	}
	return nil
}

// Unsync deixa de sincronizar un recurso. Os ficheiros locais non se borran.
// Reconéctase co servidor para que o recurso volva aparecer como ofrecido.
func (a *App) Unsync(ctx context.Context, id string) error {
	if err := a.st.DeleteFolder(ctx, id); err != nil {
		return err
	}
	ov, err := a.Overview(ctx)
	if err == nil && ov.Paired && !ov.Paused {
		return a.st.Reconnect(ctx, a.snapshotState().ServerDeviceID)
	}
	return nil
}

// --- Pausa -------------------------------------------------------------------

// Pause pausa toda a sincronización; con d > 0 retómase soa despois.
func (a *App) Pause(ctx context.Context, d time.Duration) error {
	if err := a.st.PauseAll(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ResumeAt = time.Time{}
	if d > 0 {
		a.state.ResumeAt = a.now().Add(d).UTC()
	}
	return a.saveLocked()
}

func (a *App) Resume(ctx context.Context) error {
	if err := a.st.ResumeAll(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ResumeAt = time.Time{}
	return a.saveLocked()
}

// Tick aplica as tarefas periódicas (fin da pausa temporal).
func (a *App) Tick(ctx context.Context) error {
	s := a.snapshotState()
	if !s.ResumeAt.IsZero() && !a.now().Before(s.ResumeAt) {
		return a.Resume(ctx)
	}
	return nil
}
