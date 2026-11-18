// cld: cliente de escritorio.
//
//	cld daemon    servizo de usuario: fala co Syncthing local e serve a web de
//	              configuración en 127.0.0.1
//	cld open      abre a web no navegador xa autenticada (o que executa o
//	              acceso directo)
//	cld shortcut  crea o acceso directo no menú de aplicacións (Linux)
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/DanBaDo/cld/internal/client"
	"github.com/DanBaDo/cld/internal/stapi"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "daemon":
		err = daemon(os.Args[2:])
	case "open":
		err = open(os.Args[2:])
	case "shortcut":
		err = shortcut(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cld:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "uso: cld daemon|open|shortcut [opcións]  (-h para axuda)")
	os.Exit(2)
}

func defaultDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "cld")
}

// uiInfo é o que o daemon deixa para `cld open`: só lexible polo usuario.
type uiInfo struct {
	URL        string `json:"url"`        // a que abre o navegador
	ControlURL string `json:"controlURL"` // a que usa `cld open` (sempre loopback)
	ControlKey string `json:"controlKey"`
}

func uiPath(dir string) string { return filepath.Join(dir, "ui.json") }

func daemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "directorio de estado do cliente")
	cfgPath := fs.String("config", "", "configuración do instalador (por defecto <dir>/cld.json)")
	listen := fs.String("listen", "127.0.0.1:0", "enderezo da web (porto 0: un libre)")
	publicURL := fs.String("public-url", "", "URL coa que o navegador chega á web (por defecto http://<listen>)")
	stURL := fs.String("st-url", "http://127.0.0.1:8384", "API do Syncthing local")
	foldersBase := fs.String("folders-base", "", "onde se suxire crear as carpetas (por defecto ~/cld)")
	fs.Parse(args)

	if *cfgPath == "" {
		*cfgPath = filepath.Join(*dir, "cld.json")
	}
	if *foldersBase == "" {
		home, _ := os.UserHomeDir()
		*foldersBase = filepath.Join(home, "cld")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	cfg, err := client.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	st := stapi.New("syncthing", *stURL, os.Getenv("ST_APIKEY"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err = st.WaitHealthy(wctx)
	cancel()
	if err != nil {
		return err
	}

	app, err := client.NewApp(cfg, st, filepath.Join(*dir, "state.json"), *foldersBase)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	if *publicURL == "" {
		*publicURL = "http://" + ln.Addr().String()
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	info := uiInfo{URL: *publicURL, ControlURL: "http://127.0.0.1:" + port, ControlKey: randKey()}
	web, err := client.NewWeb(app, info.URL, info.ControlKey)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(info)
	if err := os.WriteFile(uiPath(*dir), b, 0o600); err != nil {
		return err
	}
	defer os.Remove(uiPath(*dir))

	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := app.Tick(ctx); err != nil {
					log.Printf("tick: %v", err)
				}
			}
		}
	}()

	srv := &http.Server{Handler: web.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		srv.Shutdown(sctx)
	}()
	log.Printf("cld: web en %s (escoita en %s)", info.URL, ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func randKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", b)
}

// open pídelle ao daemon unha URL de acceso dun só uso e ábrea no navegador.
func open(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "directorio de estado do cliente")
	print := fs.Bool("print", false, "só amosar a URL, sen abrir o navegador")
	fs.Parse(args)

	b, err := os.ReadFile(uiPath(*dir))
	if err != nil {
		return fmt.Errorf("o servizo cld non está en marcha (%w)", err)
	}
	var info uiInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, info.ControlURL+"/_control/login-url", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("X-CLD-Control", info.ControlKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("o servizo cld non responde: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("o servizo cld respondeu %s", resp.Status)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if *print {
		fmt.Println(out.URL)
		return nil
	}
	return openBrowser(out.URL)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

// shortcut crea a entrada .desktop (Linux). En Windows e macOS farao o
// instalador (atallo no menú Inicio / app en Aplicacións).
func shortcut(args []string) error {
	fs := flag.NewFlagSet("shortcut", flag.ExitOnError)
	dir := fs.String("dir", "", "directorio do estado (se non é o por defecto)")
	fs.Parse(args)
	if runtime.GOOS != "linux" {
		return errors.New("en Windows e macOS o acceso directo créao o instalador")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	execLine := fmt.Sprintf("%q open", exe)
	if *dir != "" {
		execLine += fmt.Sprintf(" -dir %q", *dir)
	}
	data, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		data = x
	} else {
		data = filepath.Join(data, ".local/share")
	}
	path := filepath.Join(data, "applications", "cld.desktop")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "[Desktop Entry]\nType=Application\nName=cld\nGenericName=Sincronización de ficheiros\n" +
		"Comment=Configurar a sincronización co servidor de ficheiros\nExec=" + execLine + "\n" +
		"Icon=folder-sync\nTerminal=false\nCategories=Network;FileTransfer;\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("acceso directo creado:", path)
	return nil
}
