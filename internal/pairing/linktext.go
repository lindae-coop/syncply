package pairing

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode"
)

// Texto de vinculación: o que o usuario copia da web de pairsrv e pega no
// cliente xenérico. Non leva segredos (o código de vinculación é o que
// autoriza); di ao cliente con que servidor e como que usuario vincularse.
//
//	cld1:<base64url(JSON)>
const linkPrefix = "cld1:"

// LinkData é o contido do texto de vinculación.
type LinkData struct {
	V    int    `json:"v"`
	URL  string `json:"url"`  // URL base de pairsrv
	User string `json:"user"` // usuario de SFTPGo
	// Device ID de Syncthing do servidor: o cliente rexeita o emparellamento
	// se pairsrv responde con outro.
	Server string `json:"server"`
}

// DevMode indica o contorno de desenvolvemento (CLD_DEV=1): só entón se
// permite HTTP sen cifrar entre o cliente e pairsrv. En produción pairsrv
// vai detrás dun proxy con HTTPS (ou o que sexa, sempre cun certificado
// válido para as CA do sistema).
func DevMode() bool { return os.Getenv("CLD_DEV") == "1" }

// CheckPublicURL valida a URL coa que os clientes chegan a pairsrv: https,
// ou http só en desenvolvemento.
func CheckPublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("enderezo do servidor non válido")
	}
	if u.Scheme == "http" && !DevMode() {
		return ErrInsecureURL
	}
	return nil
}

// Encode xera o texto de vinculación.
func (d LinkData) Encode() string {
	d.V = 1
	b, _ := json.Marshal(d)
	return linkPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// Erros de ParseLinkText: mensaxes para o usuario.
var (
	ErrLinkPrefix  = errors.New("o texto de vinculación ten que empezar por «cld1:». Cópiao enteiro da páxina do servidor")
	ErrInsecureURL = errors.New("o servidor non usa https: a conexión non iría cifrada. Avisa ao administrador (http só se permite en desenvolvemento, con CLD_DEV=1)")
	ErrLinkBroken  = errors.New("o texto de vinculación está incompleto ou estragado. Cópiao de novo enteiro da páxina do servidor")
)

// ParseLinkText valida e decodifica un texto de vinculación pegado polo
// usuario (ignora espazos e saltos de liña engadidos ao copiar).
func ParseLinkText(s string) (*LinkData, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	rest, ok := strings.CutPrefix(s, linkPrefix)
	if !ok {
		return nil, ErrLinkPrefix
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(rest, "="))
	if err != nil {
		return nil, ErrLinkBroken
	}
	var d LinkData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, ErrLinkBroken
	}
	if d.V != 1 {
		return nil, fmt.Errorf("versión do texto de vinculación non soportada (%d): actualiza o cliente", d.V)
	}
	if err := CheckPublicURL(d.URL); errors.Is(err, ErrInsecureURL) {
		return nil, err
	} else if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrLinkBroken, err)
	}
	d.URL = strings.TrimRight(d.URL, "/")
	d.User = strings.TrimSpace(d.User)
	if d.User == "" || len(d.User) > 256 {
		return nil, fmt.Errorf("%w (falta o usuario)", ErrLinkBroken)
	}
	d.Server = strings.ToUpper(d.Server)
	if !deviceIDRe.MatchString(d.Server) {
		return nil, fmt.Errorf("%w (identificador do servidor non válido)", ErrLinkBroken)
	}
	return &d, nil
}

// Host devolve o servidor (host[:porto]) para amosarllo ao usuario.
func (d *LinkData) Host() string {
	u, _ := url.Parse(d.URL)
	return u.Host
}
