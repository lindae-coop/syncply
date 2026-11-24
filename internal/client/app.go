// Package client é o cliente de escritorio: fala co Syncthing local e con
// pairsrv, e serve a web de configuración (ver web.go).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lindae-coop/syncply/internal/pairing"
	"github.com/lindae-coop/syncply/internal/stapi"
)

// DefaultSyncthingOptions aplícanse sempre ao Syncthing do cliente, tamén
// antes de vincular (o cliente é xenérico: non leva configuración de ningún
// servidor). Despois de vincular, pairsrv pode cambialas (syncthingOptions na
// resposta do emparellamento).
var DefaultSyncthingOptions = map[string]any{
	"globalAnnounceEnabled": false, // sen descubrimento global
	"localAnnounceEnabled":  true,  // pares na mesma rede
	"relaysEnabled":         false,
	"natEnabled":            false,
	"urAccepted":            -1, // sen informes de uso
	"crashReportingEnabled": false,
	"autoUpgradeIntervalH":  0, // a versión de Syncthing vai co cliente
}

// State é o que garda o cliente tras emparellar.
type State struct {
	// Do texto de vinculación: URL de pairsrv.
	PairsrvURL     string    `json:"pairsrvURL,omitempty"`
	User           string    `json:"user,omitempty"`
	Token          string    `json:"token,omitempty"`
	ServerDeviceID string    `json:"serverDeviceID,omitempty"`
	DeviceName     string    `json:"deviceName,omitempty"`
	ResumeAt       time.Time `json:"resumeAt,omitzero"` // pausa temporal
	Stopped        bool      `json:"stopped,omitempty"` // Syncthing detido polo usuario
	// API key do Syncthing que arranca o daemon (xérase a primeira vez).
	SyncthingAPIKey string `json:"syncthingAPIKey,omitempty"`
	// Opcións de Syncthing recibidas de pairsrv ao vincular.
	SyncthingOptions map[string]any `json:"syncthingOptions,omitempty"`
}

func (s State) paired() bool { return s.Token != "" && s.ServerDeviceID != "" && s.PairsrvURL != "" }

type App struct {
	st          *stapi.Client
	statePath   string
	foldersBase string // onde se suxire crear as carpetas locais
	now         func() time.Time

	sup *Supervisor // nil se Syncthing é externo (non o arranca syncply)
	hub *Hub        // avisos de cambio para a web (ver live.go)
	upd updateState // actualizacións automáticas (ver update.go)

	mu    sync.Mutex
	state State
}

// SetSyncthingClient indica o cliente da API de Syncthing (cando non se
// coñece ao crear a App: a API key pode estar no estado).
func (a *App) SetSyncthingClient(st *stapi.Client) { a.st = st }

// SetSupervisor indica que Syncthing o arranca este daemon.
func (a *App) SetSupervisor(sup *Supervisor) { a.sup = sup }

// SyncthingAPIKey devolve a API key do Syncthing propio, xerándoa a primeira vez.
func (a *App) SyncthingAPIKey() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.SyncthingAPIKey == "" {
		a.state.SyncthingAPIKey = randToken()
		if err := a.saveLocked(); err != nil {
			return "", err
		}
	}
	return a.state.SyncthingAPIKey, nil
}

// Stopped indica se o usuario detivo a sincronización.
func (a *App) Stopped() bool { return a.snapshotState().Stopped }

// ApplyOptions aplica as opcións de Syncthing: as seguras por defecto e,
// por riba, as recibidas do servidor ao vincular.
func (a *App) ApplyOptions(ctx context.Context) error {
	opts := maps.Clone(DefaultSyncthingOptions)
	maps.Copy(opts, a.snapshotState().SyncthingOptions)
	return a.st.PatchOptions(ctx, opts)
}

// running comproba que Syncthing estea en marcha antes dunha acción.
func (a *App) running() error {
	if a.sup != nil && !a.sup.Wanted() {
		return userErr("A sincronización está detida. Arráncaa primeiro.")
	}
	return nil
}

// Stop detén Syncthing (só se o arranca este daemon). Lémbrase ao reiniciar.
func (a *App) Stop(ctx context.Context) error {
	if a.sup == nil {
		return userErr("Este cliente non xestiona o proceso de Syncthing.")
	}
	if err := a.sup.Stop(ctx); err != nil {
		return err
	}
	defer a.hub.Notify(ChangeState)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Stopped = true
	return a.saveLocked()
}

func (a *App) Start(ctx context.Context) error {
	if a.sup == nil {
		return userErr("Este cliente non xestiona o proceso de Syncthing.")
	}
	a.sup.Start()
	defer a.hub.Notify(ChangeState)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Stopped = false
	return a.saveLocked()
}

func NewApp(st *stapi.Client, statePath, foldersBase string) (*App, error) {
	a := &App{st: st, statePath: statePath, foldersBase: foldersBase, now: time.Now, hub: NewHub()}
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

// pairsrvHTTP é o cliente HTTP para pairsrv. O certificado valídase coas
// CA do sistema (pairsrv vai detrás dun proxy con HTTPS); só en
// desenvolvemento (SYNCPLY_DEV=1) se permite http.
var pairsrvHTTP = &http.Client{Timeout: 30 * time.Second}

// pairsrv chama á API de pairsrv co servidor gardado ao vincular.
func (a *App) pairsrv(ctx context.Context, method, path, token string, in, out any) error {
	return callPairsrv(ctx, pairsrvHTTP, a.snapshotState().PairsrvURL, method, path, token, in, out)
}

func callPairsrv(ctx context.Context, hc *http.Client, base, method, path, token string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, &body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
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

// LinkCheck é o resultado de comprobar un texto de vinculación pegado, para
// pedirlle confirmación ao usuario antes de enviar o código.
type LinkCheck struct {
	Text     string // o texto tal cal, para o seguinte paso
	Host     string
	User     string
	Relink   bool   // xa vinculado: actualízase a conexión co servidor
	Insecure bool   // pairsrv por http (só en desenvolvemento)
	ReachErr string // baleiro se o servidor responde
}

// checkRelink: estando vinculado, só se acepta un texto do mesmo servidor e
// usuario (para actualizar o enderezo ou o certificado de pairsrv).
func (a *App) checkRelink(d *pairing.LinkData) (bool, error) {
	s := a.snapshotState()
	if !s.paired() {
		return false, nil
	}
	if d.Server != s.ServerDeviceID {
		return true, userErr("Este texto de vinculación é doutro servidor. Este equipo xa está vinculado; para cambiar de servidor hai que reinstalar o cliente.")
	}
	if d.User != s.User {
		return true, userErr("Este equipo está vinculado como «%s» e o texto de vinculación é de «%s».", s.User, d.User)
	}
	return true, nil
}

// CheckLink valida o texto de vinculación e comproba que o servidor responde.
func (a *App) CheckLink(ctx context.Context, text string) (*LinkCheck, error) {
	d, err := pairing.ParseLinkText(text)
	if err != nil {
		return nil, userErr("%s.", upperFirst(err.Error()))
	}
	relink, err := a.checkRelink(d)
	if err != nil {
		return nil, err
	}
	c := &LinkCheck{Text: d.Encode(), Host: d.Host(), User: d.User, Relink: relink,
		Insecure: strings.HasPrefix(d.URL, "http:")}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := callPairsrv(hctx, pairsrvHTTP, d.URL, http.MethodGet, "/healthz", "", nil, nil); err != nil {
		c.ReachErr = err.Error()
	}
	return c, nil
}

// Pair vincula este equipo coa conta do usuario: co servidor e o usuario do
// texto de vinculación, envía o código e o Device ID local a pairsrv e
// engade o servidor como introducer. Se xa está vinculado, actualiza a
// conexión co servidor (enderezo, certificado e token).
func (a *App) Pair(ctx context.Context, linkText, otp, deviceName string) error {
	otp, deviceName = strings.TrimSpace(otp), strings.TrimSpace(deviceName)
	d, err := pairing.ParseLinkText(linkText)
	if err != nil {
		return userErr("%s.", upperFirst(err.Error()))
	}
	if otp == "" || deviceName == "" {
		return userErr("Enche o código de vinculación e o nome do equipo.")
	}
	relink, err := a.checkRelink(d)
	if err != nil {
		return err
	}
	if err := a.running(); err != nil {
		return err
	}
	myID, err := a.st.MyID(ctx)
	if err != nil {
		return err
	}
	var resp pairing.PairResponse
	err = callPairsrv(ctx, pairsrvHTTP, d.URL, http.MethodPost, "/api/v1/pair", "", pairing.PairRequest{
		User: d.User, OTP: otp, DeviceID: myID, DeviceName: deviceName,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.ServerDeviceID != d.Server {
		return fmt.Errorf("o servidor respondeu co Device ID %s e o texto de vinculación indica %s", resp.ServerDeviceID, d.Server)
	}
	dev := stapi.Obj{"name": "Servidor", "addresses": resp.ServerAddresses, "introducer": true}
	if !relink {
		dev["paused"] = false // ao actualizar, respectar a pausa do usuario
	}
	if err := a.st.EnsureDevice(ctx, resp.ServerDeviceID, dev); err != nil {
		return err
	}
	a.mu.Lock()
	a.state.PairsrvURL = d.URL
	a.state.User, a.state.Token, a.state.DeviceName = d.User, resp.Token, deviceName
	a.state.ServerDeviceID = resp.ServerDeviceID
	a.state.SyncthingOptions = resp.SyncthingOptions
	err = a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	a.hub.Notify(ChangeState)
	if err := a.ApplyOptions(ctx); err != nil {
		log.Printf("aplicando opcións de syncthing do servidor: %v", err)
	}
	return nil
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
		return userErr("Fai falta un código de vinculación para revogar un equipo.")
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
	Stopped         bool // Syncthing detido polo usuario
	Starting        bool // Syncthing arrancando (aínda non responde)
	Folders         []FolderView
	Offers          []OfferView
}

func (a *App) Overview(ctx context.Context) (*Overview, error) {
	s := a.snapshotState()
	ov := &Overview{Paired: s.paired(), User: s.User, DeviceName: s.DeviceName, ResumeAt: s.ResumeAt}
	if a.sup != nil && !a.sup.Wanted() {
		ov.Stopped = true
		return ov, nil
	}
	if !ov.Paired {
		ov.Hostname, _ = os.Hostname()
		return ov, nil
	}
	devs, err := a.st.Devices(ctx)
	if err != nil {
		if a.sup != nil && !a.sup.Running() || isUnreachable(err) {
			ov.Starting = true
			return ov, nil
		}
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
	if err := a.running(); err != nil {
		return err
	}
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
	if err := a.running(); err != nil {
		return err
	}
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

// isUnreachable: Syncthing non responde (arrancando ou caído).
func isUnreachable(err error) bool {
	var ne net.Error
	var oe *net.OpError
	return errors.As(err, &oe) || errors.As(err, &ne)
}

// Pause pausa toda a sincronización; con d > 0 retómase soa despois.
func (a *App) Pause(ctx context.Context, d time.Duration) error {
	if err := a.running(); err != nil {
		return err
	}
	if err := a.st.PauseAll(ctx); err != nil {
		return err
	}
	defer a.hub.Notify(ChangeState)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ResumeAt = time.Time{}
	if d > 0 {
		a.state.ResumeAt = a.now().Add(d).UTC()
	}
	return a.saveLocked()
}

func (a *App) Resume(ctx context.Context) error {
	if err := a.running(); err != nil {
		return err
	}
	if err := a.st.ResumeAll(ctx); err != nil {
		return err
	}
	defer a.hub.Notify(ChangeState)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ResumeAt = time.Time{}
	return a.saveLocked()
}

// Tick aplica as tarefas periódicas (fin da pausa temporal).
func (a *App) Tick(ctx context.Context) error {
	s := a.snapshotState()
	if !s.ResumeAt.IsZero() && !a.now().Before(s.ResumeAt) && a.running() == nil {
		return a.Resume(ctx)
	}
	return nil
}
