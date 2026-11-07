package gonote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestResumeEncryptedSessionWithoutAuthentication(t *testing.T) {
	for _, v := range [][]int{{2023, 1, 0}, {2024, 3, 9}, {2026, 2, 8}} {
		for _, encrypt := range []bool{false, true} {
			for _, compress := range []bool{false, true} {
				t.Run(fmt.Sprint(v, encrypt, compress), func(t *testing.T) {
					c, server, err := loginFixture(t, v, compress, encrypt, false)
					if err != nil {
						t.Fatal(err)
					}
					initial := len(server.calls)
					for i := 0; i < 3; i++ {
						state, err := c.Snapshot(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						// Exercise the on-disk JSON representation, including crypto keys.
						b, _ := json.Marshal(state)
						var restored SessionState
						if err = json.Unmarshal(b, &restored); err != nil {
							t.Fatal(err)
						}
						c, err = RestoreSession(restored, Options{HTTPClient: &http.Client{Transport: transportFunc(server.roundTrip)}})
						if err != nil {
							t.Fatal(err)
						}
						r := DateRange{From: time.Date(2026, 11, 9, 0, 0, 0, 0, c.loc), To: time.Date(2026, 11, 9, 0, 0, 0, 0, c.loc)}
						if _, err = c.Homework(context.Background(), r); err != nil {
							t.Fatal(err)
						}
						if _, err = c.Grades(context.Background(), c.Profile().DefaultGradePeriod); err != nil {
							t.Fatal(err)
						}
					}
					for _, name := range server.calls[initial:] {
						if name != "PageCahierDeTexte" && name != "DernieresNotes" {
							t.Fatalf("resuming authenticated again: %s", name)
						}
					}
				})
			}
		}
	}
}
func TestSessionCookiesKeepScopeAndAbsoluteExpiry(t *testing.T) {
	j, _ := newSessionJar()
	edu, _ := url.Parse("https://" + educonnectHost + "/idp/profile/SSO")
	j.SetCookies(edu, []*http.Cookie{{Name: "session", Value: "secret", Path: "/idp", Secure: true, HttpOnly: true, MaxAge: 3600}})
	saved := j.snapshot()
	if len(saved) != 1 || saved[0].Cookie.MaxAge != 0 || saved[0].Cookie.Expires.IsZero() {
		t.Fatal("lost absolute expiry")
	}
	school, _ := url.Parse("https://school.example/pronote")
	j2, _ := newSessionJar()
	if err := j2.restore(saved, school, false); err != nil {
		t.Fatal(err)
	}
	if len(j2.Cookies(edu)) != 1 || len(j2.Cookies(school)) != 0 {
		t.Fatal("cookie scope changed")
	}
	if !j2.snapshot()[0].Cookie.Expires.Equal(saved[0].Cookie.Expires) {
		t.Fatal("resuming extended cookie lifetime")
	}
	saved[0].Cookie.Expires = time.Now().Add(-time.Second)
	j3, _ := newSessionJar()
	_ = j3.restore(saved, school, false)
	if len(j3.Cookies(edu)) != 0 {
		t.Fatal("expired cookie restored")
	}
	evil, _ := url.Parse("https://other.example/")
	saved[0].Origin = evil.String()
	if !errors.Is(j3.restore(saved, school, false), ErrProtocol) {
		t.Fatal("foreign origin accepted")
	}
}
func TestExpiredResponseAndMalformedSession(t *testing.T) {
	c, _, err := loginFixture(t, []int{2026, 2, 8}, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := c.Snapshot(context.Background())
	for _, mutate := range []func(*SessionState){func(s *SessionState) { s.Order++ }, func(s *SessionState) { s.Fields[0] = "foreign" }, func(s *SessionState) { s.IV = nil }, func(s *SessionState) { s.SessionID = "evil/path" }, func(s *SessionState) { s.SessionID = "18446744073709551616" }} {
		b, _ := json.Marshal(state)
		var s SessionState
		_ = json.Unmarshal(b, &s)
		mutate(&s)
		if _, err := RestoreSession(s, Options{}); !errors.Is(err, ErrProtocol) {
			t.Fatalf("invalid state accepted: %v", err)
		}
	}
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(map[string]any{"Erreur": map[string]any{"G": 10, "Titre": "La page a expiré"}}), nil
	})
	if _, err = c.Grades(context.Background(), "t1"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expiry unrecognized: %v", err)
	}
	if _, err = c.Snapshot(context.Background()); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("invalid session saved")
	}
}
