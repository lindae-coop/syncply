package pairing

import (
	"context"
	"fmt"

	"github.com/DanBaDo/cld/internal/totp"
)

// Options é a configuración propia do servizo.
type Options struct {
	// Enderezos do servidor Syncthing que se lles dan aos clientes.
	ServerAddresses   []string `json:"serverAddresses"`
	MaxDevicesPerUser int      `json:"maxDevicesPerUser"`
}

func (o *Options) validate() error {
	if len(o.ServerAddresses) == 0 {
		return fmt.Errorf("config: faltan serverAddresses")
	}
	if o.MaxDevicesPerUser <= 0 {
		o.MaxDevicesPerUser = 5
	}
	return nil
}

// Directory é a fonte de usuarios e recursos (SFTPGo). Consúltase en cada
// emparellamento, revogación e reconciliación, así que os cambios feitos en
// SFTPGo aplícanse sen reiniciar o servizo.
type Directory interface {
	Snapshot(ctx context.Context) (*Snapshot, error)
}

// Snapshot é o estado dos usuarios nun momento dado. Só inclúe usuarios que
// poden acceder (activos e sen caducar) e, para cada un, os recursos que se
// poden sincronizar.
type Snapshot struct {
	Users   []User
	Folders []Folder
}

type Folder struct {
	ID    string // ID da carpeta en Syncthing
	Label string
	Path  string // ruta no servidor Syncthing
}

type User struct {
	Name    string
	TOTP    *TOTP    // nil: o usuario non ten TOTP e non pode emparellar equipos
	Folders []string // IDs de Folder
}

type TOTP struct {
	Secret []byte
	Algo   totp.Algo
}

func (s *Snapshot) user(name string) *User {
	for i := range s.Users {
		if s.Users[i].Name == name {
			return &s.Users[i]
		}
	}
	return nil
}

// Validate comproba a coherencia da instantánea.
func (s *Snapshot) Validate() error {
	folders := map[string]bool{}
	for _, f := range s.Folders {
		if f.ID == "" || f.Path == "" {
			return fmt.Errorf("carpeta sen id ou path")
		}
		if folders[f.ID] {
			return fmt.Errorf("carpeta %q duplicada", f.ID)
		}
		folders[f.ID] = true
	}
	users := map[string]bool{}
	for _, u := range s.Users {
		if u.Name == "" || users[u.Name] {
			return fmt.Errorf("usuario baleiro ou duplicado %q", u.Name)
		}
		users[u.Name] = true
		if u.TOTP != nil && !u.TOTP.Algo.Valid() {
			return fmt.Errorf("usuario %q: algoritmo TOTP %q non admitido", u.Name, u.TOTP.Algo)
		}
		for _, f := range u.Folders {
			if !folders[f] {
				return fmt.Errorf("usuario %q: carpeta descoñecida %q", u.Name, f)
			}
		}
	}
	return nil
}

// StaticDirectory devolve sempre a mesma instantánea (tests).
type StaticDirectory struct{ S *Snapshot }

func (d StaticDirectory) Snapshot(context.Context) (*Snapshot, error) { return d.S, nil }
