package pairing

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DanBaDo/cld/internal/stapi"
	"github.com/DanBaDo/cld/internal/totp"
)

const (
	srvID = "SERVERA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	devA1 = "AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAA2"
	devA2 = "AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAA3"
	devB1 = "BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBB2"
)

type fakeST struct {
	devices map[string]stapi.Obj
	folders map[string]stapi.Obj
}

func newFakeST() *fakeST {
	return &fakeST{
		devices: map[string]stapi.Obj{srvID: {"deviceID": srvID}},
		folders: map[string]stapi.Obj{},
	}
}

func (f *fakeST) MyID(context.Context) (string, error) { return srvID, nil }
func (f *fakeST) Devices(context.Context) ([]stapi.Obj, error) {
	var out []stapi.Obj
	for _, d := range f.devices {
		out = append(out, d)
	}
	return out, nil
}
func (f *fakeST) EnsureDevice(_ context.Context, id string, p stapi.Obj) error {
	d := stapi.Obj{"deviceID": id}
	for k, v := range p {
		d[k] = v
	}
	f.devices[id] = d
	return nil
}
func (f *fakeST) DeleteDevice(_ context.Context, id string) error { delete(f.devices, id); return nil }
func (f *fakeST) EnsureFolder(_ context.Context, id string, p stapi.Obj) error {
	d := stapi.Obj{"id": id}
	for k, v := range p {
		d[k] = v
	}
	f.folders[id] = d
	return nil
}
func (f *fakeST) Connections(context.Context) (map[string]stapi.Connection, error) {
	return map[string]stapi.Connection{}, nil
}
func (f *fakeST) DeviceStats(context.Context) (map[string]stapi.DeviceStats, error) {
	return map[string]stapi.DeviceStats{}, nil
}

func (f *fakeST) folderDevices(id string) []string {
	var out []string
	for _, d := range f.folders[id]["devices"].([]stapi.Obj) {
		if d["deviceID"] != srvID {
			out = append(out, d["deviceID"].(string))
		}
	}
	slices.Sort(out)
	return out
}

type env struct {
	svc   *Service
	st    *fakeST
	clock time.Time
	sec   map[string][]byte
}

func newEnv(t *testing.T) *env {
	t.Helper()
	secA, _ := totp.GenerateSecret()
	secB, _ := totp.GenerateSecret()
	cfg := &Config{
		ServerAddresses:   []string{"tcp://srv:22000"},
		MaxDevicesPerUser: 2,
		Folders: []Folder{
			{ID: "comun", Label: "Común", Path: "/d/comun"},
			{ID: "alice", Label: "Alice", Path: "/d/alice"},
		},
		Users: []User{
			{Name: "alice", TOTPSecret: secA, Folders: []string{"comun", "alice"}},
			{Name: "bob", TOTPSecret: secB, Folders: []string{"comun"}},
		},
	}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	e := &env{st: newFakeST(), clock: time.Unix(1_700_000_000, 0), sec: map[string][]byte{}}
	for _, u := range cfg.Users {
		e.sec[u.Name] = u.secret
	}
	svc, err := NewService(context.Background(), cfg, e.st, filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return e.clock }
	e.svc = svc
	return e
}

// otp avanza o reloxo un paso e devolve un código novo para o usuario.
func (e *env) otp(user string) string {
	e.clock = e.clock.Add(totp.Step)
	return totp.Code(e.sec[user], e.clock)
}

func (e *env) pair(user, dev string) (*PairResponse, error) {
	return e.svc.Pair(context.Background(), PairRequest{User: user, OTP: e.otp(user), DeviceID: dev, DeviceName: "equipo"})
}

func TestPairSharesFoldersByPermission(t *testing.T) {
	e := newEnv(t)
	resp, err := e.pair("alice", devA1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ServerDeviceID != srvID || resp.Token == "" {
		t.Fatalf("resposta inesperada: %+v", resp)
	}
	if _, err := e.pair("bob", devB1); err != nil {
		t.Fatal(err)
	}
	if got := e.st.folderDevices("comun"); !slices.Equal(got, []string{devA1, devB1}) {
		t.Errorf("comun: %v", got)
	}
	if got := e.st.folderDevices("alice"); !slices.Equal(got, []string{devA1}) {
		t.Errorf("alice: bob non debería ter acceso: %v", got)
	}
}

func TestOTPReplayAndLockout(t *testing.T) {
	e := newEnv(t)
	code := e.otp("alice")
	ctx := context.Background()
	req := PairRequest{User: "alice", OTP: code, DeviceID: devA1, DeviceName: "x"}
	if _, err := e.svc.Pair(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.DeviceID = devA2
	if _, err := e.svc.Pair(ctx, req); !errors.Is(err, ErrBadOTP) {
		t.Fatalf("reutilización do código: %v", err)
	}
	req.OTP = "000000"
	for range maxFailures {
		e.svc.Pair(ctx, req)
	}
	req.OTP = e.otp("alice")
	if _, err := e.svc.Pair(ctx, req); !errors.Is(err, ErrLocked) {
		t.Fatalf("esperaba bloqueo: %v", err)
	}
	e.clock = e.clock.Add(lockDuration)
	if _, err := e.pair("alice", devA2); err != nil {
		t.Fatalf("tras o bloqueo: %v", err)
	}
}

func TestUnknownUserLooksLikeBadOTP(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Pair(context.Background(), PairRequest{User: "eve", OTP: "123456", DeviceID: devA1, DeviceName: "x"})
	if !errors.Is(err, ErrBadOTP) {
		t.Fatal(err)
	}
}

func TestDeviceLimitAndOwnership(t *testing.T) {
	e := newEnv(t)
	first, err := e.pair("alice", devA1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pair("alice", devA2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pair("alice", devB1); !errors.Is(err, ErrDeviceLimit) {
		t.Fatalf("límite: %v", err)
	}
	if _, err := e.pair("bob", devA1); !errors.Is(err, ErrDeviceTaken) {
		t.Fatalf("equipo alleo: %v", err)
	}
	// Volver vincular un equipo propio non conta para o límite e renova o token.
	again, err := e.pair("alice", devA1)
	if err != nil {
		t.Fatalf("revinculación: %v", err)
	}
	if _, err := e.svc.Authenticate(first.Token); !errors.Is(err, ErrUnauthorized) {
		t.Error("o token anterior segue valendo")
	}
	if _, err := e.svc.Authenticate(again.Token); err != nil {
		t.Errorf("token novo: %v", err)
	}
}

func TestRevoke(t *testing.T) {
	e := newEnv(t)
	r1, err := e.pair("alice", devA1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pair("alice", devA2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pair("bob", devB1); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := e.svc.Authenticate(r1.Token)
	if err != nil || c.User != "alice" || c.DeviceID != devA1 {
		t.Fatalf("auth: %+v %v", c, err)
	}
	if err := e.svc.Revoke(ctx, c, devA2, "000000"); !errors.Is(err, ErrBadOTP) {
		t.Fatalf("revogar sen OTP válido: %v", err)
	}
	if err := e.svc.Revoke(ctx, c, devB1, e.otp("alice")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revogar equipo alleo: %v", err)
	}
	if err := e.svc.Revoke(ctx, c, devA2, e.otp("alice")); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.st.devices[devA2]; ok {
		t.Error("o dispositivo revogado segue no servidor")
	}
	if got := e.st.folderDevices("comun"); !slices.Equal(got, []string{devA1, devB1}) {
		t.Errorf("comun: %v", got)
	}
	devs, err := e.svc.ListDevices(ctx, c)
	if err != nil || len(devs) != 1 || !devs[0].Current {
		t.Fatalf("ListDevices: %+v %v", devs, err)
	}
}

func TestReconcileRemovesUnknownDevices(t *testing.T) {
	e := newEnv(t)
	e.st.devices[devB1] = stapi.Obj{"deviceID": devB1}
	if err := e.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.st.devices[devB1]; ok {
		t.Error("dispositivo non rexistrado non eliminado")
	}
	if _, ok := e.st.devices[srvID]; !ok {
		t.Error("eliminouse o propio servidor")
	}
}
