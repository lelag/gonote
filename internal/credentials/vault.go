// Package credentials stores account secrets separately from cached school data.
package credentials

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zalando/go-keyring"

	"github.com/lelag/gonote/internal/store"
)

// The keyring namespace is stable so prototype credentials remain accessible.
const keyringService = "pronote-cli"

const (
	Keyring = "trousseau"
	File    = "fichier"
)

var (
	ErrNotFound    = errors.New("aucun mot de passe mémorisé")
	ErrUnavailable = errors.New("stockage des identifiants indisponible")
)

type Store interface {
	Get(mode, key string) (string, error)
	Set(mode, key, password string) error
	Delete(mode, key string) error
}

type Vault struct{ Directory string }

func New(configDir string) *Vault { return &Vault{Directory: filepath.Join(configDir, "identifiants")} }
func ValidMode(mode string) bool  { return mode == Keyring || mode == File }
func validKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

func (v *Vault) Get(mode, key string) (string, error) {
	if !validKey(key) {
		return "", ErrUnavailable
	}
	switch mode {
	case Keyring:
		secret, err := keyring.Get(keyringService, key)
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", ErrUnavailable
		}
		return secret, nil
	case File:
		if runtime.GOOS == "windows" {
			return "", ErrUnavailable
		}
		path := filepath.Join(v.Directory, key+".json")
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", ErrUnavailable
		}
		var saved struct {
			Password string `json:"password"`
		}
		if err = store.Read(path, &saved); err != nil || saved.Password == "" {
			return "", ErrUnavailable
		}
		return saved.Password, nil
	case "":
		return "", ErrNotFound
	default:
		return "", ErrUnavailable
	}
}
func (v *Vault) Set(mode, key, password string) error {
	if !validKey(key) || password == "" {
		return ErrUnavailable
	}
	switch mode {
	case Keyring:
		if err := keyring.Set(keyringService, key, password); err != nil {
			return ErrUnavailable
		}
		return nil
	case File:
		if runtime.GOOS == "windows" {
			return ErrUnavailable
		}
		if err := store.Write(filepath.Join(v.Directory, key+".json"), struct {
			Password string `json:"password"`
		}{password}); err != nil {
			return ErrUnavailable
		}
		return nil
	default:
		return ErrUnavailable
	}
}
func (v *Vault) Delete(mode, key string) error {
	if !validKey(key) {
		return ErrUnavailable
	}
	var err error
	switch mode {
	case Keyring:
		err = keyring.Delete(keyringService, key)
	case File:
		err = os.Remove(filepath.Join(v.Directory, key+".json"))
	case "":
		return nil
	default:
		return ErrUnavailable
	}
	if err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return ErrUnavailable
}
