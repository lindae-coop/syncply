package totp

import (
	"testing"
	"time"
)

// Vectores do apéndice B da RFC 6238 (8 díxitos).
func TestRFC6238Vectors(t *testing.T) {
	seeds := map[Algo][]byte{
		SHA1:   []byte("12345678901234567890"),
		SHA256: []byte("12345678901234567890123456789012"),
		SHA512: []byte("1234567890123456789012345678901234567890123456789012345678901234"),
	}
	cases := []struct {
		unix int64
		want map[Algo]string
	}{
		{59, map[Algo]string{SHA1: "94287082", SHA256: "46119246", SHA512: "90693936"}},
		{1111111109, map[Algo]string{SHA1: "07081804", SHA256: "68084774", SHA512: "25091201"}},
		{1111111111, map[Algo]string{SHA1: "14050471", SHA256: "67062674", SHA512: "99943326"}},
		{1234567890, map[Algo]string{SHA1: "89005924", SHA256: "91819424", SHA512: "93441116"}},
		{2000000000, map[Algo]string{SHA1: "69279037", SHA256: "90698825", SHA512: "38618901"}},
		{20000000000, map[Algo]string{SHA1: "65353130", SHA256: "77737706", SHA512: "47863826"}},
	}
	for _, c := range cases {
		for algo, want := range c.want {
			got := HOTP(algo, seeds[algo], Counter(time.Unix(c.unix, 0)), 8)
			if got != want {
				t.Errorf("%s t=%d: %s, quería %s", algo, c.unix, got, want)
			}
		}
	}
	if Algo("md5").Valid() {
		t.Error("md5 non debería ser válido")
	}
}

func TestValidateSkew(t *testing.T) {
	s, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := DecodeSecret(s)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	code := Code(SHA1, secret, now)

	if ctr, ok := Validate(SHA1, secret, code, now, 1); !ok || ctr != Counter(now) {
		t.Fatalf("código actual rexeitado")
	}
	if _, ok := Validate(SHA1, secret, code, now.Add(Step), 1); !ok {
		t.Fatalf("código do paso anterior rexeitado con skew=1")
	}
	if _, ok := Validate(SHA1, secret, code, now.Add(2*Step), 1); ok {
		t.Fatalf("código de hai dous pasos aceptado con skew=1")
	}
	if _, ok := Validate(SHA1, secret, "12345", now, 1); ok {
		t.Fatalf("código curto aceptado")
	}
}
