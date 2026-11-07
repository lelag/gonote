package cli

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/credentials"
	"github.com/lelag/gonote/internal/demo"
	"github.com/lelag/gonote/internal/store"
)

type fakeSchool struct {
	*gonote.Client
	state    gonote.SessionState
	homework func() error
	gradeID  string
}

func (f *fakeSchool) Profile() gonote.Profile                               { return f.state.Profile }
func (f *fakeSchool) Reconnect() gonote.Reconnect                           { return f.state.Reconnect }
func (f *fakeSchool) Snapshot(context.Context) (gonote.SessionState, error) { return f.state, nil }
func (f *fakeSchool) Homework(context.Context, gonote.DateRange) ([]gonote.Assignment, error) {
	f.state.Order += 2
	if f.homework != nil {
		return nil, f.homework()
	}
	return []gonote.Assignment{}, nil
}
func (f *fakeSchool) Grades(_ context.Context, id string) (gonote.GradeReport, error) {
	f.gradeID = id
	return gonote.GradeReport{}, nil
}

type fakeVault struct {
	gets     int
	password string
	err      error
}

func (v *fakeVault) Get(mode, key string) (string, error) { v.gets++; return v.password, v.err }
func (v *fakeVault) Set(mode, key, p string) error        { v.password = p; return nil }
func (v *fakeVault) Delete(mode, key string) error        { v.password = ""; return nil }
func configuredApp(t *testing.T) (*App, store.Config) {
	t.Helper()
	a, _ := appForTest(t)
	cfg := store.Config{URL: "https://school.example/pronote", Timezone: "Europe/Paris", LoginUsername: "student", Profile: demo.New(a.Now()).Profile}
	if err := a.Store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return a, cfg
}
func fakeSession(cfg store.Config) *fakeSchool {
	return &fakeSchool{state: gonote.SessionState{URL: cfg.URL, Provider: cfg.ENT, Profile: cfg.Profile}}
}
func TestCommandsReuseSessionBeforeFetchingPassword(t *testing.T) {
	a, cfg := configuredApp(t)
	t.Setenv("PRONOTE_PASSWORD", "fake-password")
	first := fakeSession(cfg)
	logins := 0
	a.loginClient = func(_ context.Context, _ string, c gonote.Credentials, _ gonote.Options) (schoolClient, error) {
		logins++
		if c.Password != "fake-password" {
			t.Fatal("missing initial password")
		}
		return first, nil
	}
	first.homework = func() error {
		saved, err := a.session.Load()
		if err != nil || saved.Usable {
			t.Fatal("network read started without dirty marker", err)
		}
		return nil
	}
	if err := execute(a, "devoirs", "--actualiser", "--json"); err != nil {
		t.Fatal(err)
	}
	b, _ := appForTest(t)
	b.Store = a.Store
	b.loaded = false
	b.Vault = &fakeVault{err: errors.New("vault should not be consulted")}
	b.loginClient = func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error) {
		t.Fatal("new command reauthenticated")
		return nil, nil
	}
	b.restore = func(s gonote.SessionState, _ gonote.Options) (schoolClient, error) {
		if s.Order != 2 {
			t.Fatalf("counter lost: %d", s.Order)
		}
		return &fakeSchool{state: s}, nil
	}
	t.Setenv("PRONOTE_PASSWORD", "")
	if err := execute(b, "devoirs", "--actualiser", "--json"); err != nil {
		t.Fatal(err)
	}
	if logins != 1 || b.Vault.(*fakeVault).gets != 0 {
		t.Fatal("reused session requested a password")
	}
}
func TestExpiredSessionRenewsWithCookiesWithoutPassword(t *testing.T) {
	a, cfg := configuredApp(t)
	cfg.ENT = gonote.ENTEclatBFC
	cfg.ENTUsername = "edu-student"
	_ = a.Store.SaveConfig(cfg)
	old := fakeSession(cfg)
	old.state.Cookies = []gonote.SessionCookie{{Origin: "https://cas.eclat-bfc.fr/", Cookie: http.Cookie{Name: "fake", Value: "cookie"}}}
	old.homework = func() error { return gonote.ErrSessionExpired }
	a.ctx = context.Background()
	_ = a.prepare()
	_ = a.lockSession()
	a.saved = store.SavedSession{Usable: true, State: old.state}
	_ = a.session.Save(a.saved)
	_ = a.Close()
	a.restore = func(gonote.SessionState, gonote.Options) (schoolClient, error) { return old, nil }
	fresh := fakeSession(cfg)
	logins := 0
	a.Vault = &fakeVault{err: credentials.ErrUnavailable}
	a.loginClient = func(_ context.Context, _ string, c gonote.Credentials, o gonote.Options) (schoolClient, error) {
		logins++
		if len(o.SSOCookies) == 0 || c.Username != "edu-student" || c.Password != "" {
			t.Fatal("renewal did not use IdP cookies")
		}
		return fresh, nil
	}
	if err := execute(a, "devoirs", "--actualiser", "--json"); err != nil {
		t.Fatal(err)
	}
	if logins != 1 || !a.renewed || fresh.state.Order != 2 || a.Vault.(*fakeVault).gets != 0 {
		t.Fatal("renewal path incorrect")
	}
}
func TestRememberedPasswordFailureIsNotRetried(t *testing.T) {
	a, cfg := configuredApp(t)
	cfg.ENT = gonote.ENTEclatBFC
	cfg.ENTUsername = "edu-student"
	cfg.PasswordStorage = credentials.File
	_ = a.Store.SaveConfig(cfg)
	a.ctx = context.Background()
	_ = a.prepare()
	defer a.Close()
	a.Vault = &fakeVault{password: "wrong-password"}
	calls := 0
	a.loginClient = func(_ context.Context, _ string, c gonote.Credentials, _ gonote.Options) (schoolClient, error) {
		calls++
		if c.Username != "edu-student" || c.Password != "wrong-password" {
			t.Fatal("wrong credentials source")
		}
		return nil, gonote.ErrCredentials
	}
	for i := 0; i < 3; i++ {
		err := a.connect()
		if err == nil || !strings.Contains(err.Error(), "mot-de-passe") {
			t.Fatalf("missing update instruction: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("rejected password submitted %d times", calls)
	}
}
func TestInterruptedRequestStateNeverRestores(t *testing.T) {
	a, cfg := configuredApp(t)
	a.ctx = context.Background()
	_ = a.prepare()
	_ = a.lockSession()
	_ = a.session.Save(store.SavedSession{Usable: false, State: fakeSession(cfg).state})
	_ = a.Close()
	a.restore = func(gonote.SessionState, gonote.Options) (schoolClient, error) {
		t.Fatal("restored uncertain counter")
		return nil, nil
	}
	a.loginClient = func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error) {
		return fakeSession(cfg), nil
	}
	t.Setenv("PRONOTE_PASSWORD", "fake-password")
	if err := execute(a, "devoirs", "--actualiser", "--json"); err != nil {
		t.Fatal(err)
	}
}
func TestForgetPasswordIsLocalAndKeepsSession(t *testing.T) {
	a, cfg := configuredApp(t)
	cfg.PasswordStorage = credentials.File
	_ = a.Store.SaveConfig(cfg)
	if err := a.Vault.Set(credentials.File, cfg.CredentialKey(), "fake-secret"); err != nil {
		t.Fatal(err)
	}
	a.loginClient = func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error) {
		t.Fatal("forget contacted server")
		return nil, nil
	}
	sessionPath := filepath.Join(a.Store.ConfigDir, "sessions", cfg.CredentialKey()+".json")
	if err := store.Write(sessionPath, store.SavedSession{Usable: true, State: fakeSession(cfg).state}); err != nil {
		t.Fatal(err)
	}
	if err := execute(a, "oublier-mot-de-passe"); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Store.LoadConfig()
	if got.PasswordStorage != "" {
		t.Fatal("preference not removed")
	}
	if _, err := a.Vault.Get(credentials.File, cfg.CredentialKey()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("secret still present")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatal("existing session removed")
	}
}
func TestGradesRemapPeriodAfterReconnect(t *testing.T) {
	a, cfg := configuredApp(t)
	a.ctx = context.Background()
	_ = a.prepare()
	a.opts.period = cfg.Profile.Periods[0].ID
	f := fakeSession(cfg)
	f.state.Profile.Periods = append([]gonote.Period(nil), cfg.Profile.Periods...)
	f.state.Profile.Periods[0].ID = "fresh-id"
	f.state.Profile.DefaultGradePeriod = "fresh-id"
	t.Setenv("PRONOTE_PASSWORD", "fake-password")
	defer a.Close()
	a.loginClient = func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error) {
		return f, nil
	}
	if _, err := a.getGrades(); err != nil {
		t.Fatal(err)
	}
	if f.gradeID != "fresh-id" {
		t.Fatalf("used previous session period ID: %s", f.gradeID)
	}
}

func TestExpiredSSOCookiesFallBackToSavedPassword(t *testing.T) {
	a, cfg := configuredApp(t)
	cfg.ENT = gonote.ENTEclatBFC
	cfg.ENTUsername = "edu-student"
	cfg.PasswordStorage = credentials.File
	_ = a.Store.SaveConfig(cfg)
	if err := a.Vault.Set(credentials.File, cfg.CredentialKey(), "saved-fake-secret"); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background()
	_ = a.prepare()
	_ = a.lockSession()
	state := fakeSession(cfg).state
	state.Cookies = []gonote.SessionCookie{{Origin: "https://cas.eclat-bfc.fr/", Cookie: http.Cookie{Name: "expired", Value: "fake"}}}
	_ = a.session.Save(store.SavedSession{Usable: false, State: state})
	_ = a.Close()
	calls := 0
	a.loginClient = func(_ context.Context, _ string, c gonote.Credentials, o gonote.Options) (schoolClient, error) {
		calls++
		if calls == 1 {
			if c.Password != "" || len(o.SSOCookies) == 0 {
				t.Fatal("did not try cookies first")
			}
			return nil, gonote.ErrCredentialsRequired
		}
		if c.Username != "edu-student" || c.Password != "saved-fake-secret" {
			t.Fatal("saved password fallback failed")
		}
		return fakeSession(cfg), nil
	}
	if err := execute(a, "devoirs", "--actualiser", "--json"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("unexpected login attempts: %d", calls)
	}
	b, err := os.ReadFile(filepath.Join(a.Store.ConfigDir, "compte.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "saved-fake-secret") {
		t.Fatal("password leaked into account config")
	}
}
func TestSecondExpiredSessionDoesNotLoop(t *testing.T) {
	a, cfg := configuredApp(t)
	t.Setenv("PRONOTE_PASSWORD", "fake-password")
	c := fakeSession(cfg)
	reads := 0
	c.homework = func() error { reads++; return gonote.ErrSessionExpired }
	logins := 0
	a.loginClient = func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error) {
		logins++
		return c, nil
	}
	err := execute(a, "devoirs", "--actualiser", "--json")
	if !errors.Is(err, gonote.ErrSessionExpired) || logins != 2 || reads != 2 {
		t.Fatalf("retry limit failed: %v, logins=%d, reads=%d", err, logins, reads)
	}
}
func TestRejectedTokenUsesOriginalPasswordIdentity(t *testing.T) {
	a, cfg := configuredApp(t)
	cfg.Auth = gonote.Reconnect{Username: "canonical-user", Token: "old-token"}
	cfg.PasswordStorage = credentials.File
	_ = a.Store.SaveConfig(cfg)
	a.Vault = &fakeVault{password: "saved-password"}
	calls := 0
	a.loginClient = func(_ context.Context, _ string, c gonote.Credentials, _ gonote.Options) (schoolClient, error) {
		calls++
		if calls == 1 {
			if c.Username != "canonical-user" || c.Token != "old-token" {
				t.Fatal("wrong token identity")
			}
			return nil, gonote.ErrCredentials
		}
		if c.Username != "student" || c.Password != "saved-password" || c.Token != "" {
			t.Fatal("wrong password identity")
		}
		return fakeSession(cfg), nil
	}
	if err := execute(a, "devoirs", "--actualiser", "--json"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("wrong number of authentication attempts")
	}
}
