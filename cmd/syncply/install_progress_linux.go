//go:build linux

package main

// Progreso da instalación: no terminal e, se hai sesión gráfica, nunha
// páxina local (127.0.0.1, porto libre, URL cun token aleatorio) que o
// instalador abre no navegador antes de empezar. A páxina recárgase cada
// segundo (sen JavaScript) e, ao rematar, redirixe á configuración da web
// local (URL de acceso dun só uso) ou mostra o erro.

import (
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/lindae-coop/syncply/internal/brand"
)

type installProgress struct {
	mu     sync.Mutex
	steps  []string
	done   bool
	err    error
	logs   string                 // onde mirar se falla
	next   func() (string, error) // URL da configuración (un só uso)
	server *http.Server

	seen, served         chan struct{}
	seenOnce, servedOnce sync.Once
}

// startProgress: se browser, abre a páxina e agarda (como moito uns
// segundos) a que o navegador a cargue, para que se vexan todos os pasos.
func startProgress(browser bool) *installProgress {
	p := &installProgress{seen: make(chan struct{}), served: make(chan struct{})}
	if !browser {
		return p
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return p
	}
	host := ln.Addr().String()
	path := "/instalar/" + randKey()
	mux := http.NewServeMux()
	brand.Register(mux)
	mux.HandleFunc("GET "+path, p.page)
	p.server = &http.Server{ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != host { // DNS rebinding
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			h := w.Header()
			h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Content-Type-Options", "nosniff")
			mux.ServeHTTP(w, r)
		})}
	go p.server.Serve(ln)
	if err := openBrowser("http://" + host + path); err != nil {
		p.server.Close()
		p.server = nil
		return p
	}
	select {
	case <-p.seen:
	case <-time.After(15 * time.Second): // navegador lento: verá o resto
	}
	return p
}

func (p *installProgress) step(text string) {
	p.mu.Lock()
	p.steps = append(p.steps, text)
	p.mu.Unlock()
	info("· " + text)
}

// setLogs: desde aquí, se falla, os detalles están no rexistro do servizo.
func (p *installProgress) setLogs(logs string) {
	p.mu.Lock()
	p.logs = logs
	p.mu.Unlock()
}

func (p *installProgress) browserSeen() bool {
	select {
	case <-p.seen:
		return true
	default:
		return false
	}
}

// finish garda o resultado e agarda a que o navegador o recolla (como moito
// 2 minutos). Sen terminal nin navegador, avisa cunha notificación.
func (p *installProgress) finish(err error, next func() (string, error)) {
	p.mu.Lock()
	p.done, p.err, p.next = true, err, next
	p.mu.Unlock()
	if p.server != nil {
		select {
		case <-p.served:
			time.Sleep(time.Second) // que se envíe a resposta
		case <-time.After(2 * time.Minute):
		}
		p.server.Close()
	}
	if p.browserSeen() || isTerminal(os.Stdout) {
		return
	}
	title, body := "Syncply instalado", "Abre «Syncply» desde o menú de aplicacións para vincular este equipo."
	if err != nil {
		title, body = "Syncply: non se puido instalar", upperFirst(err.Error())+"."
	}
	exec.Command("notify-send", "-a", "Syncply", title, body).Run()
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

type stepView struct{ Text, State string } // ok, warn (en curso), bad

type progressView struct {
	Version string
	Steps   []stepView
	Done    bool
	Err     string
	Logs    string
}

func (p *installProgress) page(w http.ResponseWriter, r *http.Request) {
	p.seenOnce.Do(func() { close(p.seen) })
	p.mu.Lock()
	v := progressView{Version: version, Done: p.done, Logs: p.logs}
	if p.err != nil {
		v.Err = upperFirst(p.err.Error()) + "."
	}
	for i, s := range p.steps {
		st := "ok"
		if i == len(p.steps)-1 {
			switch {
			case p.err != nil:
				st = "bad"
			case !p.done:
				st = "warn"
			}
		}
		v.Steps = append(v.Steps, stepView{s, st})
	}
	next := p.next
	p.mu.Unlock()
	if v.Done && v.Err == "" {
		if u, err := next(); err == nil {
			p.servedOnce.Do(func() { close(p.served) })
			http.Redirect(w, r, u, http.StatusSeeOther)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	progressTmpl.Execute(w, v)
	if v.Done {
		p.servedOnce.Do(func() { close(p.served) })
	}
}

var progressTmpl = template.Must(template.New("").Parse(`<!doctype html>
<html lang="gl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{if not .Done}}<meta http-equiv="refresh" content="1">{{end}}
<title>Instalando Syncply</title>
<link rel="stylesheet" href="/static/syncply.css">
<link rel="icon" type="image/svg+xml" href="/static/logo.svg">
</head>
<body>
<header class="barra"><div class="envoltorio">
  <span class="marca"><img src="/static/logo.svg" alt="" width="76" height="76"></span>
  <h1>Syncply</h1>
</div></header>
<main>
<section>
  {{if .Err}}<h2>Non se puido instalar Syncply</h2>
  {{else if .Done}}<h2>Syncply {{.Version}} instalado</h2>
  {{else}}<h2>Instalando Syncply {{.Version}}…</h2>
  <p class="muted">Só para o teu usuario. Ao rematar, abrirase aquí a configuración para vincular este equipo.</p>{{end}}
  <ul class="estado">
  {{range .Steps}}<li><span class="dot {{.State}}"></span>{{.Text}}</li>
  {{end}}</ul>
  {{if .Err}}<div class="msg errmsg">{{.Err}}</div>
  <p class="muted">{{if .Logs}}Máis detalles: <code>{{.Logs}}</code>. {{end}}Podes pechar esta páxina.</p>
  {{else if .Done}}<p>Abre «Syncply» desde o menú de aplicacións para vincular este equipo.</p>{{end}}
</section>
</main>
<footer class="pe"><div class="envoltorio">
  <div class="esquerda"><img src="/static/logo-escuro.svg" alt="" width="60" height="60">
    <div><p><strong>Syncply</strong> — a nube de ficheiros do teu grupo.</p>
    <p>Un proxecto de <a href="https://lindae.eu">lindæ</a>.</p></div></div>
  <p><a href="https://github.com/lindae-coop/syncply">Código fonte</a></p>
</div></footer>
</body></html>
`))
