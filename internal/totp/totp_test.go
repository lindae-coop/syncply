package totp

import (
	"testing"
	"time"
)

// Vectores do apéndice B da RFC 6238 (SHA1, 8 díxitos).
func TestRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		got := HOTP(secret, Counter(time.Unix(c.unix, 0)), 8)
		if got != c.want {
			t.Errorf("t=%d: %s, quería %s", c.unix, got, c.want)
		}
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
	code := Code(secret, now)

	if ctr, ok := Validate(secret, code, now, 1); !ok || ctr != Counter(now) {
		t.Fatalf("código actual rexeitado")
	}
	if _, ok := Validate(secret, code, now.Add(Step), 1); !ok {
		t.Fatalf("código do paso anterior rexeitado con skew=1")
	}
	if _, ok := Validate(secret, code, now.Add(2*Step), 1); ok {
		t.Fatalf("código de hai dous pasos aceptado con skew=1")
	}
	if _, ok := Validate(secret, "12345", now, 1); ok {
		t.Fatalf("código curto aceptado")
	}
}
