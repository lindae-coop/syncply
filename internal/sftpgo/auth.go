package sftpgo

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/lindae-coop/syncply/internal/pairing"
)

// CheckPassword implementa pairing.PasswordChecker: comproba usuario e
// contrasinal coa API de SFTPGo (GET /api/v2/user/token), así que se aplican
// as súas regras (usuario activo, protocolos permitidos, bloqueos…). otp é o
// código do segundo factor de SFTPGo, só para usuarios que o teñan activado.
// O token que devolve SFTPGo descártase.
func (d *Directory) CheckPassword(ctx context.Context, user, password, otp string) error {
	if d.apiURL == nil {
		return fmt.Errorf("sftpgo: falta apiURL na configuración")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.apiURL.JoinPath("api/v2/user/token").String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, password)
	if otp != "" {
		req.Header.Set("X-SFTPGO-OTP", otp)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("sftpgo: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return pairing.ErrBadLogin
	default:
		return fmt.Errorf("sftpgo: comprobando o contrasinal: HTTP %d", resp.StatusCode)
	}
}
