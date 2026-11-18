// stproto: ferramenta do prototipo para validar o fluxo servidor/cliente:
// emparellamento con TOTP vía pairsrv, carpetas pendentes, servidor como
// introducer, propagación de pares e revogación.
//
// Execútase no contedor "tools" (ver compose.yaml): make proto ARGS="..."
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

var allNodes = []string{server, "st-client1", "st-client2", "st-client3"}

func node(name string) *stapi.Client {
	return stapi.New(name, "http://"+name+":8384", os.Getenv("ST_APIKEY"))
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(ctx)
	case "pair": // pair <cliente> <usuario> [otp]
		need(args, 2)
		otp := ""
		if len(args) > 2 {
			otp = args[2]
		}
		err = cmdPair(ctx, args[0], args[1], otp)
	case "pending": // pending <cliente>
		need(args, 1)
		err = cmdPending(ctx, args[0])
	case "accept": // accept <cliente> <folderID>
		need(args, 2)
		err = cmdAccept(ctx, args[0], args[1])
	case "devices": // devices <cliente>
		need(args, 1)
		err = cmdDevices(ctx, args[0])
	case "revoke": // revoke <desde> <equipo> <usuario>
		need(args, 3)
		err = cmdRevoke(ctx, args[0], args[1], args[2])
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
  pair <cliente> <usuario> [otp]  emparella vía pairsrv (sen otp: calcúlao
                                 cos segredos de dev/pairsrv.json)
  pending <cliente>            lista as carpetas ofrecidas ao cliente
  accept <cliente> <folderID>  o cliente acepta a carpeta ofrecida
  devices <cliente>            equipos do usuario, vistos desde ese cliente
  revoke <desde> <equipo> <usuario>
                               desde un equipo, revoga outro (pide OTP novo)
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
		"path":    dataRoot + "/" + folderID,
		"type":    "sendreceive",
		"devices": devices,
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s aceptou %q (%s) en %s/%s\n", client, folderID, label, dataRoot, folderID)
	// Forzar que o introducer reenvíe a ClusterConfig (ver stapi.Reconnect).
	for dev := range pf.OfferedBy {
		if err := cli.Reconnect(ctx, dev); err != nil {
			return err
		}
	}
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

// cmdDemo executa o fluxo completo contra pairsrv e verifica cada paso.
// alice ten st-client1 e st-client3 (carpetas comun e alice-docs); bob ten
// st-client2 (só comun).
func cmdDemo(ctx context.Context) error {
	step := func(s string) { fmt.Printf("\n### %s\n", s) }
	expect := func(err error, code int, what string) error {
		if !isStatus(err, code) {
			return fmt.Errorf("%s: esperaba HTTP %d, obtívose %v", what, code, err)
		}
		fmt.Printf("  OK  %s (HTTP %d)\n", what, code)
		return nil
	}

	step("Inicializar nodos e agardar por pairsrv")
	if err := cmdInit(ctx); err != nil {
		return err
	}
	if err := waitPairsrv(ctx); err != nil {
		return err
	}
	ids := map[string]string{}
	for _, n := range allNodes {
		ids[n], _ = node(n).MyID(ctx)
	}

	step("Emparellamentos rexeitados")
	if err := expect(cmdPair(ctx, "st-client1", "alice", "000000"), 401, "OTP incorrecto"); err != nil {
		return err
	}
	if err := expect(cmdPair(ctx, "st-client1", "eve", ""), 401, "usuario inexistente"); err != nil {
		return err
	}

	clients := []struct {
		name, user string
		folders    []string
	}{
		{"st-client1", "alice", []string{"comun", "alice-docs"}},
		{"st-client2", "bob", []string{"comun"}},
		{"st-client3", "alice", []string{"comun", "alice-docs"}},
	}
	for _, c := range clients {
		step(fmt.Sprintf("Emparellar %s (%s) e aceptar as carpetas ofrecidas", c.name, c.user))
		if err := cmdPair(ctx, c.name, c.user, ""); err != nil {
			return err
		}
		var offered []string
		if err := waitFor(ctx, c.name+" recibe as ofertas "+strings.Join(c.folders, ", "), func() (bool, error) {
			p, err := node(c.name).PendingFolders(ctx)
			offered = offered[:0]
			for id := range p {
				offered = append(offered, id)
			}
			for _, f := range c.folders {
				if _, ok := p[f]; !ok {
					return false, err
				}
			}
			return true, err
		}); err != nil {
			return err
		}
		sort.Strings(offered)
		want := slices.Clone(c.folders)
		sort.Strings(want)
		if !slices.Equal(offered, want) {
			return fmt.Errorf("%s: ofertas %v, esperábanse só %v", c.name, offered, want)
		}
		for _, f := range c.folders {
			if err := cmdAccept(ctx, c.name, f); err != nil {
				return err
			}
		}
		if c.name == "st-client1" {
			if err := expect(cmdPair(ctx, "st-client1", "bob", ""), 409, "bob non pode vincular un equipo de alice"); err != nil {
				return err
			}
		}
	}

	step("Permisos no servidor: alice-docs só cos equipos de alice")
	f, err := node(server).Folder(ctx, "alice-docs")
	if err != nil {
		return err
	}
	var with []string
	for _, d := range asList(f["devices"]) {
		if id, _ := d["deviceID"].(string); id != ids[server] {
			with = append(with, id)
		}
	}
	sort.Strings(with)
	want := []string{ids["st-client1"], ids["st-client3"]}
	sort.Strings(want)
	if !slices.Equal(with, want) {
		return fmt.Errorf("alice-docs compartida con %v", with)
	}
	fmt.Println("  OK  alice-docs: st-client1, st-client3")

	step("Introducer: os clientes coñécense e conectan entre eles (P2P)")
	peers := []string{"st-client1", "st-client2", "st-client3"}
	for _, a := range peers {
		for _, b := range peers {
			if a == b {
				continue
			}
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
	}

	step("Sincronización")
	if err := syncCheck(ctx, "st-client1", "comun", "st-client2", "st-client3"); err != nil {
		return err
	}
	if err := syncCheck(ctx, "st-client3", "alice-docs", "st-client1"); err != nil {
		return err
	}

	step("Equipos de alice vistos desde st-client1")
	if err := cmdDevices(ctx, "st-client1"); err != nil {
		return err
	}
	if devs, err := listDevices(ctx, "st-client1"); err != nil || len(devs) != 2 {
		return fmt.Errorf("esperábanse 2 equipos: %v %v", devs, err)
	}

	step("st-client1 revoga st-client3 (con OTP novo)")
	if err := cmdRevoke(ctx, "st-client1", "st-client3", "alice"); err != nil {
		return err
	}
	for _, n := range []string{"st-client1", "st-client2"} {
		if err := waitFor(ctx, n+" esquece a st-client3 (baixa vía introducer)", func() (bool, error) {
			ok, err := introducedIn(ctx, n, ids["st-client3"], ids[server])
			return !ok, err
		}); err != nil {
			return err
		}
	}
	// Xusto despois da revogación as conexións poden aparecer aínda como
	// activas un intre: esíxense 3 comprobacións seguidas sen ningunha.
	stable := 0
	if err := waitFor(ctx, "st-client3 queda sen conexión co servidor e cos pares", func() (bool, error) {
		c, err := node("st-client3").Connections(ctx)
		up := false
		for _, cn := range c {
			up = up || cn.Connected
		}
		if up {
			stable = 0
		} else {
			stable++
		}
		return stable >= 3, err
	}); err != nil {
		return err
	}
	if devs, err := listDevices(ctx, "st-client1"); err != nil || len(devs) != 1 || !devs[0].Current {
		return fmt.Errorf("tras revogar esperábase 1 equipo: %v %v", devs, err)
	}
	fmt.Println("  OK  alice só ten st-client1")
	_, err = listDevices(ctx, "st-client3")
	if err := expect(err, 401, "o token de st-client3 xa non vale"); err != nil {
		return err
	}

	step("Estado final")
	return cmdStatus(ctx)
}

// syncCheck crea un ficheiro na carpeta de "from" e agarda que apareza nos
// demais clientes.
func syncCheck(ctx context.Context, from, folder string, to ...string) error {
	name := fmt.Sprintf("ola-%s-%d.txt", from, time.Now().UnixNano())
	src := filepath.Join(nodesMount, from, "data", folder, name)
	if err := os.WriteFile(src, []byte("ola desde "+from+"\n"), 0o644); err != nil {
		return err
	}
	for _, t := range to {
		dst := filepath.Join(nodesMount, t, "data", folder, name)
		if err := waitFor(ctx, fmt.Sprintf("%s: %s -> %s", folder, from, t), func() (bool, error) {
			_, err := os.Stat(dst)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return err == nil, err
		}); err != nil {
			return err
		}
	}
	return nil
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
