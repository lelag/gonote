package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/store"
)

type statusSchool struct {
	*fakeSchool
	assignment    gonote.Assignment
	reads, writes int
	writeErr      error
	ignoreWrite   bool
	missing       bool
	readbackErr   error
}

func (s *statusSchool) Homework(context.Context, gonote.DateRange) ([]gonote.Assignment, error) {
	s.reads++
	s.state.Order += 2
	if s.writes > 0 && s.readbackErr != nil {
		return nil, s.readbackErr
	}
	if s.missing {
		return []gonote.Assignment{}, nil
	}
	return []gonote.Assignment{s.assignment}, nil
}
func (s *statusSchool) SetHomeworkDone(_ context.Context, id string, done bool) error {
	s.writes++
	s.state.Order += 2
	if id != s.assignment.ID {
		return errors.New("wrong assignment ID")
	}
	if !s.ignoreWrite {
		s.assignment.Done = done
	}
	return s.writeErr
}
func statusApp(t *testing.T, initial bool) (*App, *statusSchool, string) {
	t.Helper()
	a, cfg := configuredApp(t)
	if err := a.prepare(); err != nil {
		t.Fatal(err)
	}
	assignment := gonote.Assignment{ID: "homework-id", Due: a.Now().AddDate(0, 0, 2), Subject: gonote.Resource{Name: "Mathématiques"}, Description: "Exercices", Done: initial}
	ref, err := a.Store.Remember("h", assignment.ID, assignment, a.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal([]gonote.Assignment{assignment})
	for _, key := range []string{"homework:2026-11-01:2026-12-01", "homework:2026-11-02:2026-11-10"} {
		if err = a.Store.Save(key, store.Envelope{Fetched: a.Now(), Data: b}); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.Store.Save("grades:existing", store.Envelope{Fetched: a.Now(), Data: json.RawMessage(`{}`)})
	s := &statusSchool{fakeSchool: fakeSession(cfg), assignment: assignment}
	a.loginClient = func(_ context.Context, _ string, _ gonote.Credentials, o gonote.Options) (schoolClient, error) {
		if !o.AllowWrites {
			t.Fatal("mutation permission not explicitly enabled")
		}
		return s, nil
	}
	t.Setenv("PRONOTE_PASSWORD", "fake-password")
	return a, s, ref
}
func TestHomeworkDoneAndTodoCommandsConfirmAndRefreshCache(t *testing.T) {
	for _, cmd := range []string{"fait", "done", "a-faire", "todo"} {
		t.Run(cmd, func(t *testing.T) {
			initial := cmd == "a-faire" || cmd == "todo"
			a, s, ref := statusApp(t, initial)
			if err := execute(a, cmd, ref, "--json"); err != nil {
				t.Fatal(err)
			}
			if s.writes != 1 || s.reads != 2 || s.assignment.Done == initial {
				t.Fatal("did not set and confirm status")
			}
			for _, key := range []string{"homework:2026-11-01:2026-12-01", "homework:2026-11-02:2026-11-10"} {
				if _, err := a.Store.Load(key); err == nil {
					t.Fatal("overlapping cached list retained")
				}
			}
			if _, err := a.Store.Load("grades:existing"); err != nil {
				t.Fatal("other cache removed")
			}
			record, err := a.Store.Find(ref)
			if err != nil {
				t.Fatal(err)
			}
			var stored gonote.Assignment
			_ = json.Unmarshal(record.Data, &stored)
			if stored.Done != s.assignment.Done {
				t.Fatal("stale detail")
			}
			var payload struct {
				Data struct {
					Changed    bool
					Assignment gonote.Assignment
				}
			}
			if err = json.Unmarshal(a.Out.(interface{ Bytes() []byte }).Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if !payload.Data.Changed || payload.Data.Assignment.Done != s.assignment.Done {
				t.Fatal("wrong JSON result")
			}
		})
	}
}
func TestHomeworkStatusNoOpUsesLiveState(t *testing.T) {
	a, s, ref := statusApp(t, false)
	s.assignment.Done = true
	if err := execute(a, "fait", ref); err != nil {
		t.Fatal(err)
	}
	if s.writes != 0 || s.reads != 1 {
		t.Fatal("no-op used cache or wrote anyway")
	}
}
func TestHomeworkMutationDoesNotRetryOrKeepStaleCacheAfterLostResponse(t *testing.T) {
	a, s, ref := statusApp(t, false)
	s.writeErr = gonote.ErrUnavailable
	err := execute(a, "fait", ref)
	if err == nil || !strings.Contains(err.Error(), "non confirmée") {
		t.Fatal(err)
	}
	if s.writes != 1 || s.reads != 1 {
		t.Fatal("uncertain mutation was retried")
	}
	if _, err = a.Store.Find(ref); err == nil {
		t.Fatal("uncertain old detail retained")
	}
	if _, err = a.Store.Load("homework:2026-11-01:2026-12-01"); err == nil {
		t.Fatal("uncertain old list retained")
	}
	a.ctx = context.Background()
	defer a.Close()
	if err = a.lockSession(); err != nil {
		t.Fatal(err)
	}
	saved, err := a.session.Load()
	if err != nil || saved.Usable {
		t.Fatal("uncertain request counter retained", err)
	}
}
func TestHomeworkMutationRequiresConfirmedReadback(t *testing.T) {
	for _, failRead := range []bool{false, true} {
		a, s, ref := statusApp(t, false)
		if failRead {
			s.readbackErr = gonote.ErrUnavailable
		} else {
			s.ignoreWrite = true
		}
		if err := execute(a, "fait", ref); err == nil {
			t.Fatal("reported success without confirmation")
		}
		if s.writes != 1 {
			t.Fatal("write repeated")
		}
		if _, err := a.Store.Find(ref); err == nil {
			t.Fatal("unconfirmed detail retained")
		}
	}
}
func TestHomeworkStatusRejectsUnsupportedModesAndWrongOrGoneReferences(t *testing.T) {
	for _, args := range [][]string{{"fait", "h-12345678", "--demo"}, {"fait", "h-12345678", "--hors-ligne"}, {"done"}, {"todo", "x", "y"}} {
		a, _ := appForTest(t)
		a.loginClient = func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error) {
			t.Fatal("invalid command connected")
			return nil, nil
		}
		if err := execute(a, args...); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	a, s, ref := statusApp(t, false)
	s.missing = true
	if err := execute(a, "fait", ref); err == nil {
		t.Fatal("missing homework accepted")
	}
	if s.writes != 0 {
		t.Fatal("missing homework written")
	}
	a, s, _ = statusApp(t, false)
	other, err := a.Store.Remember("l", "lesson", struct{}{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = execute(a, "fait", other); err == nil {
		t.Fatal("lesson accepted")
	}
	if s.reads != 0 || s.writes != 0 {
		t.Fatal("wrong record reached server")
	}
}
