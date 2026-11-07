package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lelag/gonote"
)

func TestPrivateAtomicStorageAndAccountIsolation(t *testing.T) {
	s := Store{ConfigDir: filepath.Join(t.TempDir(), "config"), CacheDir: filepath.Join(t.TempDir(), "cache")}
	cfg := Config{URL: "https://school.example"}
	cfg.Auth.Username = "Alice"
	cfg.Auth.Token = "secret-token"
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.ConfigDir, "compte.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	dir, err := os.Stat(s.ConfigDir)
	if err != nil || dir.Mode().Perm() != 0700 {
		t.Fatal("configuration directory is not private")
	}
	loaded, err := s.LoadConfig()
	if err != nil || loaded.Auth.Token != "secret-token" {
		t.Fatal(err)
	}
	first := s.Account(cfg)
	cfg.Auth.Username = "Bob"
	second := s.Account(cfg)
	if first.CacheDir == second.CacheDir {
		t.Fatal("account caches overlap")
	}
	if _, err := first.Find("../../etc/passwd"); err == nil {
		t.Fatal("accepted path traversal")
	}
	if Reference("h", "id") != Reference("h", "id") || Reference("h", "id") == Reference("l", "id") {
		t.Fatal("bad reference namespace")
	}
}

func TestENTConfigSurvivesTokenRotationWithoutChangingCache(t *testing.T) {
	s := Store{ConfigDir: filepath.Join(t.TempDir(), "config"), CacheDir: filepath.Join(t.TempDir(), "cache")}
	cfg := Config{URL: "https://school.example/pronote", ENT: gonote.ENTEclatBFC, ENTUsername: "edu-student", Auth: gonote.Reconnect{Username: "temporary-pronote-id", Token: "fake-mobile-token"}}
	before := s.Account(cfg).CacheDir
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ENT != gonote.ENTEclatBFC || loaded.ENTUsername != "edu-student" {
		t.Fatal("ENT identity was not persisted")
	}
	loaded.Auth.Username = "new-pronote-id"
	loaded.Auth.Token = "rotated-token"
	if before != s.Account(loaded).CacheDir {
		t.Fatal("token rotation moved the account cache")
	}
	loaded.ENTUsername = "another-student"
	if before == s.Account(loaded).CacheDir {
		t.Fatal("different ENT accounts share cache")
	}
}

func TestDefaultReusesPrototypeAccountWithoutCopying(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG defaults are Linux-specific")
	}
	configRoot, cacheRoot := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	s, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigDir != filepath.Join(configRoot, "gonote") {
		t.Fatal("new installs must use gonote")
	}
	legacy := Store{ConfigDir: filepath.Join(configRoot, "pronote")}
	if err = legacy.SaveConfig(Config{URL: "https://school.example/pronote"}); err != nil {
		t.Fatal(err)
	}
	s, err = Default()
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigDir != legacy.ConfigDir || s.CacheDir != filepath.Join(cacheRoot, "pronote") {
		t.Fatal("prototype account not reused")
	}
	modern := Store{ConfigDir: filepath.Join(configRoot, "gonote")}
	if err = modern.SaveConfig(Config{URL: "https://other.example/pronote"}); err != nil {
		t.Fatal(err)
	}
	s, err = Default()
	if err != nil || s.ConfigDir != modern.ConfigDir {
		t.Fatal("modern account must take precedence", err)
	}
}
func TestReadRejectsOversizedLocalFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(33 << 20); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	var data any
	if err = Read(path, &data); err == nil || !strings.Contains(err.Error(), "trop volumineux") {
		t.Fatalf("size limit not enforced: %v", err)
	}
}
