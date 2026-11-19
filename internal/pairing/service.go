// Package pairing vincula equipos de usuario co servidor Syncthing mediante
// un código de vinculación (TOTP) que o usuario ve na web de pairsrv, e mantén a configuración do servidor de acordo co rexistro
// usuario → equipos e cos permisos sobre as carpetas.
package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DanBaDo/cld/internal/stapi"
	"github.com/DanBaDo/cld/internal/totp"
)

const (
	otpSkew      = 1 // ±1 paso (30 s) de desfase de reloxo
	maxFailures  = 5
	lockDuration = 5 * time.Minute
	maxNameLen   = 64
)

var (
	ErrBadOTP       = errors.New("usuario ou código de vinculación incorrecto")
	ErrBadLogin     = errors.New("usuario ou contrasinal incorrecto")
	ErrLocked       = errors.New("demasiados intentos; proba máis tarde")
	ErrBadRequest   = errors.New("petición non válida")
	ErrDeviceLimit  = errors.New("alcanzado o número máximo de equipos")
	ErrDeviceTaken  = errors.New("o equipo xa está vinculado a outro usuario")
	ErrUnauthorized = errors.New("non autorizado")
	ErrNotFound     = errors.New("equipo non atopado")
)

// Formato canónico do Device ID de Syncthing: 8 grupos de 7 caracteres base32.
var deviceIDRe = regexp.MustCompile(`^[A-Z2-7]{7}(-[A-Z2-7]{7}){7}$`)

// Syncthing é o subconxunto da API de Syncthing que usa o servizo.
type Syncthing interface {
	MyID(ctx context.Context) (string, error)
	Devices(ctx context.Context) ([]stapi.Obj, error)
	EnsureDevice(ctx context.Context, id string, patch stapi.Obj) error
	DeleteDevice(ctx context.Context, id string) error
	EnsureFolder(ctx context.Context, id string, patch stapi.Obj) error
	DeleteFolder(ctx context.Context, id string) error
	Connections(ctx context.Context) (map[string]stapi.Connection, error)
	DeviceStats(ctx context.Context) (map[string]stapi.DeviceStats, error)
}

type Service struct {
	opts      Options
	dir       Directory
	st        Syncthing
	statePath string
	now       func() time.Time

	mu       sync.Mutex
	state    *State
	serverID string

	kick chan struct{}
}

func NewService(ctx context.Context, opts Options, dir Directory, st Syncthing, statePath string) (*Service, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	state, err := loadState(statePath)
	if err != nil {
		return nil, err
	}
	id, err := st.MyID(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{opts: opts, dir: dir, st: st, statePath: statePath, now: time.Now, state: state, serverID: id,
		kick: make(chan struct{}, 1)}, nil
}

// RequestReconcile pide unha reconciliación sen agardar por ela. Varias
// peticións seguidas xúntanse nunha soa.
func (s *Service) RequestReconcile() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// ReconcileRequests é a canle pola que chegan as peticións de RequestReconcile.
func (s *Service) ReconcileRequests() <-chan struct{} { return s.kick }

func (s *Service) snapshot(ctx context.Context) (*Snapshot, error) {
	snap, err := s.dir.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("lendo usuarios: %w", err)
	}
	if err := snap.Validate(); err != nil {
		return nil, fmt.Errorf("usuarios: %w", err)
	}
	return snap, nil
}

// checkOTP valida o código, aplica bloqueo por intentos fallidos e rexeita
// códigos xa usados. Debe chamarse co mutex collido; o estado gárdao quen chama.
func (s *Service) checkOTP(us *UserState, code string) error {
	now := s.now()
	if now.Before(us.LockedUntil) {
		return ErrLocked
	}
	var ctr int64
	ok := false
	if secret, err := totp.DecodeSecret(us.Secret); us.Secret != "" && err == nil {
		ctr, ok = totp.Validate(totp.SHA1, secret, code, now, otpSkew)
	}
	if !ok || ctr <= us.LastOTPCounter {
		us.Failures++
		if us.Failures >= maxFailures {
			us.Failures = 0
			us.LockedUntil = now.Add(lockDuration)
		}
		return ErrBadOTP
	}
	us.Failures = 0
	us.LastOTPCounter = ctr
	return nil
}

type PairRequest struct {
	User       string `json:"user"`
	OTP        string `json:"otp"`
	DeviceID   string `json:"deviceID"`
	DeviceName string `json:"deviceName"`
}

type PairResponse struct {
	ServerDeviceID  string   `json:"serverDeviceID"`
	ServerAddresses []string `json:"serverAddresses"`
	Token           string   `json:"token"`
	// Opcións de Syncthing para o cliente (ver Options.SyncthingOptions).
	SyncthingOptions map[string]any `json:"syncthingOptions,omitempty"`
}

// Pair vincula un equipo. Se o equipo xa era do mesmo usuario, renóvase o
// token (p. ex. reinstalación).
func (s *Service) Pair(ctx context.Context, req PairRequest) (*PairResponse, error) {
	devID := strings.ToUpper(strings.TrimSpace(req.DeviceID))
	name := strings.TrimSpace(req.DeviceName)
	if !deviceIDRe.MatchString(devID) || name == "" || len(name) > maxNameLen {
		return nil, ErrBadRequest
	}
	if devID == s.serverID {
		return nil, ErrBadRequest
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	u := snap.user(req.User)
	if u == nil {
		return nil, ErrBadOTP // non revelar se o usuario existe
	}
	us := s.state.userState(u.Name)
	err = s.checkOTP(us, req.OTP)
	if serr := s.state.save(s.statePath); serr != nil {
		return nil, serr
	}
	if err != nil {
		return nil, err
	}

	owner, idx := s.state.owner(devID)
	if owner != "" && owner != u.Name {
		return nil, ErrDeviceTaken
	}
	if owner == "" && len(us.Devices) >= s.opts.MaxDevicesPerUser {
		return nil, ErrDeviceLimit
	}

	token, hash, err := newToken()
	if err != nil {
		return nil, err
	}
	if owner == "" {
		us.Devices = append(us.Devices, Device{ID: devID, Name: name, AddedAt: s.now().UTC(), TokenHash: hash})
	} else {
		us.Devices[idx].Name = name
		us.Devices[idx].TokenHash = hash
	}
	if err := s.state.save(s.statePath); err != nil {
		return nil, err
	}
	if err := s.reconcileLocked(ctx, snap); err != nil {
		return nil, fmt.Errorf("aplicando configuración en Syncthing: %w", err)
	}
	return &PairResponse{ServerDeviceID: s.serverID, ServerAddresses: s.opts.ServerAddresses, Token: token,
		SyncthingOptions: s.opts.SyncthingOptions}, nil
}

// ServerID é o Device ID do Syncthing do servidor.
func (s *Service) ServerID() string { return s.serverID }

// Caller identifica o equipo autenticado polo seu token.
type Caller struct {
	User     string
	DeviceID string
}

// Authenticate identifica o equipo polo seu token. Os equipos de usuarios que
// xa non poden acceder (desactivados ou eliminados en SFTPGo) son rexeitados.
func (s *Service) Authenticate(ctx context.Context, token string) (*Caller, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, us := range s.state.Users {
		for _, d := range us.Devices {
			if d.TokenHash == h {
				snap, err := s.snapshot(ctx)
				if err != nil {
					return nil, err
				}
				if snap.user(name) == nil {
					return nil, ErrUnauthorized
				}
				return &Caller{User: name, DeviceID: d.ID}, nil
			}
		}
	}
	return nil, ErrUnauthorized
}

type DeviceInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AddedAt   time.Time `json:"addedAt"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
	Current   bool      `json:"current"` // é o equipo que fai a petición
}

func (s *Service) ListDevices(ctx context.Context, c *Caller) ([]DeviceInfo, error) {
	s.mu.Lock()
	devs := slices.Clone(s.state.userState(c.User).Devices)
	s.mu.Unlock()

	conns, err := s.st.Connections(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := s.st.DeviceStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, 0, len(devs))
	for _, d := range devs {
		out = append(out, DeviceInfo{
			ID: d.ID, Name: d.Name, AddedAt: d.AddedAt,
			Connected: conns[d.ID].Connected,
			LastSeen:  stats[d.ID].LastSeen,
			Current:   d.ID == c.DeviceID,
		})
	}
	return out, nil
}

// Revoke elimina un equipo do usuario. Esixe un código de vinculación novo.
func (s *Service) Revoke(ctx context.Context, c *Caller, deviceID, otp string) error {
	deviceID = strings.ToUpper(strings.TrimSpace(deviceID))
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.snapshot(ctx)
	if err != nil {
		return err
	}
	u := snap.user(c.User)
	if u == nil {
		return ErrUnauthorized
	}
	us := s.state.userState(u.Name)
	err = s.checkOTP(us, otp)
	if serr := s.state.save(s.statePath); serr != nil {
		return serr
	}
	if err != nil {
		return err
	}
	i := slices.IndexFunc(us.Devices, func(d Device) bool { return d.ID == deviceID })
	if i < 0 {
		return ErrNotFound
	}
	us.Devices = slices.Delete(us.Devices, i, i+1)
	if err := s.state.save(s.statePath); err != nil {
		return err
	}
	return s.reconcileLocked(ctx, snap)
}

// Code é o código de vinculación que se lle mostra ao usuario.
type Code struct {
	Code    string
	Counter int64
	// Ata cando se mostra; despois toca o seguinte. O servidor aínda o acepta
	// un paso máis (desfase de reloxo) se non se usou.
	Until time.Time
}

// PairingCode devolve o código de vinculación que debe ver agora o usuario
// (na web de pairsrv, despois de identificarse en SFTPGo). A primeira vez
// créase o seu segredo. Se o código do paso actual xa se usou, devolve o do
// seguinte, que xa se acepta, para que o que se mostra sempre valla.
func (s *Service) PairingCode(ctx context.Context, user string) (*Code, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if snap.user(user) == nil {
		return nil, ErrUnauthorized
	}
	us := s.state.userState(user)
	if us.Secret == "" {
		if us.Secret, err = totp.GenerateSecret(); err != nil {
			return nil, err
		}
		if err := s.state.save(s.statePath); err != nil {
			return nil, err
		}
	}
	secret, err := totp.DecodeSecret(us.Secret)
	if err != nil {
		return nil, err
	}
	ctr := max(totp.Counter(s.now()), us.LastOTPCounter+1)
	step := int64(totp.Step / time.Second)
	return &Code{
		Code:    totp.HOTP(totp.SHA1, secret, ctr, totp.Digits),
		Counter: ctr,
		Until:   time.Unix((ctr+1)*step, 0),
	}, nil
}

// Account resume para a web o que ten o usuario: recursos que se lle
// ofrecen e equipos vinculados.
type Account struct {
	Folders    []Folder
	Devices    []DeviceInfo
	MaxDevices int
}

func (s *Service) Account(ctx context.Context, user string) (*Account, error) {
	s.mu.Lock()
	snap, err := s.snapshot(ctx)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	u := snap.user(user)
	if u == nil {
		return nil, ErrUnauthorized
	}
	a := &Account{MaxDevices: s.opts.MaxDevicesPerUser}
	for _, id := range u.Folders {
		if f := snap.folder(id); f != nil {
			a.Folders = append(a.Folders, *f)
		}
	}
	if a.Devices, err = s.ListDevices(ctx, &Caller{User: user}); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := s.snapshot(ctx)
	if err != nil {
		return err
	}
	return s.reconcileLocked(ctx, snap)
}

// reconcileLocked deixa o Syncthing do servidor como indican o rexistro e a
// instantánea de usuarios:
//   - dispositivos = equipos dos usuarios con acceso (sobran → fóra);
//   - cada recurso con algún equipo compártese con todos os equipos dos
//     usuarios con acceso a el. Isto é o que lles permite aos clientes, vía
//     introducer, coñecer os pares de cada carpeta;
//   - as carpetas creadas antes por este servizo que xa non teñen equipos
//     quítanse da configuración (os datos non se tocan).
//
// As carpetas de Syncthing que non creou este servizo non se tocan.
func (s *Service) reconcileLocked(ctx context.Context, snap *Snapshot) error {
	devices := map[string]string{} // id → nome
	folderDevs := map[string][]string{}
	for _, u := range snap.Users {
		for _, d := range s.state.userState(u.Name).Devices {
			devices[d.ID] = u.Name + ": " + d.Name
			for _, f := range u.Folders {
				folderDevs[f] = append(folderDevs[f], d.ID)
			}
		}
	}

	current, err := s.st.Devices(ctx)
	if err != nil {
		return err
	}
	for _, d := range current {
		id, _ := d["deviceID"].(string)
		if _, ok := devices[id]; !ok && id != s.serverID {
			if err := s.st.DeleteDevice(ctx, id); err != nil {
				return err
			}
		}
	}
	for id, name := range devices {
		err := s.st.EnsureDevice(ctx, id, stapi.Obj{
			"name":              name,
			"addresses":         []string{"dynamic"},
			"introducer":        false,
			"autoAcceptFolders": false,
		})
		if err != nil {
			return err
		}
	}

	var managed []string
	for _, f := range snap.Folders {
		ids := folderDevs[f.ID]
		if len(ids) == 0 {
			continue
		}
		slices.Sort(ids)
		devs := []stapi.Obj{{"deviceID": s.serverID}}
		for _, id := range ids {
			devs = append(devs, stapi.Obj{"deviceID": id})
		}
		err := s.st.EnsureFolder(ctx, f.ID, stapi.Obj{
			"label":   f.Label,
			"path":    f.Path,
			"type":    "sendreceive",
			"devices": devs,
		})
		if err != nil {
			return err
		}
		managed = append(managed, f.ID)
	}
	for _, id := range s.state.Folders {
		if !slices.Contains(managed, id) {
			if err := s.st.DeleteFolder(ctx, id); err != nil && !errors.Is(err, stapi.ErrNotFound) {
				return err
			}
		}
	}
	slices.Sort(managed)
	if !slices.Equal(managed, s.state.Folders) {
		s.state.Folders = managed
		return s.state.save(s.statePath)
	}
	return nil
}

func newToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}
