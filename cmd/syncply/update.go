package main

// Actualizacións automáticas desde o servidor ao que está vinculado o
// equipo (ver internal/update): o daemon comproba o manifesto asinado de
// pairsrv cada -update-every, descarga o paquete desta plataforma, verifica
// SHA-256 e que o binario novo responde coa versión anunciada, e
// instálao (applyUpdate, por plataforma).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lindae-coop/syncply/internal/client"
	"github.com/lindae-coop/syncply/internal/update"
)

type updater struct {
	app     *client.App
	uc      *update.Client
	dir     string // <estado>/update: descargas
	exe     string // syncply instalado (este binario)
	restart func() // pecha o daemon para volver arrancar co binario novo

	mu sync.Mutex // unha comprobación de cada vez
}

// updateDisabled devolve por que este daemon non se actualiza só (baleiro
// se pode).
func updateDisabled(noUpdate bool, stURL, stBin, exe string) string {
	switch {
	case noUpdate:
		return "desactivadas coa opción -no-update"
	case func() bool { _, ok := update.ParseVersion(version); return !ok }():
		return "é unha versión de desenvolvemento (" + version + ")"
	case stURL != "":
		return "usa un Syncthing externo"
	case update.PackageFiles[update.PackageKey()] == "":
		return "non hai paquetes para esta plataforma (" + update.PackageKey() + ")"
	}
	return platformUpdateDisabled(exe, stBin)
}

func (u *updater) run(ctx context.Context, every time.Duration) {
	delay := 2 * time.Minute // tras arrancar: deixar que Syncthing se poña ao día
	for {
		// ±20 %: que os equipos dun servidor non pregunten todos á vez.
		d := time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		delay = every
		if u.app.PairsrvURL() == "" {
			continue
		}
		if msg, err := u.check(ctx); err != nil {
			log.Printf("actualización: %v", err)
		} else if msg != "" {
			log.Printf("actualización: %s", msg)
		}
	}
}

// check comproba se hai unha versión nova e, se a hai, instálaa.
func (u *updater) check(ctx context.Context) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	base := u.app.PairsrvURL()
	if base == "" {
		return "", errors.New("este equipo aínda non está vinculado a un servidor")
	}
	fail := func(err error) (string, error) {
		u.app.SetUpdateStatus(func(s *client.UpdateStatus) {
			s.LastCheck, s.Applying, s.Err = time.Now(), "", upperFirst(err.Error())+"."
		})
		return "", err
	}
	m, p, err := u.uc.Check(ctx, base, version)
	if err != nil {
		return fail(err)
	}
	if p == nil {
		u.app.SetUpdateStatus(func(s *client.UpdateStatus) {
			s.LastCheck, s.Latest, s.Applying, s.Err = time.Now(), m.Version, "", ""
		})
		return fmt.Sprintf("xa tes a última versión (%s)", version), nil
	}
	log.Printf("actualización: %s → %s (%s)", version, m.Version, p.File)
	u.app.SetUpdateStatus(func(s *client.UpdateStatus) {
		s.LastCheck, s.Latest, s.Applying, s.Err = time.Now(), m.Version, m.Version, ""
	})
	if err := os.MkdirAll(u.dir, 0o700); err != nil {
		return fail(err)
	}
	u.cleanup()
	staged := filepath.Join(u.dir, m.Version+"-"+p.File)
	if err := u.uc.Download(ctx, base, p, staged); err != nil {
		return fail(err)
	}
	if err := applyUpdate(ctx, u, staged, m); err != nil {
		return fail(fmt.Errorf("instalando %s: %w", m.Version, err))
	}
	// Se en 3 minutos segue este proceso, a instalación non rematou.
	go func() {
		time.Sleep(3 * time.Minute)
		u.app.SetUpdateStatus(func(s *client.UpdateStatus) {
			if s.Applying == m.Version {
				s.Applying, s.Err = "", fmt.Sprintf("Non se completou a instalación de %s; volverase tentar.", m.Version)
			}
		})
	}()
	return fmt.Sprintf("instalando a versión %s", m.Version), nil
}

// cleanup borra as descargas anteriores (en Windows, a que se está
// executando como instalador non se pode borrar: xa se borrará despois).
func (u *updater) cleanup() {
	ents, _ := os.ReadDir(u.dir)
	for _, e := range ents {
		os.RemoveAll(filepath.Join(u.dir, e.Name()))
	}
}

// checkBinary comproba que o binario descargado se executa nesta máquina e
// é a versión anunciada.
func checkBinary(ctx context.Context, path, want string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("o binario novo non se executa: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != "syncply "+want {
		return fmt.Errorf("o binario novo di ser %q, non %s", got, want)
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

// updateCmd: `syncply update` pídelle ao daemon que comprobe agora.
func updateCmd(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "directorio de estado do cliente")
	fs.Parse(args)
	var out struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := controlCallTimeout(*dir, "update", &out, 30*time.Minute); err != nil {
		return err
	}
	if out.Error != "" {
		return errors.New(out.Error)
	}
	info(upperFirst(out.Message) + ".")
	return nil
}
