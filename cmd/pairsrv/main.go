// pairsrv: servizo de emparellamento de equipos co servidor Syncthing. O
// usuario entra na súa web co usuario e contrasinal de SFTPGo, ve alí un
// código de vinculación (TOTP xerado por pairsrv) e introdúceo no cliente.
// Ver internal/pairing e internal/sftpgo.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/DanBaDo/cld/internal/pairing"
	"github.com/DanBaDo/cld/internal/sftpgo"
	"github.com/DanBaDo/cld/internal/stapi"
)

func main() {
	cfgPath := flag.String("config", "dev/pairsrv.json", "ficheiro de configuración")
	statePath := flag.String("state", ".dev/pairsrv/state.json", "ficheiro de estado")
	listen := flag.String("listen", ":8080", "enderezo de escoita da web e da API dos clientes")
	hookListen := flag.String("hook-listen", "", "enderezo da API interna para SFTPGo (token en PAIRSRV_HOOK_TOKEN); baleiro: desactivada")
	downloads := flag.String("downloads", "", "directorio cos paquetes do cliente, servidos en /descargas/; baleiro: sen descargas")
	reconcileEvery := flag.Duration("reconcile", time.Minute, "intervalo de reconciliación con Syncthing")
	flag.Parse()

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	dir, err := sftpgo.Open(cfg.SFTPGo)
	if err != nil {
		log.Fatal(err)
	}
	defer dir.Close()
	st := stapi.New("st-server", env("ST_URL", "http://st-server:8384"), os.Getenv("ST_APIKEY"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err = st.WaitHealthy(wctx)
	if err == nil {
		err = waitSFTPGo(wctx, dir)
	}
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	svc, err := pairing.NewService(ctx, cfg.Options, dir, st, *statePath)
	if err != nil {
		log.Fatal(err)
	}

	// Reconciliación ao recibir avisos de SFTPGo e periódica: a periódica
	// cubre os cambios que non xeran eventos (cartafoles), un Syncthing
	// reiniciado, cambios feitos a man ou avisos perdidos.
	go func() {
		for {
			if err := svc.Reconcile(ctx); err != nil && ctx.Err() == nil {
				log.Printf("reconciliación: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-svc.ReconcileRequests():
			case <-time.After(*reconcileEvery):
			}
		}
	}()

	if *hookListen != "" {
		token := os.Getenv("PAIRSRV_HOOK_TOKEN")
		if len(token) < 16 {
			log.Fatal("PAIRSRV_HOOK_TOKEN: fai falta un token de polo menos 16 caracteres")
		}
		hook := &http.Server{Addr: *hookListen, Handler: svc.HookHandler(token), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			hook.Close()
		}()
		go func() {
			log.Printf("API interna escoitando en %s", *hookListen)
			if err := hook.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}

	// A API dos clientes en /api/ e /healthz; o resto é a web dos usuarios.
	api := svc.Handler()
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/healthz", api)
	mux.Handle("/", pairing.NewWeb(svc, dir, cfg.Web).Handler())
	if *downloads != "" {
		mux.Handle("GET /descargas/", http.StripPrefix("/descargas/", filesOnly(http.FileServer(http.Dir(*downloads)))))
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		srv.Shutdown(sctx)
	}()
	log.Printf("pairsrv escoitando en %s", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type config struct {
	pairing.Options
	SFTPGo sftpgo.Config      `json:"sftpgo"`
	Web    pairing.WebOptions `json:"web"`
}

func loadConfig(path string) (*config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// waitSFTPGo agarda a que SFTPGo cree a súa BD (primeiro arranque).
func waitSFTPGo(ctx context.Context, dir *sftpgo.Directory) error {
	for {
		err := dir.CheckSchema(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(2 * time.Second):
		}
	}
}

// filesOnly non deixa listar directorios.
func filesOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
