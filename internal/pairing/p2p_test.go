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

func TestP2PClientOptionsPublic(t *testing.T) {
	own := &P2POptions{
		Discovery: &P2PServer{URL: "https://nube.example.org:8443", CertFile: "testdata/st-cert.pem"},
		Relay:     &P2PServer{URL: "relay://nube.example.org:22067", CertFile: "testdata/st-cert.pem", Token: "t"},
	}

	// Só descubrimento público como apoio: o propio vai primeiro, e o relay
	// segue sendo só o noso.
	p := *own
	p.PublicDiscovery = true
	o, err := p.ClientOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ann := o["globalAnnounceServers"].([]string)
	if len(ann) != 2 || !strings.HasPrefix(ann[0], "https://nube.example.org:8443/") || ann[1] != PublicDiscoveryServers {
		t.Errorf("descubrimento: %v", ann)
	}
	for _, a := range o["listenAddresses"].([]string) {
		if strings.Contains(a, "syncthing.net") || a == "default" {
			t.Errorf("relay público sen pedilo: %s", a)
		}
	}

	// Relays públicos ademais do noso.
	p.PublicRelays = true
	o, err = p.ClientOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	la := o["listenAddresses"].([]string)
	if len(la) != 4 || !strings.HasPrefix(la[2], "relay://nube.example.org:22067/") || la[3] != PublicRelayPool {
		t.Errorf("escoita: %v", la)
	}

	// Só os públicos, sen servidores propios.
	o, err = (&P2POptions{PublicDiscovery: true, PublicRelays: true}).ClientOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o["globalAnnounceEnabled"] != true || o["relaysEnabled"] != true || o["natEnabled"] != true {
		t.Errorf("opcións: %v", o)
	}
	if la := o["listenAddresses"].([]string); len(la) != 3 || la[2] != PublicRelayPool {
		t.Errorf("escoita: %v", la)
	}

	// Sen nada: sen opcións (sistema pechado).
	if o, _ := (&P2POptions{}).ClientOptions(context.Background()); len(o) != 0 {
		t.Errorf("opcións sen p2p: %v", o)
	}
}
