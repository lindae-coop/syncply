package pairing

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/DanBaDo/cld/internal/totp"
)

// Config describe usuarios e carpetas. No prototipo vén dun ficheiro; máis
// adiante sairá de SFTPGo (usuarios e directorios con permiso de escritura).
type Config struct {
	// Enderezos do servidor Syncthing que se lles dan aos clientes.
	ServerAddresses   []string `json:"serverAddresses"`
	MaxDevicesPerUser int      `json:"maxDevicesPerUser"`
	Folders           []Folder `json:"folders"`
	Users             []User   `json:"users"`
}

type Folder struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"` // ruta no servidor Syncthing
}

type User struct {
	Name       string   `json:"name"`
	TOTPSecret string   `json:"totpSecret"` // base32
	Folders    []string `json:"folders"`

	secret []byte
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, c.validate()
}

func (c *Config) validate() error {
	if len(c.ServerAddresses) == 0 {
		return fmt.Errorf("config: faltan serverAddresses")
	}
	if c.MaxDevicesPerUser <= 0 {
		c.MaxDevicesPerUser = 5
	}
	folders := map[string]bool{}
	for _, f := range c.Folders {
		if f.ID == "" || f.Path == "" {
			return fmt.Errorf("config: carpeta sen id ou path")
		}
		if folders[f.ID] {
			return fmt.Errorf("config: carpeta %q duplicada", f.ID)
		}
		folders[f.ID] = true
	}
	users := map[string]bool{}
	for i := range c.Users {
		u := &c.Users[i]
		if u.Name == "" || users[u.Name] {
			return fmt.Errorf("config: usuario baleiro ou duplicado %q", u.Name)
		}
		users[u.Name] = true
		s, err := totp.DecodeSecret(u.TOTPSecret)
		if err != nil {
			return fmt.Errorf("config: usuario %q: %w", u.Name, err)
		}
		u.secret = s
		for _, f := range u.Folders {
			if !folders[f] {
				return fmt.Errorf("config: usuario %q: carpeta descoñecida %q", u.Name, f)
			}
		}
	}
	return nil
}

func (c *Config) user(name string) *User {
	for i := range c.Users {
		if c.Users[i].Name == name {
			return &c.Users[i]
		}
	}
	return nil
}
