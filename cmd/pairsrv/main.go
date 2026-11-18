// pairsrv: servizo de emparellamento de equipos co servidor Syncthing
// mediante o TOTP da conta de SFTPGo. Ver internal/pairing e internal/sftpgo.
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
	"syscall"
	"time"

	"github.com/DanBaDo/cld/internal/pairing"
	"github.com/DanBaDo/cld/internal/sftpgo"
	"github.com/DanBaDo/cld/internal/stapi"
)

func main() {
	cfgPath := flag.String("config", "dev/pairsrv.json", "ficheiro de configuración")
	statePath := flag.String("state", ".dev/pairsrv/state.json", "ficheiro de estado")
	listen := flag.String("listen", ":8080", "enderezo de escoita")
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

	// Reconciliación periódica: recupera a configuración se Syncthing se
	// reinicia, se alguén a toca a man ou se fallou unha aplicación anterior.
	go func() {
		for {
			if err := svc.Reconcile(ctx); err != nil && ctx.Err() == nil {
				log.Printf("reconciliación: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(*reconcileEvery):
			}
		}
	}()

	srv := &http.Server{
		Addr:              *listen,
		Handler:           svc.Handler(),
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
	SFTPGo sftpgo.Config `json:"sftpgo"`
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
