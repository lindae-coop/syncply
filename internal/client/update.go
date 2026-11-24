package client

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// UpdateStatus é o estado das actualizacións automáticas (as fai o daemon:
// ver cmd/syncply/update.go). Só en memoria.
type UpdateStatus struct {
	Version   string // a deste cliente
	Disabled  string // se non baleiro, motivo polo que non se actualiza só
	LastCheck time.Time
	Latest    string // versión publicada no servidor (na última comprobación)
	Applying  string // versión que se está instalando
	Err       string
}

type updateState struct {
	mu     sync.Mutex
	status UpdateStatus
	check  func(ctx context.Context) (string, error)
}

// SetUpdater indica a versión, se as actualizacións están desactivadas (e
// por que) e a función que comproba e aplica unha actualización.
func (a *App) SetUpdater(version, disabled string, check func(ctx context.Context) (string, error)) {
	a.upd.mu.Lock()
	a.upd.status.Version, a.upd.status.Disabled, a.upd.check = version, disabled, check
	a.upd.mu.Unlock()
}

// UpdateStatus devolve o estado das actualizacións.
func (a *App) UpdateStatus() UpdateStatus {
	a.upd.mu.Lock()
	defer a.upd.mu.Unlock()
	return a.upd.status
}

// SetUpdateStatus modifica o estado e avisa á web.
func (a *App) SetUpdateStatus(f func(*UpdateStatus)) {
	a.upd.mu.Lock()
	f(&a.upd.status)
	a.upd.mu.Unlock()
	a.hub.Notify(ChangeState)
}

// CheckUpdate comproba agora se hai unha versión nova no servidor e, se a
// hai, instálaa. Devolve unha mensaxe para o usuario.
func (a *App) CheckUpdate(ctx context.Context) (string, error) {
	a.upd.mu.Lock()
	check, disabled := a.upd.check, a.upd.status.Disabled
	a.upd.mu.Unlock()
	if disabled != "" || check == nil {
		return "", userErr("As actualizacións automáticas están desactivadas neste equipo: %s.", disabled)
	}
	return check(ctx)
}

// StartUpdateCheck fai o mesmo que CheckUpdate en segundo plano (a
// descarga pode levar máis que unha petición da web); o resultado vese no
// estado.
func (a *App) StartUpdateCheck() error {
	a.upd.mu.Lock()
	check, disabled := a.upd.check, a.upd.status.Disabled
	a.upd.mu.Unlock()
	if disabled != "" || check == nil {
		return userErr("As actualizacións automáticas están desactivadas neste equipo: %s.", disabled)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		check(ctx) // o resultado (ou o erro) queda no estado
	}()
	return nil
}

// PairsrvURL é o servidor co que está vinculado (baleiro se non o está).
func (a *App) PairsrvURL() string {
	s := a.snapshotState()
	if !s.paired() {
		return ""
	}
	return s.PairsrvURL
}

// PairsrvHTTPClient devolve un cliente HTTP coma o de pairsrv (CA do
// sistema) co tempo máximo indicado (as descargas son grandes).
func PairsrvHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: pairsrvHTTP.Transport}
}
