package gonote

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}
}
func date(s string) any { return map[string]any{"_T": 7, "V": s} }

// This in-memory server validates actual HTTP envelopes and authentication
// challenges. It never opens a socket or contacts a PRONOTE instance.
type fixtureServer struct {
	t                       *testing.T
	version                 []int
	compression, encryption bool
	security                bool
	cipher                  sessionCipher
	order                   int
	calls                   []string
	ent                     bool
	space                   int
	omitToken               bool
	homeworkDone            bool
	statusWrites            int
}

func (s *fixtureServer) roundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		if strings.HasSuffix(r.URL.Path, "infoMobileApp.json") {
			return response(map[string]any{"version": s.version, "nomEtab": "Collège de test", "espaces": []any{map[string]any{"genreEspace": 6}}}), nil
		}
		if strings.HasSuffix(r.URL.Path, "mobile.eleve.html") {
			html := fmt.Sprintf(`<script>try{Start({h:42,a:6,CoA:%t,CrA:%t,sCoA:%t,sCrA:%t,http:false})}catch(e){}</script>`, s.compression, s.encryption, !s.compression, !s.encryption)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(html))}, nil
		}
		if strings.Contains(r.URL.Path, "FichiersExternes") {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/pdf"}}, Body: io.NopCloser(strings.NewReader("test pdf"))}, nil
		}
		s.t.Fatalf("unexpected GET %s", r.URL.Path)
	}
	var envelope map[string]any
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		s.t.Fatal(err)
	}
	space := s.space
	if space == 0 {
		space = 6
	}
	if !strings.Contains(r.URL.Path, fmt.Sprintf("/appelfonction/%d/42/", space)) {
		s.t.Fatal("incorrect RPC space")
	}
	d := dialectFor(s.version)
	name, _ := envelope[d.request].(string)
	s.calls = append(s.calls, name)
	if !allowedRPC(name) {
		s.t.Fatalf("write attempted: %s", name)
	}
	expected := s.cipher.encryptText(strconv.Itoa(s.order + 1))
	if envelope[d.order] != expected || !strings.HasSuffix(r.URL.Path, "/"+expected) {
		s.t.Errorf("request order incorrect for %s", name)
	}
	s.order += 2
	inner := node{envelope[d.secure]}
	if s.encryption || s.compression {
		b, err := hex.DecodeString(inner.text())
		if err != nil {
			s.t.Fatal(err)
		}
		if s.encryption {
			b, err = s.cipher.decrypt(b)
			if err != nil {
				s.t.Fatal(err)
			}
		}
		if s.compression {
			rd := flate.NewReader(bytes.NewReader(b))
			b, err = io.ReadAll(rd)
			_ = rd.Close()
			if err != nil {
				s.t.Fatal(err)
			}
			b, err = hex.DecodeString(string(b))
			if err != nil {
				s.t.Fatal(err)
			}
		}
		inner, err = parseJSON(b)
		if err != nil {
			s.t.Fatal(err)
		}
	}
	data := inner.get(d.data)
	var result any
	var after func()
	switch name {
	case "FonctionParametres":
		iv, err := base64.StdEncoding.DecodeString(data.get("Uuid").text())
		if err != nil {
			s.t.Fatal(err)
		}
		s.cipher.iv = iv
		result = map[string]any{"identifiantNav": "navigator", "General": map[string]any{"PremierLundi": date("31/08/2026"), "PlacesParJour": 20, "PlacesParHeure": 2, "ListePeriodes": []any{map[string]any{"N": "t1", "G": 2, "L": "Trimestre 1", "dateDebut": date("01/09/2026"), "dateFin": date("18/12/2026")}}}}
	case "Identification":
		if data.get("genreEspace").integer() != space {
			s.t.Fatal("incorrect mobile student space")
		}
		username := "Alice"
		if s.ent {
			username = "ENT-GENERATED-USER"
		}
		if data.get("identifiant").text() != username {
			s.t.Error("username missing")
		}
		if data.get("pourENT").boolean() != s.ent {
			s.t.Error("wrong ENT mode")
		}
		if s.ent && data.get("demandeConnexionAppliMobile").boolean() {
			s.t.Error("ENT browser login requested a mobile token")
		}
		temp := s.authenticationCipher()
		challenge := temp.encryptText("aXbYcZ")
		if s.ent {
			challenge = "raw-sso-challenge"
		}
		result = map[string]any{"modeCompLog": true, "modeCompMdp": false, "alea": "seed", "challenge": challenge, "login": "Alice"}
	case "Authentification":
		temp := s.authenticationCipher()
		solved := "abc"
		if s.ent {
			solved = "raw-sso-challenge"
		}
		if data.get("challenge").text() != temp.encryptText(solved) {
			s.t.Error("wrong challenge solution")
		}
		actions := []int{}
		if s.security {
			actions = []int{3}
		}
		result = map[string]any{"cle": temp.encryptText("1,2,3,4,5,6"), "jetonConnexionAppliMobile": "password", "actionsDoubleAuth": actions}
		if s.omitToken {
			delete(result.(map[string]any), "jetonConnexionAppliMobile")
		}
		after = func() { s.cipher.key = []byte{1, 2, 3, 4, 5, 6} }
	case "ParametresUtilisateur":
		result = map[string]any{"ressource": map[string]any{"N": "student", "G": 4, "L": "Alice", "classeDEleve": map[string]any{"L": "4e B"}, "listeOngletsPourPeriodes": []any{map[string]any{"G": 198, "periodeParDefaut": map[string]any{"N": "t1"}}}}, "listeOnglets": []any{map[string]any{"G": 16}, map[string]any{"G": 88}, map[string]any{"G": 89}, map[string]any{"G": 198}}}
	case "PageCahierDeTexte":
		if inner.get(d.signature).get("onglet").integer() == 89 {
			result = map[string]any{"ListeCahierDeTextes": []any{map[string]any{"N": "l1", "Date": date("09/11/2026 08:00:00"), "DateFin": date("09/11/2026 09:00:00"), "Matiere": map[string]any{"N": "maths", "L": "Mathématiques"}, "listeContenus": []any{map[string]any{"L": "Fractions", "descriptif": map[string]any{"_T": 21, "V": "<b>Le cours</b>"}}}}}}
		} else {
			result = map[string]any{"ListeTravauxAFaire": []any{map[string]any{"N": "h1", "PourLe": date("09/11/2026"), "Matiere": map[string]any{"N": "maths", "L": "Mathématiques"}, "descriptif": "Exercices", "TAFFait": s.homeworkDone, "genreRendu": 2, "ListePieceJointe": map[string]any{"_T": 24, "V": []any{map[string]any{"N": "a1", "L": "exercice.pdf", "G": 1}}}}}}
		}
	case "SaisieTAFFaitEleve":
		tasks := data.get("listeTAF").list()
		if len(tasks) != 1 || tasks[0].get("N").text() != "h1" || tasks[0].get("E").integer() != 2 || inner.get(d.signature).get("onglet").integer() != 7 {
			s.t.Fatal("incorrect homework mutation envelope")
		}
		if _, ok := tasks[0].get("TAFFait").v.(bool); !ok {
			s.t.Fatal("missing explicit boolean status")
		}
		s.homeworkDone = tasks[0].get("TAFFait").boolean()
		s.statusWrites++
		result = map[string]any{}
	case "PageEmploiDuTemps":
		result = map[string]any{"ListeCours": []any{map[string]any{"N": "course1", "DateDuCours": date("09/11/2026 08:00:00"), "duree": 2, "place": 0, "ListeContenus": []any{map[string]any{"N": "maths", "L": "Mathématiques", "G": 16}, map[string]any{"L": "B204", "G": 17}}}}}
	case "DernieresNotes":
		p, ok := data.get("Periode").v.(map[string]any)
		if !ok || p["_T"] != nil || data.get("Periode").get("N").text() != "t1" {
			s.t.Fatal("period must be an unwrapped resource reference")
		}
		result = map[string]any{"listeServices": []any{}, "listeDevoirs": []any{map[string]any{"N": "g1", "date": date("09/11/2026"), "service": map[string]any{"N": "maths", "L": "Mathématiques"}, "note": map[string]any{"_T": 10, "V": "16,5"}, "bareme": map[string]any{"_T": 10, "V": "20"}, "coefficient": 2}}, "moyGenerale": 0, "baremeMoyGenerale": 20}
	default:
		s.t.Fatalf("unexpected RPC %s", name)
	}
	payload := map[string]any{d.data: result}
	var secure any = payload
	if s.encryption || s.compression {
		b, _ := json.Marshal(payload)
		if s.compression {
			var buf bytes.Buffer
			w, _ := flate.NewWriter(&buf, 6)
			_, _ = w.Write(b)
			_ = w.Close()
			b = buf.Bytes()
		}
		if s.encryption {
			b = s.cipher.encrypt(b)
		}
		secure = hex.EncodeToString(b)
	}
	res := response(map[string]any{d.secure: secure})
	if after != nil {
		after()
	}
	return res, nil
}
func (s *fixtureServer) authenticationCipher() sessionCipher {
	hash := sha256.Sum256([]byte("seedpassword"))
	key := "alice" + strings.ToUpper(hex.EncodeToString(hash[:]))
	if s.ent {
		hash = sha256.Sum256([]byte("ENT-GENERATED-SECRET"))
		key = strings.ToUpper(hex.EncodeToString(hash[:]))
	}
	return sessionCipher{key: []byte(key), iv: s.cipher.iv}
}
func loginFixture(t *testing.T, version []int, compress, encrypt, security bool) (*Client, *fixtureServer, error) {
	t.Helper()
	s := &fixtureServer{t: t, version: version, compression: compress, encryption: encrypt, security: security}
	hc := &http.Client{Transport: transportFunc(s.roundTrip)}
	c, err := Login(context.Background(), "https://school.example/pronote/eleve.html", Credentials{Username: "Alice", Password: "password", DeviceID: "device"}, Options{HTTPClient: hc})
	return c, s, err
}
func TestLoginAndReadAllModes(t *testing.T) {
	for _, v := range [][]int{{2023, 1, 0}, {2024, 3, 9}, {2025, 1, 3}} {
		for _, compress := range []bool{false, true} {
			for _, encrypt := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v/compress=%t/encrypt=%t", v, compress, encrypt), func(t *testing.T) {
					c, _, err := loginFixture(t, v, compress, encrypt, false)
					if err != nil {
						t.Fatal(err)
					}
					if c.Profile().Student.Name != "Alice" || c.Reconnect().Token != "password" {
						t.Fatal("missing profile/token")
					}
					from := time.Date(2026, 11, 9, 0, 0, 0, 0, c.loc)
					r := DateRange{From: from, To: from}
					hw, err := c.Homework(context.Background(), r)
					if err != nil || len(hw) != 1 || len(hw[0].Attachments) != 1 {
						t.Fatalf("homework: %v %v", hw, err)
					}
					lessons, err := c.Lessons(context.Background(), r)
					if err != nil || len(lessons) != 1 || lessons[0].Contents[0].Description != "<b>Le cours</b>" {
						t.Fatalf("lessons: %v %v", lessons, err)
					}
					tt, err := c.Timetable(context.Background(), r)
					if err != nil || len(tt) != 1 || tt[0].End.Sub(tt[0].Start) != time.Hour {
						t.Fatalf("timetable: %v %v", tt, err)
					}
					gr, err := c.Grades(context.Background(), "")
					if err != nil || len(gr.Grades) != 1 || *gr.Grades[0].Value.Points != 16.5 || *gr.Average.Points != 0 {
						t.Fatalf("grades: %v %v", gr, err)
					}
					var file bytes.Buffer
					if err = c.Download(context.Background(), hw[0].Attachments[0], &file); err != nil || file.String() != "test pdf" {
						t.Fatalf("download: %s %v", file.String(), err)
					}
				})
			}
		}
	}
}
func TestSecurityStopsBeforeAccountLoad(t *testing.T) {
	_, s, err := loginFixture(t, []int{2025, 1, 3}, true, true, true)
	var security *SecurityRequiredError
	if !errors.As(err, &security) {
		t.Fatalf("expected security error: %v", err)
	}
	if len(s.calls) != 3 || s.calls[2] != "Authentification" {
		t.Fatal(s.calls)
	}
}
func TestCryptoKnownVectorAndPadding(t *testing.T) {
	c := sessionCipher{}
	if got := c.encryptText("1"); got != "3fa959b13967e0ef176069e01e23c8d7" {
		t.Fatal(got)
	}
	if _, err := c.decrypt([]byte{1}); err == nil {
		t.Fatal("accepted invalid block")
	}
	bad := c.encrypt([]byte("hello"))
	bad[len(bad)-1] ^= 0xff
	if _, err := c.decrypt(bad); err == nil {
		t.Fatal("accepted invalid padding")
	}
}
func TestRPCAllowlistAndCanceledQueue(t *testing.T) {
	c := &Client{gate: make(chan struct{}, 1)}
	if _, err := c.call(context.Background(), "SaisieTAFFaitEleve", nil, nil, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	c.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.call(ctx, "DernieresNotes", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestConcurrentReadsAllocateOrderInsideQueue(t *testing.T) {
	c, _, err := loginFixture(t, []int{2025, 1, 3}, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var active, maxActive atomic.Int32
	base := c.http.Transport
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		time.Sleep(time.Millisecond)
		return base.RoundTrip(r)
	})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Grades(context.Background(), ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("overlapping requests: %d", maxActive.Load())
	}
}
func TestGradeStatusesAndCalendarWeeks(t *testing.T) {
	for _, tt := range []struct {
		input   string
		status  int
		numeric bool
	}{{"16,5", 0, true}, {"0", 0, true}, {"|1", 1, false}, {"|4", 4, false}, {"|6", 6, false}, {"", -1, false}, {"invalid", -1, false}} {
		v := gradeValue(node{tt.input})
		if v.Status != tt.status || (v.Points != nil) != tt.numeric {
			t.Errorf("%q: %+v", tt.input, v)
		}
	}
	loc, _ := time.LoadLocation("Europe/Paris")
	c := &Client{loc: loc, profile: Profile{FirstMonday: time.Date(2026, 10, 19, 0, 0, 0, 0, loc)}}
	if got := c.WeekNumber(time.Date(2026, 10, 26, 0, 0, 0, 0, loc)); got != 2 {
		t.Fatalf("DST week = %d", got)
	}
}
func TestNormalizeURLPreservesCaseAndRejectsCredentials(t *testing.T) {
	u, err := NormalizeURL("School.example/MyPRONOTE/eleve.html?login=true#x")
	if err != nil || u != "https://School.example/MyPRONOTE" {
		t.Fatalf("%s %v", u, err)
	}
	if _, err := NormalizeURL("https://user:password@school.example"); err == nil {
		t.Fatal("accepted URL credentials")
	}
}

func TestCapturedResponseShapes(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Paris")
	read := func(file string) node {
		t.Helper()
		b, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		n, err := parseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	hw, err := decodeAssignments(read("homework.json"), loc)
	if err != nil || len(hw) != 2 || hw[0].Subject.Name != "MATHÉMATIQUES" || hw[0].Submission != 2 || hw[0].Due.Format("2006-01-02") != "2025-09-08" {
		t.Fatalf("homework fixture: %+v %v", hw, err)
	}
	lessons, err := decodeLessons(read("lessons.json"), loc)
	if err != nil || len(lessons) != 2 || len(lessons[0].Contents) == 0 || len(lessons[0].Teachers) == 0 {
		t.Fatalf("lessons fixture: %+v %v", lessons, err)
	}
	c := &Client{loc: loc, blockMinutes: 30}
	entries, err := c.decodeTimetable(read("timetable.json"))
	if err != nil || len(entries) != 3 || entries[0].Subject.Name == "" {
		t.Fatalf("timetable fixture: %+v %v", entries, err)
	}
}

func TestBootstrapLiteralAndCanceledReplacement(t *testing.T) {
	n, err := bootstrap([]byte(`<script>try { Start ({h:42,a:6,CrA:false,CoA:false,note:'a } quoted value'}) }catch(e){}</script>`))
	if err != nil || n.get("h").integer() != 42 || n.get("CrA").boolean() {
		t.Fatalf("bootstrap: %+v %v", n, err)
	}
	start := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	entries := []TimetableEntry{{ID: "old", Start: start, End: start.Add(time.Hour), Canceled: true}, {ID: "new", Start: start, End: start.Add(time.Hour)}, {ID: "other", Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour), Canceled: true}}
	got := FilterCanceledReplacements(entries)
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "other" {
		t.Fatal(got)
	}
}

func TestExpiredSessionIsNeverReused(t *testing.T) {
	c, _, err := loginFixture(t, []int{2025, 1, 3}, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(map[string]any{"Erreur": map[string]any{"Titre": "La page a expiré"}}), nil
	})
	for i := 0; i < 2; i++ {
		if _, err = c.Grades(context.Background(), ""); !errors.Is(err, ErrSessionExpired) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("expired session sent %d requests", calls)
	}
}

func TestTokenReconnectUsesMobileCookie(t *testing.T) {
	s := &fixtureServer{t: t, version: []int{2025, 1, 3}, encryption: true, compression: true}
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		cookie, err := r.Cookie("appliMobile")
		if !strings.HasSuffix(r.URL.Path, "infoMobileApp.json") && (err != nil || cookie.Value != "1") {
			t.Error("mobile reconnect cookie absent")
		}
		return s.roundTrip(r)
	})}
	if _, err := Login(context.Background(), "https://school.example/pronote", Credentials{Username: "Alice", Token: "password", DeviceID: "device"}, Options{HTTPClient: hc}); err != nil {
		t.Fatal(err)
	}
}
