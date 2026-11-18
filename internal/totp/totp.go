// Package totp implementa TOTP (RFC 6238) cos parámetros habituais das apps
// autenticadoras (pasos de 30 s e 6 díxitos) e os algoritmos que admite
// SFTPGo: HMAC-SHA1 (por defecto), SHA256 e SHA512.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"time"
)

// Algo é o algoritmo HMAC, cos mesmos nomes que a configuración de SFTPGo.
type Algo string

const (
	SHA1   Algo = "sha1"
	SHA256 Algo = "sha256"
	SHA512 Algo = "sha512"
)

func (a Algo) hash() (func() hash.Hash, error) {
	switch strings.ToLower(string(a)) {
	case "", string(SHA1):
		return sha1.New, nil
	case string(SHA256):
		return sha256.New, nil
	case string(SHA512):
		return sha512.New, nil
	}
	return nil, fmt.Errorf("algoritmo TOTP non admitido: %q", a)
}

// Valid indica se o algoritmo está admitido.
func (a Algo) Valid() bool { _, err := a.hash(); return err == nil }

const (
	Step   = 30 * time.Second
	Digits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret devolve un segredo aleatorio de 160 bits en base32.
func GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// DecodeSecret acepta base32 con ou sen recheo, espazos e minúsculas.
func DecodeSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	s = strings.TrimRight(s, "=")
	b, err := b32.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("segredo TOTP non válido: %w", err)
	}
	if len(b) < 10 {
		return nil, fmt.Errorf("segredo TOTP demasiado curto (%d bytes)", len(b))
	}
	return b, nil
}

// Counter devolve o número de paso correspondente a t.
func Counter(t time.Time) int64 { return t.Unix() / int64(Step/time.Second) }

// HOTP calcula o código para un contador (RFC 4226) con n díxitos. Un
// algoritmo non admitido trátase como SHA1 (compróbese antes con Valid).
func HOTP(algo Algo, secret []byte, counter int64, n int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	h, err := algo.hash()
	if err != nil {
		h = sha1.New
	}
	m := hmac.New(h, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range n {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", n, v%mod)
}

// Code devolve o código TOTP de 6 díxitos para o instante t.
func Code(algo Algo, secret []byte, t time.Time) string {
	return HOTP(algo, secret, Counter(t), Digits)
}

// Validate comproba code no instante now admitindo ±skew pasos de desfase de
// reloxo. Devolve o contador que coincidiu, para que quen chama poida
// rexeitar a reutilización do mesmo código.
func Validate(algo Algo, secret []byte, code string, now time.Time, skew int) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != Digits || !algo.Valid() {
		return 0, false
	}
	c := Counter(now)
	for d := -skew; d <= skew; d++ {
		want := HOTP(algo, secret, c+int64(d), Digits)
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c + int64(d), true
		}
	}
	return 0, false
}

// URI devolve o enderezo otpauth:// para dar de alta o segredo nunha app
// autenticadora (normalmente como código QR).
func URI(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	label := url.PathEscape(issuer + ":" + account)
	return "otpauth://totp/" + label + "?" + v.Encode()
}
