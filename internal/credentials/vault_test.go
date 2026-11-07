package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitPrivateFileStorage(t *testing.T) {
	v := New(t.TempDir())
	key := strings.Repeat("a", 64)
	if _, err := v.Get(File, key); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := v.Set(File, key, "fake-secret"); err != nil {
		t.Fatal(err)
	}
	secret, err := v.Get(File, key)
	if err != nil || secret != "fake-secret" {
		t.Fatal("secret not restored", err)
	}
	path := filepath.Join(v.Directory, key+".json")
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("not private")
	}
	if err = v.Set(File, key, "updated"); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = v.Get(File, key); !errors.Is(err, ErrUnavailable) {
		t.Fatal("read non-private secret")
	}
	if err = v.Delete(File, key); err != nil {
		t.Fatal(err)
	}
	if err = v.Delete(File, key); err != nil {
		t.Fatal("delete not idempotent")
	}
	if err = v.Set(File, "../bad", "secret"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("invalid key accepted")
	}
}
