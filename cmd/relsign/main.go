// relsign: claves e manifesto asinado das actualizacións do cliente (ver
// internal/update). A clave privada só se usa na CI ao publicar unha
// versión; nunca vai nos servidores.
//
//	relsign keygen -out FICHEIRO
//	    xera unha clave nova (semente en base64, 0600) e mostra a pública,
//	    que hai que engadir a internal/update/keys.go.
//	relsign pubkey -key FICHEIRO
//	    mostra a clave pública dunha clave privada.
//	relsign manifest -key FICHEIRO -version vX.Y.Z PAQUETE...
//	    escribe update.json e update.json.sig no directorio do primeiro
//	    paquete. A clave tamén pode ir en SYNCPLY_UPDATE_KEY.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/lindae-coop/syncply/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "pubkey":
		err = pubkey(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "relsign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "uso: relsign keygen|pubkey|manifest [opcións]  (-h para axuda)")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "ficheiro da clave privada (non se sobrescribe)")
	fs.Parse(args)
	if *out == "" {
		return errors.New("falta -out")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, update.EncodeKey(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println(update.EncodeKey(pub))
	return nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	s := os.Getenv("SYNCPLY_UPDATE_KEY")
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		s = string(b)
	}
	if s == "" {
		return nil, errors.New("falta a clave (-key ou SYNCPLY_UPDATE_KEY)")
	}
	return update.ParsePrivateKey(s)
}

func pubkey(args []string) error {
	fs := flag.NewFlagSet("pubkey", flag.ExitOnError)
	keyPath := fs.String("key", "", "clave privada")
	fs.Parse(args)
	k, err := loadKey(*keyPath)
	if err != nil {
		return err
	}
	fmt.Println(update.EncodeKey(k.Public().(ed25519.PublicKey)))
	return nil
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	keyPath := fs.String("key", "", "clave privada (ou SYNCPLY_UPDATE_KEY)")
	version := fs.String("version", "", "versión publicada (vX.Y.Z)")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("indica os paquetes")
	}
	k, err := loadKey(*keyPath)
	if err != nil {
		return err
	}
	files := map[string]string{}
	for _, p := range fs.Args() {
		plat := ""
		for pl, name := range update.PackageFiles {
			if filepath.Base(p) == name {
				plat = pl
			}
		}
		if plat == "" {
			return fmt.Errorf("%s: non é un paquete coñecido", p)
		}
		files[plat] = p
	}
	man, err := update.BuildManifest(*version, files)
	if err != nil {
		return err
	}
	pub := k.Public().(ed25519.PublicKey)
	if !slices.ContainsFunc(update.TrustedKeys(), func(k ed25519.PublicKey) bool { return k.Equal(pub) }) {
		fmt.Fprintln(os.Stderr, "aviso: esta clave non está en internal/update/keys.go: os clientes publicados non aceptarán esta sinatura")
	}
	dir := filepath.Dir(fs.Arg(0))
	if err := os.WriteFile(filepath.Join(dir, update.ManifestFile), man, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, update.SigFile), update.Sign(k, man), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s asinado (%s)\n", filepath.Join(dir, update.ManifestFile), *version)
	return nil
}
