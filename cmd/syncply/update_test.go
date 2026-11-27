//go:build !windows

package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTgz(t *testing.T, path string, files map[string]string) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	tw := tar.NewWriter(zw)
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	zw.Close()
	f.Close()
}

func TestExtractBins(t *testing.T) {
	dir := t.TempDir()
	tgz := filepath.Join(dir, "p.tar.gz")
	// Paquete actual: só syncply, na raíz.
	writeTgz(t, tgz, map[string]string{"syncply": "novo syncply"})
	got, err := extractBins(tgz, dir, "syncply")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got["syncply"]); string(b) != "novo syncply" {
		t.Errorf("syncply: %q", b)
	}

	// Formato anterior (o que esperan os clientes ata v0.4.1).
	writeTgz(t, tgz, map[string]string{
		"syncply-linux-amd64/syncply":    "novo syncply",
		"syncply-linux-amd64/syncthing":  "novo syncthing",
		"syncply-linux-amd64/install.sh": "#!/bin/sh",
	})
	got, err = extractBins(tgz, dir, "syncply", "syncthing")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"syncply": "novo syncply", "syncthing": "novo syncthing"} {
		b, _ := os.ReadFile(got[name])
		if string(b) != want {
			t.Errorf("%s: %q", name, b)
		}
	}

	writeTgz(t, tgz, map[string]string{"syncply-linux-amd64/install.sh": "x"})
	if _, err := extractBins(tgz, dir, "syncply"); err == nil {
		t.Fatal("aceptou un paquete sen syncply")
	}
}

func TestPlatformUpdateDisabled(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "syncply")
	if r := platformUpdateDisabled(exe, filepath.Join(dir, "syncthing")); r != "" {
		t.Fatalf("instalación normal desactivada: %s", r)
	}
	if r := platformUpdateDisabled(exe, "/usr/bin/syncthing"); r == "" {
		t.Fatal("Syncthing noutro directorio non desactiva")
	}
}
