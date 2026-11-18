// pairsrv: servizo de emparellamento de equipos co servidor Syncthing
// mediante TOTP. Ver internal/pairing.
package main

import (
	"context"
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
	"github.com/DanBaDo/cld/internal/stapi"
	"github.com/DanBaDo/cld/internal/totp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "newsecret" {
		newSecret(os.Args[2:])
		return
	}
	cfgPath := flag.String("config", "dev/pairsrv.json", "ficheiro de configuración")
	statePath := flag.String("state", ".dev/pairsrv/state.json", "ficheiro de estado")
	listen := flag.String("listen", ":8080", "enderezo de escoita")
	reconcileEvery := flag.Duration("reconcile", time.Minute, "intervalo de reconciliación con Syncthing")
	flag.Parse()

	cfg, err := pairing.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	st := stapi.New("st-server", env("ST_URL", "http://st-server:8384"), os.Getenv("ST_APIKEY"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err = st.WaitHealthy(wctx)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	svc, err := pairing.NewService(ctx, cfg, st, *statePath)
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

func newSecret(args []string) {
	user := "usuario"
	if len(args) > 0 {
		user = args[0]
	}
	s, err := totp.GenerateSecret()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("segredo:", s)
	fmt.Println("uri:    ", totp.URI("cld", user, s))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
