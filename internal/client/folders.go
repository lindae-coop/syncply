package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/lindae-coop/syncply/internal/stapi"
)

// checkOverlap: a carpeta local dun recurso non pode coincidir con outra
// que xa se sincroniza nin estar dentro dela (nin contela). except é o ID
// do propio recurso ao movelo.
func checkOverlap(folders []stapi.Obj, path, except string) error {
	for _, f := range folders {
		if id, _ := f["id"].(string); id == except {
			continue
		}
		p, _ := f["path"].(string)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if p == path || isInside(path, p) || isInside(p, path) {
			label, _ := f["label"].(string)
			return userErr("Esa carpeta coincide ou está dentro doutra que xa se sincroniza (%s).", label)
		}
	}
	return nil
}

// isInside indica se path está dentro de dir (sen ser el).
func isInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (a *App) cleanLocalPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", userErr("A carpeta local ten que ser unha ruta completa (p. ex. %s).", a.suggestPath("Documentos"))
	}
	return filepath.Clean(path), nil
}

// Tempo máximo para que Syncthing deteña unha carpeta pausada.
var pauseWait = 10 * time.Second

// Move cambia a carpeta local dun recurso xa aceptado movendo os ficheiros
// (non se descargan de novo). Mentres, o recurso queda en pausa.
//
// Só se cambia a ruta en Syncthing despois de mover: se apuntase a unha
// carpeta baleira, Syncthing tomaría os ficheiros como borrados e enviaría
// os borrados aos outros equipos.
func (a *App) Move(ctx context.Context, id, path string) error {
	if err := a.running(); err != nil {
		return err
	}
	dst, err := a.cleanLocalPath(path)
	if err != nil {
		return err
	}
	f, err := a.st.Folder(ctx, id)
	if errors.Is(err, stapi.ErrNotFound) {
		return userErr("Ese recurso xa non se sincroniza neste equipo.")
	}
	if err != nil {
		return err
	}
	src, _ := f["path"].(string)
	src = filepath.Clean(src)
	switch {
	case dst == src:
		return userErr("Esa xa é a carpeta do recurso.")
	case isInside(dst, src):
		return userErr("A carpeta nova non pode estar dentro da actual.")
	}
	folders, err := a.st.Folders(ctx)
	if err != nil {
		return err
	}
	if err := checkOverlap(folders, dst, id); err != nil {
		return err
	}
	if _, err := os.Stat(src); err != nil {
		return userErr("Non se atopa a carpeta actual (%s): %v.", src, err)
	}
	// O destino non pode existir, salvo como carpeta baleira (bórrase para
	// poder renomear; recréase se algo falla).
	removedEmpty := false
	switch entries, err := os.ReadDir(dst); {
	case err == nil && len(entries) > 0:
		return userErr("A carpeta %s xa existe e non está baleira. Escolle unha nova ou baleira.", dst)
	case err == nil:
		if err := os.Remove(dst); err != nil {
			return userErr("Non se pode usar %s: %v.", dst, err)
		}
		removedEmpty = true
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return userErr("Non se pode crear %s: %v.", filepath.Dir(dst), err)
		}
	default:
		return userErr("Non se pode usar %s: %v.", dst, err)
	}
	restore := func() {
		if removedEmpty {
			_ = os.Mkdir(dst, 0o755)
		}
	}

	if err := a.st.EnsureFolder(ctx, id, stapi.Obj{"paused": true}); err != nil {
		restore()
		return err
	}
	defer a.hub.Notify(ChangeState)
	unpause := func() { _ = a.st.EnsureFolder(context.WithoutCancel(ctx), id, stapi.Obj{"paused": false}) }
	a.waitPaused(ctx, id)
	if err := renameRetry(ctx, src, dst); err != nil {
		restore()
		unpause()
		if isCrossDevice(err) {
			return userErr("A carpeta nova está noutra unidade e non se pode mover directamente. Podes deixar de sincronizar o recurso e volver aceptalo na carpeta nova (descargarase de novo; os ficheiros actuais quedan en %s).", src)
		}
		return userErr("Non se puido mover a carpeta: %v.", err)
	}
	if err := a.st.EnsureFolder(ctx, id, stapi.Obj{"path": dst, "paused": false}); err != nil {
		// Volver deixalo como estaba.
		if rerr := os.Rename(dst, src); rerr != nil {
			return userErr("Moveuse a carpeta a %s pero non se puido cambiar en Syncthing (%v) nin volver movela (%v). Avisa a quen administre o servidor.", dst, err, rerr)
		}
		restore()
		unpause()
		return err
	}
	return nil
}

// waitPaused agarda (como moito pauseWait) a que Syncthing deteña a
// carpeta: en Windows non se pode renomear mentres ten ficheiros abertos.
// Syncthing 2.1.7 devolve o estado baleiro para unha carpeta en pausa.
func (a *App) waitPaused(ctx context.Context, id string) {
	deadline := time.Now().Add(pauseWait)
	for time.Now().Before(deadline) {
		st, err := a.st.FolderStatus(ctx, id)
		if err != nil || st.State == "paused" || st.State == "" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// renameRetry: en Windows o renomeado pode fallar uns instantes mentres o
// sistema (antivirus, indexador) ten ficheiros abertos.
func renameRetry(ctx context.Context, src, dst string) error {
	var err error
	for range 10 {
		if err = os.Rename(src, dst); err == nil || isCrossDevice(err) || errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(300 * time.Millisecond):
		}
	}
	return err
}

func isCrossDevice(err error) bool {
	var le *os.LinkError
	if !errors.As(err, &le) {
		return false
	}
	if runtime.GOOS == "windows" {
		return le.Err == syscall.Errno(17) // ERROR_NOT_SAME_DEVICE
	}
	return errors.Is(le.Err, syscall.EXDEV)
}

// --- Explorador de carpetas ---------------------------------------------------

// O navegador non dá rutas completas de carpetas, así que a web local ten
// o seu propio explorador: lista as subcarpetas dunha carpeta e devolve a
// ruta escollida á páxina principal.

type Browse struct {
	Dir     string
	Parent  string // baleiro na raíz
	Subdirs []string
	Roots   []string // cartafol persoal e unidades
	Err     string
	More    bool // máis subcarpetas das que se amosan
	Empty   bool // sen ningunha entrada (nin ficheiros nin ocultas)
}

const browseMax = 500

// BrowseDir lista as subcarpetas visibles de dir (por defecto, o cartafol
// persoal).
func (a *App) BrowseDir(dir string) *Browse {
	home, _ := os.UserHomeDir()
	b := &Browse{Roots: browseRoots(home)}
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		dir = home
	}
	if dir == "" {
		dir = string(filepath.Separator)
	}
	b.Dir = filepath.Clean(dir)
	if p := filepath.Dir(b.Dir); p != b.Dir {
		b.Parent = p
	}
	entries, err := os.ReadDir(b.Dir)
	if err != nil {
		b.Err = err.Error()
		return b
	}
	b.Empty = len(entries) == 0
	for _, e := range entries {
		name := e.Name()
		if hiddenName(name) {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(b.Dir, name)); err == nil && fi.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		if len(b.Subdirs) == browseMax {
			b.More = true
			break
		}
		b.Subdirs = append(b.Subdirs, name)
	}
	slices.SortFunc(b.Subdirs, func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) })
	return b
}

// hiddenName: carpetas que non interesan para gardar recursos.
func hiddenName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") ||
		name == "System Volume Information" || name == "lost+found"
}

// pickedPath une a carpeta escollida e o nome da subcarpeta (opcional).
func pickedPath(dir, name string) (string, bool) {
	dir, name = strings.TrimSpace(dir), strings.TrimSpace(name)
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	if name == "" {
		return filepath.Clean(dir), true
	}
	if strings.ContainsAny(name, `/\:`) || name == "." || name == ".." {
		return "", false
	}
	return filepath.Join(dir, name), true
}

// MakeDir crea a subcarpeta name dentro de dir (botón «Nova carpeta» do
// explorador) e devolve a súa ruta.
func (a *App) MakeDir(dir, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", userErr("Escribe o nome da carpeta nova.")
	}
	path, ok := pickedPath(dir, name)
	if !ok {
		return "", userErr("O nome da carpeta non pode levar «/», «\\» nin «:».")
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", userErr("Xa existe «%s» nesta carpeta.", filepath.Base(path))
		}
		return "", userErr("Non se puido crear a carpeta: %v", err)
	}
	return path, nil
}

// dirEmpty: a carpeta non existe ou non ten ningunha entrada.
func dirEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil && errors.Is(err, os.ErrNotExist) || err == nil && len(entries) == 0
}
