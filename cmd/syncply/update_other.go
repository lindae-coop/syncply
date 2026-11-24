//go:build !windows

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/lindae-coop/syncply/internal/update"
)

func hideWindow(*exec.Cmd) {}

// platformUpdateDisabled: en Linux, o instalado por install.sh (syncply e
// syncthing no mesmo directorio, con permiso de escritura para o usuario).
func platformUpdateDisabled(exe, stBin string) string {
	if runtime.GOOS != "linux" {
		return "non hai actualizacións automáticas para " + runtime.GOOS
	}
	dir := filepath.Dir(exe)
	if filepath.Clean(stBin) != filepath.Join(dir, "syncthing") {
		return "Syncthing non está xunto a syncply (" + stBin + ")"
	}
	if syscall.Access(dir, 2 /* W_OK */) != nil {
		return "sen permiso de escritura en " + dir
	}
	return ""
}

// applyUpdate (Linux): extrae syncply e syncthing do paquete, substitúeos
// (renomear sobre un binario en execución é seguro) e pecha o daemon, que
// volve arrancar co binario novo (reexec). Gárdase a versión anterior como
// syncply.old e syncthing.old.
func applyUpdate(ctx context.Context, u *updater, staged string, m *update.Manifest) error {
	dir := filepath.Dir(u.exe)
	newBins, err := extractBins(staged, dir, "syncply", "syncthing")
	defer func() {
		for _, p := range newBins {
			os.Remove(p)
		}
	}()
	if err != nil {
		return err
	}
	if err := checkBinary(ctx, newBins["syncply"], m.Version); err != nil {
		return err
	}
	for _, name := range []string{"syncthing", "syncply"} {
		cur := filepath.Join(dir, name)
		os.Remove(cur + ".old")
		os.Link(cur, cur+".old") // copia de seguridade (sen erro se non se pode)
		if err := os.Rename(newBins[name], cur); err != nil {
			return err
		}
	}
	u.restart()
	return nil
}

// extractBins extrae do paquete (syncply-linux-amd64/<nome>) os binarios
// indicados a ficheiros temporais en dir.
func extractBins(tgz, dir string, names ...string) (map[string]string, error) {
	f, err := os.Open(tgz)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]string{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, err
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || !want[name] || out[name] != "" ||
			filepath.Dir(filepath.Clean(h.Name)) == "." || h.Size > update.MaxPackage {
			continue
		}
		tmp := filepath.Join(dir, "."+name+".new")
		w, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return out, err
		}
		out[name] = tmp
		_, err = io.Copy(w, io.LimitReader(tr, h.Size))
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return out, err
		}
	}
	for _, n := range names {
		if out[n] == "" {
			return out, fmt.Errorf("o paquete non leva %s", n)
		}
	}
	return out, nil
}

// reexec substitúe este proceso polo binario novo cos mesmos argumentos (o
// PID non cambia: systemd segue vixíandoo).
func reexec(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
