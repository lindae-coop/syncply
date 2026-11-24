package update

import (
	"crypto/ed25519"
	"log"
	"os"
)

// releaseKeys: claves públicas coas que se asinan as versións publicadas
// (a privada está só nos segredos da CI: SYNCPLY_UPDATE_KEY). Para cambiar de
// clave, engadir a nova aquí e publicar polo menos unha versión asinada coa
// vella antes de retirala: os clientes só aceptan as claves que levan.
var releaseKeys = []string{
	"zW4n3tAFnKp4EmOM8faLqdGDoMrxeGTyRfDFmvhZ6t8=", // 2026-11-24
}

// TrustedKeys devolve as claves coas que o cliente verifica as
// actualizacións. En desenvolvemento (SYNCPLY_DEV=1) acepta tamén a de
// SYNCPLY_UPDATE_PUBKEY, para probar con versións asinadas en local.
func TrustedKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, s := range releaseKeys {
		if k, err := ParsePublicKey(s); err == nil {
			out = append(out, k)
		}
	}
	if os.Getenv("SYNCPLY_DEV") == "1" {
		if s := os.Getenv("SYNCPLY_UPDATE_PUBKEY"); s != "" {
			k, err := ParsePublicKey(s)
			if err != nil {
				log.Printf("SYNCPLY_UPDATE_PUBKEY: %v", err)
			} else {
				out = append(out, k)
			}
		}
	}
	return out
}
