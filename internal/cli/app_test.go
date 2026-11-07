package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/demo"
	"github.com/lelag/gonote/internal/store"
)

func appForTest(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	s := store.Store{ConfigDir: filepath.Join(t.TempDir(), "config"), CacheDir: filepath.Join(t.TempDir(), "cache")}
	a := New(strings.NewReader(""), &out, &out, s)
	loc, _ := time.LoadLocation("Europe/Paris")
	a.Now = func() time.Time { return time.Date(2026, 11, 7, 9, 12, 0, 0, loc) }
	return a, &out
}
func execute(a *App, args ...string) error {
	defer a.Close()
	cmd := a.Command()
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}
func TestFrenchCommandsAndEnglishAliases(t *testing.T) {
	for _, command := range []string{"devoirs", "homework", "cours", "lessons", "notes", "grades", "emploi-du-temps", "timetable"} {
		t.Run(command, func(t *testing.T) {
			a, out := appForTest(t)
			if err := execute(a, "--demo", "--json", command); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if value["demo"] != true || value["data"] == nil {
				t.Fatal(out.String())
			}
			if _, err := a.Store.LoadConfig(); err == nil {
				t.Fatal("demo saved credentials")
			}
		})
	}
}
func TestViewsStayWithinTerminalWidths(t *testing.T) {
	for _, width := range []int{24, 32, 48, 80, 100, 120} {
		for _, command := range [][]string{{}, {"devoirs"}, {"cours"}, {"notes"}, {"emploi-du-temps"}} {
			t.Run(fmt.Sprintf("%d/%v", width, command), func(t *testing.T) {
				a, out := appForTest(t)
				args := []string{"--demo", "--no-color", "--largeur", fmt.Sprint(width)}
				args = append(args, command...)
				if err := execute(a, args...); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(out.String(), "\x1b") {
					t.Fatal("ANSI in plain output")
				}
				for _, line := range strings.Split(out.String(), "\n") {
					if w := ansi.StringWidth(line); w > width {
						t.Errorf("%d columns exceeds %d: %q", w, width, line)
					}
				}
			})
		}
	}
}
func TestStableDetailsAndFullHomeworkText(t *testing.T) {
	a, out := appForTest(t)
	if err := execute(a, "--demo", "devoirs"); err != nil {
		t.Fatal(err)
	}
	ref := store.Reference("h", "h-anglais")
	if !strings.Contains(out.String(), ref) {
		t.Fatal("missing reference")
	}
	b, result := appForTest(t)
	b.Store = a.Store // a.Store already points at the demo directory.
	b.opts.demo = true
	b.loaded = true
	b.profile = demo.New(b.Now()).Profile
	if err := execute(b, "--demo", "voir", ref); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.String(), "Écrire cinq phrases.") || !strings.Contains(result.String(), "My favourite animal.pdf") {
		t.Fatal(result.String())
	}
}
func TestOfflineReadsSavedDataWithoutLogin(t *testing.T) {
	a, out := appForTest(t)
	profile := demo.New(a.Now()).Profile
	cfg := store.Config{URL: "https://school.example/pronote", Auth: gonote.Reconnect{Username: "Alice"}, Profile: profile}
	if err := a.Store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r := gonote.DateRange{From: midnight(a.Now()).AddDate(0, 0, -14), To: midnight(a.Now()).AddDate(0, 0, 30)}
	b, _ := json.Marshal(demo.New(a.Now()).Homework)
	if err := a.Store.Account(cfg).Save(rangeKey("homework", r), store.Envelope{Fetched: a.Now().Add(-48 * time.Hour), Profile: profile, Data: b}); err != nil {
		t.Fatal(err)
	}
	if err := execute(a, "devoirs", "--hors-ligne", "--json"); err != nil {
		t.Fatal(err)
	}
	if a.client != nil {
		t.Fatal("offline mode connected")
	}
	if !strings.Contains(out.String(), `"cached": true`) {
		t.Fatal(out.String())
	}
}
func TestDateSelectionAndConflicts(t *testing.T) {
	a, _ := appForTest(t)
	a.opts.tomorrow = true
	r, err := a.selection("day")
	if err != nil || r.From.Weekday() != time.Sunday {
		t.Fatalf("tomorrow must be calendar Sunday: %v %v", r, err)
	}
	a.opts.on = "2026-11-09"
	if _, err = a.selection("day"); err == nil {
		t.Fatal("accepted ambiguous dates")
	}
	b, _ := appForTest(t)
	if err := execute(b, "--demo", "--hors-ligne", "--actualiser"); err == nil {
		t.Fatal("accepted conflicting cache flags")
	}
}
func TestTeacherHTMLCannotControlTerminal(t *testing.T) {
	got := plain("<p>Une <b>fraction</b> &amp; un calcul.</p><script>secret()</script><ul><li>Étape 1</li></ul>\x1b]52;c;bad\x07\x1b[31mRouge\x1b[0m\u202e")
	if strings.ContainsAny(got, "\x1b\x07\u202e") || strings.Contains(got, "secret") || strings.Contains(got, "<b>") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "fraction & un calcul.") || !strings.Contains(got, "• Étape 1") {
		t.Fatal(got)
	}
	link := plain(`<a href="https://school.example/exercise">Exercice</a><img alt="Schéma" src="image.png">`)
	if !strings.Contains(link, "https://school.example/exercise") || !strings.Contains(link, "Schéma") {
		t.Fatal(link)
	}
}

func TestWeeklyGridBottomBordersAlign(t *testing.T) {
	a, out := appForTest(t)
	if err := execute(a, "--demo", "edt", "--largeur", "110", "--no-color"); err != nil {
		t.Fatal(err)
	}
	bottoms := 0
	for _, line := range strings.Split(out.String(), "\n") {
		n := strings.Count(line, "╰")
		if n > 0 {
			if n != 5 {
				t.Errorf("unaligned weekly grid: %q", line)
			}
			bottoms += n
		}
	}
	if bottoms != 5 {
		t.Fatalf("expected five grid columns, got %d", bottoms)
	}
}

func TestOfflineDayCanReuseSavedInterval(t *testing.T) {
	a, out := appForTest(t)
	profile := demo.New(a.Now()).Profile
	cfg := store.Config{URL: "https://school.example/pronote", Profile: profile}
	cfg.Auth.Username = "Alice"
	if err := a.Store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r := gonote.DateRange{From: midnight(a.Now()).AddDate(0, 0, -14), To: midnight(a.Now()).AddDate(0, 0, 30)}
	b, _ := json.Marshal(demo.New(a.Now()).Homework)
	if err := a.Store.Account(cfg).Save(rangeKey("homework", r), store.Envelope{Fetched: a.Now().Add(-24 * time.Hour), Profile: profile, Data: b}); err != nil {
		t.Fatal(err)
	}
	if err := execute(a, "devoirs", "--le", "2026-11-09", "--hors-ligne", "--json"); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Data []gonote.Assignment `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Data) != 2 {
		t.Fatalf("day filter returned %d assignments", len(parsed.Data))
	}
	for _, x := range parsed.Data {
		if x.Due.Format("2006-01-02") != "2026-11-09" {
			t.Fatal("cache returned unfiltered data")
		}
	}
}

func TestENTConnectionFlagAndReconnectIdentity(t *testing.T) {
	a, out := appForTest(t)
	if err := execute(a, "connexion", "--help"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--ent") || !strings.Contains(out.String(), "eclat-bfc") {
		t.Fatal("ENT provider missing from help")
	}
	a, _ = appForTest(t)
	if err := execute(a, "connexion", "--ent", "unknown"); err == nil || !strings.Contains(err.Error(), "ENT inconnu") {
		t.Fatalf("unknown ENT accepted: %v", err)
	}
	a.cfg = store.Config{ENT: gonote.ENTEclatBFC, ENTUsername: "edu-student", Auth: gonote.Reconnect{Username: "pronote-user", DeviceID: "device"}}
	if cred := a.credentials(); cred.Username != "edu-student" || cred.Token != "" {
		t.Fatal("password login did not select EduConnect identity")
	}
	a.cfg.Auth.Token = "mobile-token"
	if cred := a.credentials(); cred.Username != "pronote-user" || cred.Token != "mobile-token" {
		t.Fatal("token reconnect did not select PRONOTE identity")
	}
}

func TestVersionUsesPublicCommandNameAndInjectedBuildVersion(t *testing.T) {
	a, out := appForTest(t)
	a.Version = "v0.1.0-test"
	if err := execute(a, "--version"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "gonote v0.1.0-test\n" {
		t.Fatal("unexpected version output", out.String())
	}
}
