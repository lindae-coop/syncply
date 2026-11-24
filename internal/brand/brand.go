// Package brand contén a estética común das páxinas de Syncply (a web de
// pairsrv e a web local do cliente): unha folla de estilos coa mesma
// imaxe que a web do proxecto (docs/index.html), con modo escuro
// automático, e o logo.
//
// O logo é unha copia limpa de docs/logo.svg cun <style> interno que
// aclara o contorno e o texto «lindæ» cando o sistema está en modo
// escuro. Se cambia docs/logo.svg, rexenerala (ver o historial de git).
package brand

import (
	_ "embed"
	"net/http"
)

//go:embed syncply.css
var css []byte

//go:embed logo.svg
var logo []byte

// Logo para fondos sempre escuros (o pé): copia de docs/logo-escuro.svg.
//
//go:embed logo-escuro.svg
var logoDark []byte

// Rutas nas que Register serve os ficheiros.
const (
	CSSPath      = "/static/syncply.css"
	LogoPath     = "/static/logo.svg"
	LogoDarkPath = "/static/logo-escuro.svg"
)

// Register engade ao mux as rutas da folla de estilos e do logo. Son
// públicas (non levan datos do usuario): a páxina de «sen acceso» do
// cliente e o login de pairsrv tamén as usan.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+CSSPath, serve("text/css; charset=utf-8", css))
	mux.HandleFunc("GET "+LogoPath, serve("image/svg+xml", logo))
	mux.HandleFunc("GET "+LogoDarkPath, serve("image/svg+xml", logoDark))
}

func serve(ctype string, body []byte) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		h := rw.Header()
		h.Set("Content-Type", ctype)
		// As páxinas van con no-store; estes ficheiros poden gardarse un
		// pouco (cambian só ao actualizar o binario).
		h.Set("Cache-Control", "public, max-age=3600")
		rw.Write(body)
	}
}
