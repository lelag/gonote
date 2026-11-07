package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionLockSerializesProcessesAndPersistsDirtyState(t *testing.T) {
	s := Store{ConfigDir: t.TempDir()}
	cfg := Config{URL: "https://school.example/pronote", LoginUsername: "student"}
	first, err := s.LockSession(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Save(SavedSession{Usable: false}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err = s.LockSession(ctx, cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("competing command did not block: %v", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := s.LockSession(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	saved, err := next.Load()
	if err != nil || saved.Usable {
		t.Fatal("lost interrupted-request marker", err)
	}
	info, err := os.Stat(filepath.Join(s.ConfigDir, "sessions", cfg.CredentialKey()+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("session not private", err)
	}
}
