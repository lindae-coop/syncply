package sftpgo

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"testing"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

// Segredo TOTP gardado por SFTPGo 2.7.6 (POST /api/v2/user/totp/save do
// usuario "proba"), copiado da súa BD.
func TestDecryptSFTPGoVector(t *testing.T) {
	s := Secret{
		Status:         "Secretbox",
		Payload:        "7NxO9jd6gfty19kPVgswtX3Oez3Q8SlOXVnKUmIKP49CVuHyPVTvj+cVFab+cJ7F0+DBkipD5xT9dJp1rHqbcM2WlKUHGwSA",
		Key:            "497de61470c09fdc7bc63bd2ff00b17b2b102aff244137c6295a47f0a2910d22",
		AdditionalData: "proba",
	}
	got, err := s.Decrypt("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "S3RZ2OZWS4PTKU2ZAOFSJ567FM6YUIEP" {
		t.Fatalf("descifrado: %q", got)
	}
	s.AdditionalData = "outro"
	if _, err := s.Decrypt(""); err == nil {
		t.Fatal("descifrou con additional_data incorrecto")
	}
}

// encryptWithMaster cifra como SFTPGo cando ten clave mestra (mode 1).
func encryptWithMaster(t *testing.T, plain, ad, master string) Secret {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	var derived [32]byte
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(master), key, []byte(ad)), derived[:]); err != nil {
		t.Fatal(err)
	}
	var nonce [24]byte
	rand.Read(nonce[:])
	box := secretbox.Seal(nonce[:], []byte(plain), &nonce, &derived)
	return Secret{Status: "Secretbox", Payload: base64.StdEncoding.EncodeToString(box),
		Key: hex.EncodeToString(key), AdditionalData: ad, Mode: 1}
}

func TestDecryptMasterKey(t *testing.T) {
	s := encryptWithMaster(t, "SEGREDO", "alice", "clave-mestra")
	if _, err := s.Decrypt(""); err == nil {
		t.Fatal("descifrou sen clave mestra")
	}
	if got, err := s.Decrypt("clave-mestra"); err != nil || got != "SEGREDO" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestDecryptUnsupported(t *testing.T) {
	if _, err := (Secret{Status: "VaultTransit", Payload: "x"}).Decrypt(""); err == nil {
		t.Fatal("aceptou un provedor non admitido")
	}
	if got, _ := (Secret{Status: "Plain", Payload: "x"}).Decrypt(""); got != "x" {
		t.Fatal("Plain")
	}
}
