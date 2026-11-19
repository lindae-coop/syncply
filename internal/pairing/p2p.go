package pairing

// P2P a través dos servidores propios de descubrimento (stdiscosrv) e relay
// (strelaysrv). Os clientes fíxanos polo seu Device ID (?id=...), que se
// calcula do certificado que cada servizo xera ao arrancar.

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// P2POptions configura o descubrimento global e o relay propios.
type P2POptions struct {
	// Servidor de descubrimento: URL (https://host:8443/) e certificado.
	Discovery *P2PServer `json:"discovery,omitempty"`
	// Relay: URL (relay://host:22067/), certificado e token (-token de strelaysrv).
	Relay *P2PServer `json:"relay,omitempty"`
}

type P2PServer struct {
	URL      string `json:"url"`
	CertFile string `json:"certFile"`
	Token    string `json:"token,omitempty"`
}

// ClientOptions devolve as opcións de Syncthing dos clientes para usar estes
// servidores. Agarda (ata que remate ctx) a que existan os certificados: os
// servizos xéranos no primeiro arranque.
func (p *P2POptions) ClientOptions(ctx context.Context) (map[string]any, error) {
	opts := map[string]any{}
	if p.Discovery != nil {
		u, err := p.Discovery.pinnedURL(ctx, "https")
		if err != nil {
			return nil, fmt.Errorf("p2p.discovery: %w", err)
		}
		opts["globalAnnounceEnabled"] = true
		opts["globalAnnounceServers"] = []string{u}
	}
	if p.Relay != nil {
		u, err := p.Relay.pinnedURL(ctx, "relay")
		if err != nil {
			return nil, fmt.Errorf("p2p.relay: %w", err)
		}
		opts["relaysEnabled"] = true
		// Sen "default": nin relays públicos nin escoita en todos os
		// enderezos por defecto; o resto é o mesmo que "default".
		opts["listenAddresses"] = []string{"tcp://0.0.0.0:22000", "quic://0.0.0.0:22000", u}
	}
	if len(opts) > 0 {
		opts["natEnabled"] = true // UPnP/NAT-PMP: máis conexións directas
	}
	return opts, nil
}

func (s *P2PServer) pinnedURL(ctx context.Context, scheme string) (string, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return "", err
	}
	if u.Scheme != scheme || u.Host == "" {
		return "", fmt.Errorf("url %q: ten que ser %s://host:porto/", s.URL, scheme)
	}
	id, err := waitCertID(ctx, s.CertFile)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("id", id)
	if s.Token != "" {
		q.Set("token", s.Token)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func waitCertID(ctx context.Context, path string) (string, error) {
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			return DeviceIDFromPEM(b)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%s: non aparece (%w)", path, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// DeviceIDFromPEM calcula o Device ID de Syncthing dun certificado PEM:
// SHA-256 do DER en base32, cun díxito de control Luhn (mod 32) por cada
// bloque de 13 caracteres, en grupos de 7 separados por guións.
func DeviceIDFromPEM(b []byte) (string, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return "", errors.New("non é un certificado PEM")
	}
	sum := sha256.Sum256(blk.Bytes)
	s := strings.TrimRight(base32.StdEncoding.EncodeToString(sum[:]), "=") // 52
	var l strings.Builder
	for i := range 4 {
		p := s[i*13 : (i+1)*13]
		l.WriteString(p)
		l.WriteByte(luhn32(p))
	}
	id := l.String() // 56
	var out []string
	for i := 0; i < len(id); i += 7 {
		out = append(out, id[i:i+7])
	}
	return strings.Join(out, "-"), nil
}

const luhnBase32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// luhn32 reproduce o de Syncthing (lib/protocol/luhn.go).
func luhn32(s string) byte {
	factor, sum := 1, 0
	for i := range len(s) {
		addend := factor * strings.IndexByte(luhnBase32, s[i])
		if factor == 2 {
			factor = 1
		} else {
			factor = 2
		}
		sum += addend/32 + addend%32
	}
	return luhnBase32[(32-sum%32)%32]
}
