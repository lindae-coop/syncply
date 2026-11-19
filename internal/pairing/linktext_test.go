package pairing

import (
	"errors"
	"strings"
	"testing"
)

const testServerID = "AAAAAAA-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH"

func TestLinkTextRoundTrip(t *testing.T) {
	in := LinkData{URL: "https://nube.example.org/", User: "alice", Server: testServerID}
	s := in.Encode()
	if !strings.HasPrefix(s, "cld1:") {
		t.Fatalf("sen prefixo: %s", s)
	}
	// Ao copiar poden colarse saltos de liña e espazos.
	pasted := "  " + s[:20] + "\n" + s[20:] + " \n"
	d, err := ParseLinkText(pasted)
	if err != nil {
		t.Fatal(err)
	}
	if d.URL != "https://nube.example.org" || d.User != "alice" || d.Server != testServerID || d.V != 1 {
		t.Fatalf("%+v", d)
	}
	if d.Host() != "nube.example.org" {
		t.Fatalf("host %q", d.Host())
	}
}

func TestLinkTextHTTPOnlyInDev(t *testing.T) {
	text := LinkData{URL: "http://pairsrv:8080", User: "bob", Server: testServerID}.Encode()
	t.Setenv("CLD_DEV", "")
	if _, err := ParseLinkText(text); !errors.Is(err, ErrInsecureURL) {
		t.Errorf("http fóra de desenvolvemento: %v", err)
	}
	t.Setenv("CLD_DEV", "1")
	if _, err := ParseLinkText(text); err != nil {
		t.Errorf("http en desenvolvemento: %v", err)
	}
}

func TestLinkTextInvalid(t *testing.T) {
	ok := LinkData{URL: "https://nube.example.org", User: "bob", Server: testServerID}
	for name, tc := range map[string]struct {
		s    string
		want error
	}{
		"baleiro":      {"", ErrLinkPrefix},
		"só código":    {"123456", ErrLinkPrefix},
		"base64 mal":   {"cld1:%%%", ErrLinkBroken},
		"non JSON":     {"cld1:aG9sYQ", ErrLinkBroken},
		"cortado":      {ok.Encode()[:30], ErrLinkBroken},
		"sen usuario":  {LinkData{URL: ok.URL, Server: ok.Server}.Encode(), ErrLinkBroken},
		"sen servidor": {LinkData{URL: ok.URL, User: "bob", Server: "XYZ"}.Encode(), ErrLinkBroken},
		"esquema":      {LinkData{URL: "ftp://x", User: "bob", Server: ok.Server}.Encode(), ErrLinkBroken},
	} {
		if _, err := ParseLinkText(tc.s); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, quería %v", name, err, tc.want)
		}
	}
	if _, err := ParseLinkText(`cld1:eyJ2IjoyfQ`); err == nil || !strings.Contains(err.Error(), "versión") { // {"v":2}
		t.Errorf("versión: %v", err)
	}
}
