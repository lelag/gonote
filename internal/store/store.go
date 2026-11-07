// Package store manages private, atomic local configuration and cache files.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lelag/gonote"
)

type Config struct {
	URL             string             `json:"url"`
	Auth            gonote.Reconnect   `json:"auth"`
	Profile         gonote.Profile     `json:"profile"`
	Timezone        string             `json:"timezone"`
	ENT             gonote.ENTProvider `json:"ent,omitempty"`
	ENTUsername     string             `json:"ent_username,omitempty"`
	LoginUsername   string             `json:"login_username,omitempty"`
	PasswordStorage string             `json:"password_storage,omitempty"`
}

func (c Config) Username() string {
	if c.ENT != "" {
		return c.ENTUsername
	}
	if c.LoginUsername != "" {
		return c.LoginUsername
	}
	return c.Auth.Username
}

// CredentialKey is stable across reconnect-token rotation, and separates schools
// and identity providers. Passwords and tokens never participate in this key.
func (c Config) CredentialKey() string {
	h := sha256.Sum256([]byte(c.URL + "\x00" + string(c.ENT) + "\x00" + c.Username()))
	return hex.EncodeToString(h[:])
}

type Envelope struct {
	Query   string          `json:"query,omitempty"`
	Fetched time.Time       `json:"fetched"`
	Profile gonote.Profile  `json:"profile"`
	Data    json.RawMessage `json:"data"`
}
type Record struct {
	Ref     string          `json:"ref"`
	Kind    string          `json:"kind"`
	Fetched time.Time       `json:"fetched"`
	Data    json.RawMessage `json:"data"`
}
type Store struct{ ConfigDir, CacheDir string }

func Default() (Store, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return Store{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Store{}, err
	}
	s := Store{ConfigDir: filepath.Join(config, "gonote"), CacheDir: filepath.Join(cache, "gonote")}
	// Keep existing prototype accounts usable without copying their secrets.
	if _, err := os.Stat(filepath.Join(s.ConfigDir, "compte.json")); os.IsNotExist(err) {
		legacy := filepath.Join(config, "pronote")
		if _, err := os.Stat(filepath.Join(legacy, "compte.json")); err == nil {
			s.ConfigDir = legacy
			s.CacheDir = filepath.Join(cache, "pronote")
		}
	}
	return s, nil
}
func (s Store) LoadConfig() (Config, error) {
	var cfg Config
	err := Read(filepath.Join(s.ConfigDir, "compte.json"), &cfg)
	return cfg, err
}
func (s Store) SaveConfig(cfg Config) error {
	return Write(filepath.Join(s.ConfigDir, "compte.json"), cfg)
}
func (s Store) Account(cfg Config) Store {
	identity := cfg.Username()
	if cfg.ENT != "" && cfg.ENTUsername != "" {
		identity = string(cfg.ENT) + "\x00" + cfg.ENTUsername
	}
	h := sha256.Sum256([]byte(cfg.URL + "\x00" + identity))
	s.CacheDir = filepath.Join(s.CacheDir, hex.EncodeToString(h[:12]))
	return s
}
func (s Store) Load(key string) (Envelope, error) {
	var e Envelope
	err := Read(s.cachePath(key), &e)
	return e, err
}
func (s Store) Save(key string, e Envelope) error { e.Query = key; return Write(s.cachePath(key), e) }

// Covering finds the freshest saved interval containing the requested interval.
// It does not combine partial snapshots, which could hide gaps in school data.
func (s Store) Covering(key string) (Envelope, error) {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return Envelope{}, os.ErrNotExist
	}
	files, err := os.ReadDir(s.CacheDir)
	if err != nil {
		return Envelope{}, err
	}
	var best Envelope
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		var e Envelope
		if Read(filepath.Join(s.CacheDir, file.Name()), &e) != nil {
			continue
		}
		p := strings.Split(e.Query, ":")
		if len(p) != 3 || p[0] != parts[0] || p[1] > parts[1] || p[2] < parts[2] {
			continue
		}
		if best.Fetched.IsZero() || e.Fetched.After(best.Fetched) {
			best = e
		}
	}
	if best.Fetched.IsZero() {
		return best, os.ErrNotExist
	}
	return best, nil
}
func (s Store) cachePath(key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(s.CacheDir, hex.EncodeToString(h[:])+".json")
}
func Reference(kind, id string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + id))
	return kind + "-" + hex.EncodeToString(h[:4])
}
func (s Store) Remember(kind, id string, data any, fetched time.Time) (string, error) {
	ref := Reference(kind, id)
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	err = Write(filepath.Join(s.CacheDir, "details", ref+".json"), Record{Ref: ref, Kind: kind, Fetched: fetched, Data: b})
	return ref, err
}
func (s Store) Find(ref string) (Record, error) {
	var r Record
	if len(ref) != 10 || ref[1] != '-' {
		return r, fmt.Errorf("référence invalide")
	}
	if _, err := hex.DecodeString(ref[2:]); err != nil {
		return r, fmt.Errorf("référence invalide")
	}
	err := Read(filepath.Join(s.CacheDir, "details", ref+".json"), &r)
	return r, err
}
func Read(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil {
		return err
	}
	if len(b) > 32<<20 {
		return fmt.Errorf("fichier local trop volumineux")
	}
	return json.Unmarshal(b, v)
}
func Write(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gonote-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// InvalidateHomework removes all cached intervals, including overlapping ranges,
// and the selected detail before a mutation whose outcome could be uncertain.
func (s Store) InvalidateHomework(ref string) error {
	if len(ref) != 10 || !strings.HasPrefix(ref, "h-") {
		return fmt.Errorf("référence de devoir invalide")
	}
	if _, err := hex.DecodeString(ref[2:]); err != nil {
		return fmt.Errorf("référence de devoir invalide")
	}
	files, err := os.ReadDir(s.CacheDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.CacheDir, file.Name())
		var e Envelope
		if err = Read(path, &e); err != nil {
			return err
		}
		if strings.HasPrefix(e.Query, "homework:") {
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	err = os.Remove(filepath.Join(s.CacheDir, "details", ref+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
