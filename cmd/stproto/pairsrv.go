package main

// Cliente de pairsrv: o que fará o cliente real (emparellar co código, gardar o
// token, listar e revogar equipos).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DanBaDo/cld/internal/pairing"
)

const pairsrvURL = "http://pairsrv:8080"

type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return fmt.Sprintf("pairsrv %d: %s", e.Code, e.Msg) }

func isStatus(err error, code int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Code == code
}

func pairsrv(ctx context.Context, method, path, token string, in, out any) error {
	var body *bytes.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, pairsrvURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return &apiError{Code: resp.StatusCode, Msg: e.Error}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func waitPairsrv(ctx context.Context) error {
	return waitFor(ctx, "pairsrv dispoñible", func() (bool, error) {
		return pairsrv(ctx, http.MethodGet, "/healthz", "", nil, nil) == nil, nil
	})
}

// --- Código de vinculación -------------------------------------------------
//
// Fai o que fai o usuario: entra na web de pairsrv co seu usuario e
// contrasinal de SFTPGo e le o código que se mostra. A web sempre mostra un
// código aínda non usado.

var codeRe = regexp.MustCompile(`id="code">(\d{6})<`)

func devOTP(ctx context.Context, user string) (string, error) {
	d, err := loadDemo()
	if err != nil {
		return "", err
	}
	u := d.user(user)
	if u == nil {
		// Usuario descoñecido: calquera código vale para probar o rexeitamento.
		return "000000", nil
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	form := url.Values{"user": {u.Name}, "password": {u.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pairsrvURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req) // segue a redirección a /
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	m := codeRe.FindSubmatch(b)
	if resp.StatusCode != http.StatusOK || m == nil {
		return "", fmt.Errorf("web de pairsrv: sen código para %s (HTTP %d)", user, resp.StatusCode)
	}
	return string(m[1]), nil
}

// cmdOTP amosa o código de vinculación actual para usar a man (p. ex. na web
// de cld).
func cmdOTP(ctx context.Context, user string) error {
	code, err := devOTP(ctx, user)
	if err != nil {
		return err
	}
	fmt.Printf("%s  (código de vinculación de %s; válido uns 30-60 s)\n", code, user)
	return nil
}

// --- Token do equipo --------------------------------------------------------

func tokenPath(client string) string { return filepath.Join(nodesMount, client, "pairsrv-token") }

func loadToken(client string) (string, error) {
	b, err := os.ReadFile(tokenPath(client))
	if err != nil {
		return "", fmt.Errorf("%s non está emparellado: %w", client, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// --- Ordes ------------------------------------------------------------------

// cmdPair fai o que fará o instalador do cliente: envía usuario + código + o seu
// Device ID a pairsrv e engade o servidor como introducer cos datos da resposta.
func cmdPair(ctx context.Context, client, user, otp string) error {
	cli := node(client)
	cliID, err := cli.MyID(ctx)
	if err != nil {
		return err
	}
	if otp == "" {
		if otp, err = devOTP(ctx, user); err != nil {
			return err
		}
	}
	var resp pairing.PairResponse
	err = pairsrv(ctx, http.MethodPost, "/api/v1/pair", "", pairing.PairRequest{
		User: user, OTP: otp, DeviceID: cliID, DeviceName: client,
	}, &resp)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tokenPath(client), []byte(resp.Token+"\n"), 0o600); err != nil {
		return err
	}
	if err := cli.EnsureDevice(ctx, resp.ServerDeviceID, map[string]any{
		"name":       "servidor",
		"addresses":  resp.ServerAddresses,
		"introducer": true,
	}); err != nil {
		return err
	}
	fmt.Printf("%s emparellado como %s (servidor %s)\n", client, user, short(resp.ServerDeviceID))
	return nil
}

func listDevices(ctx context.Context, client string) ([]pairing.DeviceInfo, error) {
	tok, err := loadToken(client)
	if err != nil {
		return nil, err
	}
	var devs []pairing.DeviceInfo
	err = pairsrv(ctx, http.MethodGet, "/api/v1/devices", tok, nil, &devs)
	return devs, err
}

func cmdDevices(ctx context.Context, client string) error {
	devs, err := listDevices(ctx, client)
	if err != nil {
		return err
	}
	fmt.Printf("equipos vinculados (%d), vistos desde %s:\n", len(devs), client)
	for _, d := range devs {
		cur, conn := "", "desconectado"
		if d.Current {
			cur = " (este)"
		}
		if d.Connected {
			conn = "conectado"
		}
		fmt.Printf("  %-11s %s  %s  alta %s%s\n", d.Name, short(d.ID), conn, d.AddedAt.Format(time.DateTime), cur)
	}
	return nil
}

// cmdRevoke: desde o equipo "from", o usuario revoga o equipo "target" cun
// código de vinculación novo.
func cmdRevoke(ctx context.Context, from, target, user string) error {
	tok, err := loadToken(from)
	if err != nil {
		return err
	}
	// target pode ser un nodo do contorno (st-clientN) ou un Device ID.
	targetID := strings.ToUpper(target)
	if strings.HasPrefix(target, "st-") {
		if targetID, err = node(target).MyID(ctx); err != nil {
			return err
		}
	}
	otp, err := devOTP(ctx, user)
	if err != nil {
		return err
	}
	err = pairsrv(ctx, http.MethodDelete, "/api/v1/devices/"+targetID, tok, map[string]string{"otp": otp}, nil)
	if err == nil {
		fmt.Printf("%s revogou %s\n", from, target)
	}
	return err
}
