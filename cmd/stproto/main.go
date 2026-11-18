// stproto: ferramenta do prototipo para validar con Syncthing o fluxo
// servidor/cliente: carpetas pendentes, servidor como introducer,
// propagación de pares e revogación.
//
// Execútase no contedor "tools" (ver compose.yaml): make proto ARGS="..."
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DanBaDo/cld/internal/stapi"
)

const (
	server     = "st-server"
	dataRoot   = "/var/syncthing/data" // ruta dentro dos contedores de Syncthing
	nodesMount = "/nodes"              // ./.dev montado no contedor tools
)

var allNodes = []string{server, "st-client1", "st-client2"}

func node(name string) *stapi.Client {
	return stapi.New(name, "http://"+name+":8384", os.Getenv("ST_APIKEY"))
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(ctx)
	case "share": // share <folderID> <label>
		need(args, 2)
		err = cmdShare(ctx, args[0], args[1])
	case "pair": // pair <cliente> <folderID>...
		need(args, 2)
		err = cmdPair(ctx, args[0], args[1:])
	case "pending": // pending <cliente>
		need(args, 1)
		err = cmdPending(ctx, args[0])
	case "accept": // accept <cliente> <folderID>
		need(args, 2)
		err = cmdAccept(ctx, args[0], args[1])
	case "revoke": // revoke <cliente>
		need(args, 1)
		err = cmdRevoke(ctx, args[0])
	case "status":
		err = cmdStatus(ctx)
	case "demo":
		err = cmdDemo(ctx)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERRO:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `uso: stproto <orde>
  init                         configura os nodos (sen descubrimento global nin relays)
  share <folderID> <etiqueta>  crea unha carpeta no servidor
  pair <cliente> <folderID>... simula o emparellamento (paso do OTP)
  pending <cliente>            lista as carpetas ofrecidas ao cliente
  accept <cliente> <folderID>  o cliente acepta a carpeta ofrecida
  revoke <cliente>             o servidor revoga o dispositivo
  status                       estado de todos os nodos
  demo                         fluxo completo con verificacións`)
	os.Exit(2)
}

func need(args []string, n int) {
	if len(args) < n {
		usage()
	}
}

// cmdInit: opcións dun sistema pechado. Descubrimento local activado porque
// os pares introducidos teñen enderezo "dynamic".
func cmdInit(ctx context.Context) error {
	for _, n := range allNodes {
		c := node(n)
		if err := c.WaitHealthy(ctx); err != nil {
			return err
		}
		err := c.PatchOptions(ctx, stapi.Obj{
			"globalAnnounceEnabled": false,
			"localAnnounceEnabled":  true,
			"relaysEnabled":         false,
			"natEnabled":            false,
			"urAccepted":            -1,
			"crashReportingEnabled": false,
			"autoUpgradeIntervalH":  0,
		})
		if err != nil {
			return err
		}
		id, err := c.MyID(ctx)
		if err != nil {
			return err
		}
		if err := c.EnsureDevice(ctx, id, stapi.Obj{"name": n}); err != nil {
			return err
		}
		fmt.Printf("%-11s %s\n", n, id)
	}
	return nil
}

func cmdShare(ctx context.Context, folderID, label string) error {
	err := node(server).EnsureFolder(ctx, folderID, stapi.Obj{
		"label": label,
		"path":  dataRoot + "/" + folderID,
		"type":  "sendreceive",
	})
	if err == nil {
		fmt.Printf("servidor: carpeta %q (%s) creada\n", folderID, label)
	}
	return err
}

// cmdPair simula o resultado do emparellamento con OTP: o servidor autoriza o
// Device ID do cliente nas súas carpetas e o cliente engade o servidor como
// introducer.
func cmdPair(ctx context.Context, client string, folders []string) error {
	srv, cli := node(server), node(client)
	srvID, err := srv.MyID(ctx)
	if err != nil {
		return err
	}
	cliID, err := cli.MyID(ctx)
	if err != nil {
		return err
	}
	if err := srv.EnsureDevice(ctx, cliID, stapi.Obj{
		"name":      client,
		"addresses": []string{"dynamic"},
	}); err != nil {
		return err
	}
	for _, f := range folders {
		if err := srv.ShareFolder(ctx, f, cliID); err != nil {
			return err
		}
	}
	if err := cli.EnsureDevice(ctx, srvID, stapi.Obj{
		"name":       "servidor",
		"addresses":  []string{"tcp://" + server + ":22000"},
		"introducer": true,
	}); err != nil {
		return err
	}
	fmt.Printf("%s emparellado co servidor; carpetas autorizadas: %s\n", client, strings.Join(folders, ", "))
	return nil
}

func cmdPending(ctx context.Context, client string) error {
	p, err := node(client).PendingFolders(ctx)
	if err != nil {
		return err
	}
	if len(p) == 0 {
		fmt.Println("(sen carpetas pendentes)")
	}
	for id, pf := range p {
		for dev, o := range pf.OfferedBy {
			fmt.Printf("%s  etiqueta=%q  ofrecida por %s\n", id, o.Label, short(dev))
		}
	}
	return nil
}

func cmdAccept(ctx context.Context, client, folderID string) error {
	cli := node(client)
	p, err := cli.PendingFolders(ctx)
	if err != nil {
		return err
	}
	pf, ok := p[folderID]
	if !ok {
		return fmt.Errorf("%s: a carpeta %q non está pendente", client, folderID)
	}
	var devices []stapi.Obj
	label := folderID
	for dev, o := range pf.OfferedBy {
		devices = append(devices, stapi.Obj{"deviceID": dev})
		if o.Label != "" {
			label = o.Label
		}
	}
	err = cli.EnsureFolder(ctx, folderID, stapi.Obj{
		"label":   label,
		"path":    dataRoot + "/" + label,
		"type":    "sendreceive",
		"devices": devices,
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s aceptou %q en %s/%s\n", client, folderID, dataRoot, label)
	// Forzar que o introducer reenvíe a ClusterConfig (ver stapi.Reconnect).
	for dev := range pf.OfferedBy {
		if err := cli.Reconnect(ctx, dev); err != nil {
			return err
		}
	}
	return nil
}

func cmdRevoke(ctx context.Context, client string) error {
	cliID, err := node(client).MyID(ctx)
	if err != nil {
		return err
	}
	if err := node(server).DeleteDevice(ctx, cliID); err != nil {
		return err
	}
	fmt.Printf("servidor: %s revogado\n", client)
	return nil
}

func cmdStatus(ctx context.Context) error {
	names := map[string]string{}
	for _, n := range allNodes {
		if id, err := node(n).MyID(ctx); err == nil {
			names[id] = n
		}
	}
	label := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return short(id)
	}
	for _, n := range allNodes {
		c := node(n)
		myID, err := c.MyID(ctx)
		if err != nil {
			fmt.Printf("== %s: %v\n", n, err)
			continue
		}
		fmt.Printf("== %s (%s)\n", n, short(myID))
		devs, err := c.Devices(ctx)
		if err != nil {
			return err
		}
		conns, err := c.Connections(ctx)
		if err != nil {
			return err
		}
		for _, d := range devs {
			id, _ := d["deviceID"].(string)
			if id == myID {
				continue
			}
			extra := ""
			if d["introducer"] == true {
				extra += " introducer"
			}
			if by, _ := d["introducedBy"].(string); by != "" {
				extra += " introducido-por=" + label(by)
			}
			st := "desconectado"
			if cn := conns[id]; cn.Connected {
				st = "conectado " + cn.Address
			}
			fmt.Printf("   dispositivo %-11s %s%s\n", label(id), st, extra)
		}
		folders, err := c.Folders(ctx)
		if err != nil {
			return err
		}
		for _, f := range folders {
			var with []string
			for _, d := range asList(f["devices"]) {
				if id, _ := d["deviceID"].(string); id != myID {
					with = append(with, label(id))
				}
			}
			sort.Strings(with)
			fmt.Printf("   carpeta %q -> %v compartida con [%s]\n", f["id"], f["path"], strings.Join(with, " "))
		}
	}
	return nil
}

// cmdDemo executa o fluxo completo e verifica cada paso.
func cmdDemo(ctx context.Context) error {
	const folder = "proba"
	step := func(s string) { fmt.Printf("\n### %s\n", s) }

	step("Inicializar nodos")
	if err := cmdInit(ctx); err != nil {
		return err
	}
	ids := map[string]string{}
	for _, n := range allNodes {
		ids[n], _ = node(n).MyID(ctx)
	}

	step("Servidor crea a carpeta")
	if err := cmdShare(ctx, folder, "Proba"); err != nil {
		return err
	}

	for _, cl := range []string{"st-client1", "st-client2"} {
		step("Emparellar e aceptar: " + cl)
		if err := cmdPair(ctx, cl, []string{folder}); err != nil {
			return err
		}
		if err := waitFor(ctx, cl+" recibe a oferta (pending folders)", func() (bool, error) {
			p, err := node(cl).PendingFolders(ctx)
			_, ok := p[folder]
			return ok, err
		}); err != nil {
			return err
		}
		if err := cmdAccept(ctx, cl, folder); err != nil {
			return err
		}
	}

	step("Introducer: os clientes deben coñecerse e conectar entre eles (P2P)")
	for _, pair := range [][2]string{{"st-client1", "st-client2"}, {"st-client2", "st-client1"}} {
		a, b := pair[0], pair[1]
		if err := waitFor(ctx, a+" coñece a "+b+" introducido polo servidor", func() (bool, error) {
			return introducedIn(ctx, a, ids[b], ids[server])
		}); err != nil {
			return err
		}
		// Esíxense 3 comprobacións seguidas: unha conexión rexeitada pode
		// aparecer un instante como "connected".
		stable := 0
		if err := waitFor(ctx, a+" conectado directamente a "+b, func() (bool, error) {
			c, err := node(a).Connections(ctx)
			if c[ids[b]].Connected {
				stable++
			} else {
				stable = 0
			}
			return stable >= 3, err
		}); err != nil {
			return err
		}
	}

	step("Sincronización: ficheiro creado en st-client1 aparece en st-client2")
	name := fmt.Sprintf("ola-%d.txt", time.Now().Unix())
	src := filepath.Join(nodesMount, "st-client1/data/Proba", name)
	if err := os.WriteFile(src, []byte("ola desde client1\n"), 0o644); err != nil {
		return err
	}
	dst := filepath.Join(nodesMount, "st-client2/data/Proba", name)
	if err := waitFor(ctx, "ficheiro en st-client2", func() (bool, error) {
		_, err := os.Stat(dst)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		return err
	}

	step("Revogar st-client1: st-client2 debe esquecelo (baixa vía introducer)")
	if err := cmdRevoke(ctx, "st-client1"); err != nil {
		return err
	}
	if err := waitFor(ctx, "st-client2 elimina a st-client1", func() (bool, error) {
		ok, err := introducedIn(ctx, "st-client2", ids["st-client1"], ids[server])
		return !ok, err
	}); err != nil {
		return err
	}

	step("Estado final")
	return cmdStatus(ctx)
}

// introducedIn indica se o nodo n ten o dispositivo dev introducido por by.
func introducedIn(ctx context.Context, n, dev, by string) (bool, error) {
	devs, err := node(n).Devices(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range devs {
		if d["deviceID"] == dev {
			return d["introducedBy"] == by, nil
		}
	}
	return false, nil
}

func waitFor(ctx context.Context, what string, cond func() (bool, error)) error {
	start := time.Now()
	deadline := start.Add(90 * time.Second)
	for {
		ok, err := cond()
		if err != nil && !errors.Is(err, stapi.ErrNotFound) {
			return fmt.Errorf("%s: %w", what, err)
		}
		if ok {
			fmt.Printf("  OK  %s (%.1fs)\n", what, time.Since(start).Seconds())
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("esgotado o tempo agardando: %s", what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func asList(v any) []map[string]any {
	var out []map[string]any
	l, _ := v.([]any)
	for _, e := range l {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func short(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}
