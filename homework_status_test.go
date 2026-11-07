package gonote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestHomeworkStatusWritesAndReopenAcrossProtocols(t *testing.T) {
	for _, v := range [][]int{{2023, 1, 0}, {2024, 3, 9}, {2026, 2, 8}} {
		for _, encrypt := range []bool{false, true} {
			for _, compress := range []bool{false, true} {
				t.Run(fmt.Sprint(v, encrypt, compress), func(t *testing.T) {
					s := &fixtureServer{t: t, version: v, compression: compress, encryption: encrypt}
					opts := Options{AllowWrites: true, HTTPClient: &http.Client{Transport: transportFunc(s.roundTrip)}}
					c, err := Login(context.Background(), "https://school.example/pronote", Credentials{Username: "Alice", Password: "password"}, opts)
					if err != nil {
						t.Fatal(err)
					}
					r := DateRange{From: time.Date(2026, 11, 9, 0, 0, 0, 0, c.loc), To: time.Date(2026, 11, 9, 0, 0, 0, 0, c.loc)}
					for _, done := range []bool{true, true, false} {
						if err = c.SetHomeworkDone(context.Background(), "h1", done); err != nil {
							t.Fatal(err)
						}
						items, err := c.Homework(context.Background(), r)
						if err != nil || len(items) != 1 || items[0].Done != done {
							t.Fatal("status readback failed", err)
						}
					}
					if s.statusWrites != 3 {
						t.Fatal("incorrect number of mutation requests")
					}
					state, err := c.Snapshot(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					readOnly, err := RestoreSession(state, Options{HTTPClient: opts.HTTPClient})
					if err != nil {
						t.Fatal(err)
					}
					if err = readOnly.SetHomeworkDone(context.Background(), "h1", true); !errors.Is(err, ErrReadOnly) {
						t.Fatal("write permission leaked through persisted state")
					}
					resumed, err := RestoreSession(state, opts)
					if err != nil {
						t.Fatal(err)
					}
					if err = resumed.SetHomeworkDone(context.Background(), "h1", true); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
func TestHomeworkMutationValidatesBeforeNetworkAndRestrictsOtherWrites(t *testing.T) {
	c, s, err := loginFixture(t, []int{2026, 2, 8}, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	before := len(s.calls)
	if err = c.SetHomeworkDone(context.Background(), "h1", true); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	c.allowWrites = true
	if err = c.SetHomeworkDone(context.Background(), " ", true); err == nil {
		t.Fatal("blank ID accepted")
	}
	if _, err = c.call(context.Background(), "SaisieTAFARendreEleve", nil, nil, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatal("unsupported write allowed")
	}
	delete(c.tabs, 88)
	if err = c.SetHomeworkDone(context.Background(), "h1", true); !errors.Is(err, ErrAccessDenied) {
		t.Fatal(err)
	}
	if len(s.calls) != before {
		t.Fatal("invalid mutation reached server")
	}
}
