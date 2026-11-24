// Package update: actualizacións do cliente syncply a través do servidor.
//
// Cada versión publica, xunto aos paquetes do cliente (/descargas/ de
// pairsrv), un manifesto asinado:
//
//	update.json      {"product":"syncply","version":"vX.Y.Z","packages":{"linux-amd64":{file,sha256,size},...}}
//	update.json.sig  sinatura Ed25519 (base64) de sigContext + update.json
//
// A clave privada é do proxecto (segredo da CI), non do operador do
// servidor: o cliente é xenérico e leva compiladas as claves públicas
// (TrustedKeys). Así un servidor comprometido non pode distribuír binarios
// propios, só reter actualizacións ou servir versións asinadas antigas (que
// o cliente rexeita: nunca baixa de versión).
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	Product      = "syncply"
	ManifestFile = "update.json"
	SigFile      = "update.json.sig"
	// sigContext sepárao doutros usos posibles da mesma clave.
	sigContext = "syncply-update-v1\n"
	// MaxPackage: tamaño máximo dun paquete (Syncthing + syncply ~ 40 MB).
	MaxPackage  = 256 << 20
	maxManifest = 64 << 10
)

// Manifest describe unha versión publicada.
type Manifest struct {
	Product  string             `json:"product"`
	Version  string             `json:"version"`
	Packages map[string]Package `json:"packages"` // clave: GOOS-GOARCH
}

type Package struct {
	File   string `json:"file"` // nome en /descargas/
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Platform é a clave do paquete deste binario.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// PackageFiles: nome do paquete de cada plataforma (os mesmos que fan
// `make dist`, `make dist-windows` e deploy/Containerfile.pairsrv).
var PackageFiles = map[string]string{
	"linux-amd64":   "syncply-linux-amd64.tar.gz",
	"windows-amd64": "syncply-windows-amd64.exe",
}

// --- Versións ----------------------------------------------------------------

var releaseRE = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// ParseVersion le unha versión publicada (vX.Y.Z). As compilacións de
// desenvolvemento ("dev", "v0.1.0-3-gabc", "-dirty") non o son.
func ParseVersion(v string) ([3]int, bool) {
	m := releaseRE.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// Newer indica se candidate é unha versión publicada posterior a current.
func Newer(candidate, current string) bool {
	a, ok1 := ParseVersion(candidate)
	b, ok2 := ParseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// --- Sinatura ----------------------------------------------------------------

// ParsePublicKey le unha clave pública Ed25519 en base64.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("clave pública non válida")
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey le unha clave privada (semente Ed25519 de 32 bytes en base64).
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, errors.New("clave privada non válida")
	}
	return ed25519.NewKeyFromSeed(b), nil
}

func EncodeKey(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// Sign asina o manifesto (os bytes exactos de update.json).
func Sign(key ed25519.PrivateKey, manifest []byte) []byte {
	sig := ed25519.Sign(key, append([]byte(sigContext), manifest...))
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

// Verify comproba a sinatura cunha das claves e devolve o manifesto.
func Verify(keys []ed25519.PublicKey, manifest, sig []byte) (*Manifest, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return nil, errors.New("sinatura con formato incorrecto")
	}
	msg := append([]byte(sigContext), manifest...)
	ok := false
	for _, k := range keys {
		if ed25519.Verify(k, msg, s) {
			ok = true
			break
		}
	}
	if !ok {
		return nil, errors.New("sinatura non válida")
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("manifesto: %w", err)
	}
	if m.Product != Product {
		return nil, fmt.Errorf("manifesto doutro produto (%q)", m.Product)
	}
	if _, ok := ParseVersion(m.Version); !ok {
		return nil, fmt.Errorf("manifesto con versión non válida (%q)", m.Version)
	}
	return &m, nil
}

// BuildManifest crea o manifesto dunha versión cos paquetes indicados
// (plataforma → ruta).
func BuildManifest(version string, files map[string]string) ([]byte, error) {
	if _, ok := ParseVersion(version); !ok {
		return nil, fmt.Errorf("versión %q: só se asinan versións publicadas (vX.Y.Z)", version)
	}
	m := Manifest{Product: Product, Version: version, Packages: map[string]Package{}}
	for plat, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		m.Packages[plat] = Package{File: filepath.Base(path), SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
	}
	b, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// --- Cliente -----------------------------------------------------------------

// Client consulta as actualizacións no servidor (pairsrv) co que está
// vinculado o equipo.
type Client struct {
	HTTP     *http.Client
	Keys     []ed25519.PublicKey
	Platform string
}

// ErrNoManifest: o servidor non publica actualizacións (sen descargas ou
// imaxe construída sen clave).
var ErrNoManifest = errors.New("o servidor non publica actualizacións")

// Check devolve a actualización dispoñible para esta plataforma, ou nil se
// non hai unha versión posterior a current.
func (c *Client) Check(ctx context.Context, base, current string) (*Manifest, *Package, error) {
	if len(c.Keys) == 0 {
		return nil, nil, errors.New("este cliente non ten claves para verificar actualizacións")
	}
	man, err := c.get(ctx, base, ManifestFile, maxManifest)
	if err != nil {
		return nil, nil, err
	}
	sig, err := c.get(ctx, base, SigFile, 1<<10)
	if err != nil {
		return nil, nil, err
	}
	m, err := Verify(c.Keys, man, sig)
	if err != nil {
		return nil, nil, err
	}
	if !Newer(m.Version, current) {
		return m, nil, nil
	}
	p, ok := m.Packages[c.Platform]
	if !ok {
		return m, nil, nil
	}
	if p.File == "" || p.File != filepath.Base(p.File) || strings.ContainsAny(p.File, `/\`) ||
		len(p.SHA256) != 64 || p.Size <= 0 || p.Size > MaxPackage {
		return nil, nil, fmt.Errorf("manifesto: paquete %s non válido", c.Platform)
	}
	return m, &p, nil
}

// Download descarga o paquete en dst e comproba tamaño e SHA-256. Se falla,
// non deixa nada en dst.
func (c *Client) Download(ctx context.Context, base string, p *Package, dst string) error {
	resp, err := c.open(ctx, base, p.File)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, p.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n != p.Size:
		err = fmt.Errorf("%s: tamaño %d, esperado %d", p.File, n, p.Size)
	case hex.EncodeToString(h.Sum(nil)) != strings.ToLower(p.SHA256):
		err = fmt.Errorf("%s: o SHA-256 non coincide co do manifesto", p.File)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (c *Client) open(ctx context.Context, base, file string) (*http.Response, error) {
	u := strings.TrimRight(base, "/") + "/descargas/" + url.PathEscape(file)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound && (file == ManifestFile || file == SigFile) {
			return nil, ErrNoManifest
		}
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp, nil
}

func (c *Client) get(ctx context.Context, base, file string, max int64) ([]byte, error) {
	resp, err := c.open(ctx, base, file)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: demasiado grande", file)
	}
	return b, nil
}
