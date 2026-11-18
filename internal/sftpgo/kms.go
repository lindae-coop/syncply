package sftpgo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

// Secret é un segredo tal como o garda SFTPGo (kms.BaseSecret).
type Secret struct {
	Status         string `json:"status"`
	Payload        string `json:"payload"`
	Key            string `json:"key"`
	AdditionalData string `json:"additional_data"`
	Mode           int    `json:"mode"`
}

// Decrypt reproduce o provedor "local" do KMS de SFTPGo
// (internal/kms/local.go, v2.7.6):
//
//	clave = HKDF-SHA256(ikm, salt=key, info=additional_data)
//	ikm   = sha256(key || additional_data || key)   se mode == 0
//	        clave mestra                            se mode == 1
//	payload = base64(nonce[24] || secretbox)        (gocloud.dev/secrets/localsecrets)
//
// Outros provedores (Vault, AWS, GCP, Azure) non se admiten.
func (s Secret) Decrypt(masterKey string) (string, error) {
	switch s.Status {
	case "Plain":
		return s.Payload, nil
	case "Secretbox":
	case "":
		return "", errors.New("segredo baleiro")
	default:
		return "", fmt.Errorf("provedor KMS non admitido: %q", s.Status)
	}
	key, err := hex.DecodeString(s.Key)
	if err != nil {
		return "", fmt.Errorf("clave do segredo: %w", err)
	}
	var ikm []byte
	switch {
	case s.Mode == 0:
		var b []byte
		b = append(b, key...)
		b = append(b, s.AdditionalData...)
		b = append(b, key...)
		h := sha256.Sum256(b)
		ikm = h[:]
	case masterKey == "":
		return "", errors.New("o segredo está cifrado cunha clave mestra e non se configurou")
	default:
		ikm = []byte(masterKey)
	}
	var info []byte
	if s.AdditionalData != "" {
		info = []byte(s.AdditionalData)
	}
	var derived [32]byte
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, key, info), derived[:]); err != nil {
		return "", err
	}
	box, err := base64.StdEncoding.DecodeString(s.Payload)
	if err != nil {
		return "", fmt.Errorf("payload do segredo: %w", err)
	}
	if len(box) < 24 {
		return "", errors.New("payload do segredo demasiado curto")
	}
	var nonce [24]byte
	copy(nonce[:], box[:24])
	plain, ok := secretbox.Open(nil, box[24:], &nonce, &derived)
	if !ok {
		return "", errors.New("non se puido descifrar o segredo")
	}
	return string(plain), nil
}
