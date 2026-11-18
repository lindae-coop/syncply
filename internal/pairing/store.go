package pairing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State é o rexistro propio do servidor: Syncthing non sabe que varios
// Device IDs son do mesmo usuario. Gárdase nun JSON (abonda para o prototipo).
type State struct {
	Users map[string]*UserState `json:"users"`
}

type UserState struct {
	// Último contador TOTP aceptado: impide reutilizar un código.
	LastOTPCounter int64     `json:"lastOtpCounter"`
	Failures       int       `json:"failures"`
	LockedUntil    time.Time `json:"lockedUntil,omitzero"`
	Devices        []Device  `json:"devices"`
}

type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AddedAt   time.Time `json:"addedAt"`
	TokenHash string    `json:"tokenHash"` // sha256 do token do equipo
}

func loadState(path string) (*State, error) {
	s := &State{Users: map[string]*UserState{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Users == nil {
		s.Users = map[string]*UserState{}
	}
	return s, nil
}

// save escribe de forma atómica (temporal + rename).
func (s *State) save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *State) userState(name string) *UserState {
	us := s.Users[name]
	if us == nil {
		us = &UserState{}
		s.Users[name] = us
	}
	return us
}

// owner devolve o usuario e o índice do dispositivo con ese ID.
func (s *State) owner(deviceID string) (string, int) {
	for name, us := range s.Users {
		for i, d := range us.Devices {
			if d.ID == deviceID {
				return name, i
			}
		}
	}
	return "", -1
}
