package sftpgo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DanBaDo/cld/internal/pairing"
)

func TestCheckPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		switch {
		case r.URL.Path != "/api/v2/user/token":
			http.NotFound(w, r)
		case ok && u == "ana" && p == "boa" && r.Header.Get("X-SFTPGO-OTP") == "":
			w.Write([]byte(`{"access_token":"x"}`))
		case ok && u == "dous" && p == "boa" && r.Header.Get("X-SFTPGO-OTP") == "123456":
			w.Write([]byte(`{"access_token":"x"}`))
		case u == "roto":
			http.Error(w, "", http.StatusInternalServerError)
		default:
			http.Error(w, "", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	d, err := Open(Config{DB: "/non/existe.db", APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := d.CheckPassword(ctx, "ana", "boa", ""); err != nil {
		t.Errorf("correcto: %v", err)
	}
	if err := d.CheckPassword(ctx, "ana", "mala", ""); !errors.Is(err, pairing.ErrBadLogin) {
		t.Errorf("incorrecto: %v", err)
	}
	if err := d.CheckPassword(ctx, "dous", "boa", ""); !errors.Is(err, pairing.ErrBadLogin) {
		t.Errorf("sen segundo factor: %v", err)
	}
	if err := d.CheckPassword(ctx, "dous", "boa", "123456"); err != nil {
		t.Errorf("con segundo factor: %v", err)
	}
	if err := d.CheckPassword(ctx, "roto", "x", ""); err == nil || errors.Is(err, pairing.ErrBadLogin) {
		t.Errorf("erro do servidor: %v", err)
	}
}
