// Package pairing vincula equipos de usuario co servidor Syncthing mediante
// TOTP e mantén a configuración do servidor de acordo co rexistro
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
	ErrBadOTP       = errors.New("usuario ou código OTP incorrecto")
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
	Connections(ctx context.Context) (map[string]stapi.Connection, error)
	DeviceStats(ctx context.Context) (map[string]stapi.DeviceStats, error)
}

type Service struct {
	cfg       *Config
	st        Syncthing
	statePath string
	now       func() time.Time

	mu       sync.Mutex
	state    *State
	serverID string
}

func NewService(ctx context.Context, cfg *Config, st Syncthing, statePath string) (*Service, error) {
	state, err := loadState(statePath)
	if err != nil {
		return nil, err
	}
	id, err := st.MyID(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, st: st, statePath: statePath, now: time.Now, state: state, serverID: id}, nil
}

// checkOTP valida o código, aplica bloqueo por intentos fallidos e rexeita
// códigos xa usados. Debe chamarse co mutex collido; o estado gárdao quen chama.
func (s *Service) checkOTP(u *User, us *UserState, code string) error {
	now := s.now()
	if now.Before(us.LockedUntil) {
		return ErrLocked
	}
	ctr, ok := totp.Validate(u.secret, code, now, otpSkew)
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

	u := s.cfg.user(req.User)
	if u == nil {
		return nil, ErrBadOTP // non revelar se o usuario existe
	}
	us := s.state.userState(u.Name)
	err := s.checkOTP(u, us, req.OTP)
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
	if owner == "" && len(us.Devices) >= s.cfg.MaxDevicesPerUser {
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
	if err := s.reconcileLocked(ctx); err != nil {
		return nil, fmt.Errorf("aplicando configuración en Syncthing: %w", err)
	}
	return &PairResponse{ServerDeviceID: s.serverID, ServerAddresses: s.cfg.ServerAddresses, Token: token}, nil
}

// Caller identifica o equipo autenticado polo seu token.
type Caller struct {
	User     string
	DeviceID string
}

func (s *Service) Authenticate(token string) (*Caller, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, us := range s.state.Users {
		for _, d := range us.Devices {
			if d.TokenHash == h {
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

// Revoke elimina un equipo do usuario. Esixe un código OTP novo.
func (s *Service) Revoke(ctx context.Context, c *Caller, deviceID, otp string) error {
	deviceID = strings.ToUpper(strings.TrimSpace(deviceID))
	s.mu.Lock()
	defer s.mu.Unlock()

	u := s.cfg.user(c.User)
	if u == nil {
		return ErrUnauthorized
	}
	us := s.state.userState(u.Name)
	err := s.checkOTP(u, us, otp)
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
	return s.reconcileLocked(ctx)
}

func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileLocked(ctx)
}

// reconcileLocked deixa o Syncthing do servidor como indica o rexistro:
//   - dispositivos = equipos de todos os usuarios (sobran → fóra);
//   - cada carpeta configurada compártese con todos os equipos dos usuarios
//     con acceso a ela. Isto é o que lles permite aos clientes, vía
//     introducer, coñecer os pares de cada carpeta.
//
// As carpetas de Syncthing que non estean na configuración non se tocan.
func (s *Service) reconcileLocked(ctx context.Context) error {
	type want struct{ name string }
	devices := map[string]want{}
	folderDevs := map[string][]string{}
	for _, u := range s.cfg.Users {
		for _, d := range s.state.userState(u.Name).Devices {
			devices[d.ID] = want{name: u.Name + ": " + d.Name}
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
	for id, w := range devices {
		err := s.st.EnsureDevice(ctx, id, stapi.Obj{
			"name":              w.name,
			"addresses":         []string{"dynamic"},
			"introducer":        false,
			"autoAcceptFolders": false,
		})
		if err != nil {
			return err
		}
	}
	for _, f := range s.cfg.Folders {
		devs := []stapi.Obj{{"deviceID": s.serverID}}
		ids := folderDevs[f.ID]
		slices.Sort(ids)
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
