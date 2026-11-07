package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/lelag/gonote"
)

// SessionFile is held exclusively for the entire restore/use/save sequence.
// Usable is cleared before sending requests, so an interrupted command cannot
// leave a stale request counter available to another process.
type SessionFile struct {
	lock *flock.Flock
	path string
}
type SavedSession struct {
	Usable bool                `json:"usable"`
	State  gonote.SessionState `json:"state"`
}

func (s Store) LockSession(ctx context.Context, c Config) (*SessionFile, error) {
	dir := filepath.Join(s.ConfigDir, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, c.CredentialKey())
	l := flock.New(path+".lock", flock.SetPermissions(0600))
	ok, err := l.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !ok {
		_ = l.Close()
		if err == nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("attente de la session : %w", err)
	}
	return &SessionFile{lock: l, path: path + ".json"}, nil
}
func (s *SessionFile) Load() (SavedSession, error) {
	var v SavedSession
	err := Read(s.path, &v)
	return v, err
}
func (s *SessionFile) Save(v SavedSession) error { return Write(s.path, v) }
func (s *SessionFile) Close() error              { return s.lock.Close() }
