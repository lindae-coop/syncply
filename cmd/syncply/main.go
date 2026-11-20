// syncply: cliente de escritorio.
//
//	syncply daemon    servizo de usuario: fala co Syncthing local e serve a web de
//	              configuración en 127.0.0.1
//	syncply open      abre a web no navegador xa autenticada (o que executa o
//	              acceso directo)
//	syncply shortcut  crea o acceso directo no menú de aplicacións (Linux)
//	syncply install   instálase para o usuario (Windows; sen argumentos, o dobre
//	              clic instala ou, se xa está instalado, abre a web)
//	syncply uninstall desinstala (Windows)
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/lindae-coop/syncply/internal/client"
	"github.com/lindae-coop/syncply/internal/stapi"
)

// version gravase ao compilar: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	var err error
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "":
		err = defaultAction()
	case "install":
		err = install(os.Args[2:])
	case "uninstall":
		err = uninstall(os.Args[2:])
	case "daemon":
		err = daemon(os.Args[2:])
	case "open":
		err = open(os.Args[2:])
	case "shortcut":
		err = shortcut(os.Args[2:])
	case "version":
		fmt.Println("syncply", version)
	default:
		usage()
	}
	if errors.Is(err, errAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "syncply:", err)
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "syncply:", err)
		reportError(err)
		os.Exit(1)
	}
}

var errAlreadyRunning = errors.New("o servizo xa está en marcha")

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}

func usage() {
	fmt.Fprintln(os.Stderr, "uso: syncply daemon|open|shortcut|install|uninstall|version [opcións]  (-h para axuda)")
	os.Exit(2)
}

func defaultDir() string { return defaultStateDir() }

// uiInfo é o que o daemon deixa para `syncply open`: só lexible polo usuario.
type uiInfo struct {
	URL        string `json:"url"`        // a que abre o navegador
	ControlURL string `json:"controlURL"` // a que usa `syncply open` (sempre loopback)
	ControlKey string `json:"controlKey"`
}

func uiPath(dir string) string { return filepath.Join(dir, "ui.json") }

func daemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "directorio de estado do cliente")
	listen := fs.String("listen", "127.0.0.1:0", "enderezo da web (porto 0: un libre)")
	publicURL := fs.String("public-url", "", "URL coa que o navegador chega á web (por defecto http://<listen>)")
	stURL := fs.String("st-url", "", "API dun Syncthing externo (se se indica, syncply non arranca o seu)")
	stBin := fs.String("syncthing", "", "binario de Syncthing (por defecto, xunto a syncply ou no PATH)")
	stHome := fs.String("st-home", "", "directorio de Syncthing (por defecto <dir>/syncthing)")
	stGUI := fs.String("st-gui", "", "enderezo da API de Syncthing (por defecto, socket Unix en <st-home>; en Windows, porto libre en 127.0.0.1)")
	foldersBase := fs.String("folders-base", "", "onde se suxire crear as carpetas (por defecto ~/syncply)")
	logFile := fs.String("log", "", "ficheiro de rexistro (por defecto stderr; en Windows <dir>\\syncply.log)")
	fs.Parse(args)

	if *foldersBase == "" {
		home, _ := os.UserHomeDir()
		*foldersBase = filepath.Join(home, "syncply")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	release, err := singleInstance(*dir)
	if err != nil {
		return err
	}
	defer release()
	if *logFile == "" {
		*logFile = defaultLogFile(*dir)
	}
	var logOut io.Writer = os.Stderr
	if *logFile != "" {
		f, err := openLog(*logFile)
		if err != nil {
			return err
		}
		defer f.Close()
		logOut = f
		log.SetOutput(f)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := client.NewApp(nil, filepath.Join(*dir, "state.json"), *foldersBase)
	if err != nil {
		return err
	}
	var supDone chan struct{}
	if *stURL != "" {
		// Syncthing externo (p. ex. xa instalado como servizo).
		st := stapi.New("syncthing", *stURL, os.Getenv("ST_APIKEY"))
		app.SetSyncthingClient(st)
		wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = st.WaitHealthy(wctx)
		cancel()
		if err != nil {
			return err
		}
	} else {
		supDone, err = superviseSyncthing(ctx, app, *dir, *stBin, *stHome, *stGUI, logOut)
		if err != nil {
			return err
		}
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
	web.OnShutdown = func() {
		log.Printf("syncply: peche pedido polo canal de control")
		stop()
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
	log.Printf("syncply %s: web en %s (escoita en %s)", version, info.URL, ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if supDone != nil {
		<-supDone // agardar a que Syncthing remate
	}
	return nil
}

// superviseSyncthing prepara e lanza o Syncthing propio do cliente.
func superviseSyncthing(ctx context.Context, app *client.App, dir, bin, home, gui string, logOut io.Writer) (chan struct{}, error) {
	if bin == "" {
		var err error
		if bin, err = findSyncthing(); err != nil {
			return nil, err
		}
	}
	if home == "" {
		home = filepath.Join(dir, "syncthing")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	if gui == "" {
		if runtime.GOOS == "windows" {
			port, err := freePort()
			if err != nil {
				return nil, err
			}
			gui = "127.0.0.1:" + port
		} else {
			gui = "unix://" + filepath.Join(home, "gui.sock")
		}
	}
	key := os.Getenv("ST_APIKEY") // desenvolvemento: chave fixa compartida con stproto
	if key == "" {
		var err error
		if key, err = app.SyncthingAPIKey(); err != nil {
			return nil, err
		}
	}
	apiBase := gui
	if !strings.HasPrefix(gui, "unix://") {
		h, p, err := net.SplitHostPort(strings.TrimPrefix(gui, "http://"))
		if err != nil {
			return nil, fmt.Errorf("st-gui: %w", err)
		}
		if h == "" || h == "0.0.0.0" || h == "::" {
			h = "127.0.0.1"
		}
		apiBase = "http://" + net.JoinHostPort(h, p)
	}
	st := stapi.New("syncthing", apiBase, key)
	app.SetSyncthingClient(st)

	sup := &client.Supervisor{
		Bin: bin, Home: home, GUIAddress: gui, APIKey: key, Log: logOut,
		Shutdown: st.Shutdown,
		OnStart: func(ctx context.Context) {
			wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			if err := st.WaitHealthy(wctx); err != nil {
				log.Printf("syncthing non responde: %v", err)
				return
			}
			if err := app.ApplyOptions(ctx); err != nil {
				log.Printf("aplicando opcións de syncthing: %v", err)
			}
		},
	}
	app.SetSupervisor(sup)
	if !app.Stopped() {
		sup.Start()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.Run(ctx)
	}()
	log.Printf("syncply: syncthing %s (home %s, API %s)", bin, home, gui)
	return done, nil
}

// findSyncthing busca o binario xunto a syncply e despois no PATH.
func findSyncthing() (string, error) {
	name := "syncthing"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("non se atopa syncthing (xunto a syncply nin no PATH); usa -syncthing ou -st-url")
	}
	return p, nil
}

func freePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	_, p, _ := net.SplitHostPort(l.Addr().String())
	return p, nil
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
	if err := ensureDaemon(*dir); err != nil {
		return err
	}
	return openUI(*dir, *print)
}

func openUI(dir string, print bool) error {
	u, err := loginURL(dir)
	if err != nil {
		return err
	}
	if print {
		fmt.Println(u)
		return nil
	}
	return openBrowser(u)
}

func loginURL(dir string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := controlCall(dir, "login-url", &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// controlCall fala co daemon pola súa canle de control (porto e clave en
// ui.json, só lexible polo usuario).
func controlCall(dir, action string, out any) error {
	b, err := os.ReadFile(uiPath(dir))
	if err != nil {
		return fmt.Errorf("o servizo syncply non está en marcha (%w)", err)
	}
	var info uiInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, info.ControlURL+"/_control/"+action, bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("X-Syncply-Control", info.ControlKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("o servizo syncply non responde: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("o servizo syncply respondeu %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// openLog abre o rexistro do daemon; se pasa de 5 MB, gárdao como .1 e
// empeza outro.
func openLog(path string) (*os.File, error) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
	path := filepath.Join(data, "applications", "syncply.desktop")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "[Desktop Entry]\nType=Application\nName=Syncply\nGenericName=Sincronización de ficheiros\n" +
		"Comment=Configurar a sincronización co servidor de ficheiros\nExec=" + execLine + "\n" +
		"Icon=folder-sync\nTerminal=false\nCategories=Network;FileTransfer;\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("acceso directo creado:", path)
	return nil
}
