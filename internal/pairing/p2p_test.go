package pairing

import (
	"context"
	"os"
	"strings"
	"testing"
)

// testdata/st-cert.pem xerouse con `syncthing generate` (2.1.7) e o ID con
// `syncthing device-id`.
const stCertID = "YHT4QRY-RAIA4XA-EA6ZGUW-ILKA753-ESXIDWD-LON4LO7-J3IBT72-IJZ56AE"

func TestDeviceIDFromPEM(t *testing.T) {
	b, err := os.ReadFile("testdata/st-cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	id, err := DeviceIDFromPEM(b)
	if err != nil || id != stCertID {
		t.Fatalf("id = %q, %v; quérese %q", id, err, stCertID)
	}
	if _, err := DeviceIDFromPEM([]byte("non")); err == nil {
		t.Fatal("aceptou algo que non é un certificado")
	}
}

func TestP2PClientOptions(t *testing.T) {
	p := &P2POptions{
		Discovery: &P2PServer{URL: "https://nube.example.org:8443", CertFile: "testdata/st-cert.pem"},
		Relay:     &P2PServer{URL: "relay://nube.example.org:22067", CertFile: "testdata/st-cert.pem", Token: "seg/redo"},
	}
	o, err := p.ClientOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := o["globalAnnounceServers"].([]string)[0]; got != "https://nube.example.org:8443/?id="+stCertID {
		t.Errorf("descubrimento: %s", got)
	}
	la := o["listenAddresses"].([]string)
	if r := la[len(la)-1]; r != "relay://nube.example.org:22067/?id="+stCertID+"&token=seg%2Fredo" {
		t.Errorf("relay: %s", r)
	}
	if o["relaysEnabled"] != true || o["globalAnnounceEnabled"] != true {
		t.Errorf("opcións: %v", o)
	}
	for _, a := range la {
		if strings.Contains(a, "default") || strings.Contains(a, "syncthing.net") {
			t.Errorf("enderezo público: %s", a)
		}
	}
	if _, err := (&P2POptions{Relay: &P2PServer{URL: "https://x/", CertFile: "testdata/st-cert.pem"}}).ClientOptions(context.Background()); err == nil {
		t.Error("aceptou un relay sen relay://")
	}
}
