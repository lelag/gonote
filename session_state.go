package gonote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// SessionCookie preserves the original scope and expiry of an authentication
// cookie. It is a secret, just like a browser's cookie database.
type SessionCookie struct {
	Origin string      `json:"origin"`
	Cookie http.Cookie `json:"cookie"`
}

type sessionJar struct {
	inner http.CookieJar
	mu    sync.Mutex
	saved map[string]SessionCookie
}

func newSessionJar() (*sessionJar, error) {
	j, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	return &sessionJar{inner: j, saved: map[string]SessionCookie{}}, nil
}
func (j *sessionJar) Cookies(u *url.URL) []*http.Cookie { return j.inner.Cookies(u) }
func (j *sessionJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inner.SetCookies(u, cookies)
	for _, original := range cookies {
		c := *original
		if c.Path == "" || c.Path[0] != '/' {
			c.Path = "/"
			if i := strings.LastIndex(u.Path, "/"); i > 0 {
				c.Path = u.Path[:i]
			}
		}
		domain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if domain == "" {
			domain = strings.ToLower(u.Hostname())
		}
		key := domain + "\x00" + c.Path + "\x00" + c.Name
		if c.MaxAge < 0 || c.MaxAge == 0 && !c.Expires.IsZero() && !c.Expires.After(time.Now()) {
			delete(j.saved, key)
			continue
		}
		if c.MaxAge > 0 {
			c.Expires = time.Now().Add(time.Duration(c.MaxAge) * time.Second)
			c.MaxAge = 0
		}
		c.Raw = ""
		c.RawExpires = ""
		c.Unparsed = nil
		j.saved[key] = SessionCookie{Origin: u.Scheme + "://" + u.Host + "/", Cookie: c}
	}
}
func (j *sessionJar) snapshot() []SessionCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []SessionCookie{}
	for _, c := range j.saved {
		if c.Cookie.Expires.IsZero() || c.Cookie.Expires.After(time.Now()) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		a, b := out[i], out[k]
		return a.Origin+a.Cookie.Path+a.Cookie.Name < b.Origin+b.Cookie.Path+b.Cookie.Name
	})
	return out
}
func (j *sessionJar) restore(cookies []SessionCookie, base *url.URL, ssoOnly bool) error {
	for _, saved := range cookies {
		u, err := url.Parse(saved.Origin)
		if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ErrProtocol
		}
		isSSO := u.Host == eclatHost || u.Host == educonnectHost
		if !sameOrigin(u, base) && !isSSO {
			return ErrProtocol
		}
		if ssoOnly && !isSSO {
			continue
		}
		c := saved.Cookie
		j.SetCookies(u, []*http.Cookie{&c})
	}
	return nil
}

// SessionState contains live authentication secrets and the PRONOTE request
// counter. Store it privately, and hold an exclusive lock across restore, use
// and snapshot. Never use the same state concurrently in separate processes.
type SessionState struct {
	Version      int             `json:"version"`
	URL          string          `json:"url"`
	Timezone     string          `json:"timezone"`
	SessionID    string          `json:"session_id"`
	Space        int             `json:"space"`
	Order        int             `json:"order"`
	Key          []byte          `json:"key"`
	IV           []byte          `json:"iv"`
	Compress     bool            `json:"compress"`
	Encrypt      bool            `json:"encrypt"`
	Mobile       bool            `json:"mobile"`
	Provider     ENTProvider     `json:"provider,omitempty"`
	Fields       []string        `json:"fields"`
	Profile      Profile         `json:"profile"`
	Reconnect    Reconnect       `json:"reconnect"`
	Tabs         []int           `json:"tabs"`
	Endings      []string        `json:"endings"`
	BlocksPerDay int             `json:"blocks_per_day"`
	BlockMinutes float64         `json:"block_minutes"`
	Cookies      []SessionCookie `json:"cookies"`
}

func (c *Client) Snapshot(ctx context.Context) (SessionState, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return SessionState{}, ctx.Err()
	}
	if c.invalid {
		return SessionState{}, ErrSessionExpired
	}
	d := c.dialect
	s := SessionState{
		Version:      1,
		URL:          c.base.String(),
		Timezone:     c.loc.String(),
		SessionID:    c.session,
		Space:        c.space,
		Order:        c.order,
		Key:          append([]byte(nil), c.cipher.key...),
		IV:           append([]byte(nil), c.cipher.iv...),
		Compress:     c.compress,
		Encrypt:      c.encrypt,
		Mobile:       c.mobile,
		Provider:     c.ent,
		Fields:       []string{d.order, d.request, d.secure, d.data, d.signature},
		Profile:      c.Profile(),
		Reconnect:    c.reconnect,
		Endings:      append([]string(nil), c.endings...),
		BlocksPerDay: c.blocksPerDay,
		BlockMinutes: c.blockMinutes,
	}
	for tab := range c.tabs {
		s.Tabs = append(s.Tabs, tab)
	}
	sort.Ints(s.Tabs)
	if j, ok := c.http.Jar.(*sessionJar); ok {
		s.Cookies = j.snapshot()
	}
	return s, nil
}

// RestoreSession resumes the existing session without network access or login.
// Server-side expiration is still reported through ErrSessionExpired.
func RestoreSession(s SessionState, opts Options) (*Client, error) {
	base, err := NormalizeURL(s.URL)
	if err != nil || base != s.URL || s.Version != 1 || (s.SessionID == "" || s.SessionID == "0") || (s.Space != 3 && s.Space != 6) || s.Order < 0 || s.Order%2 != 0 || len(s.Key) == 0 || len(s.IV) != 16 || len(s.Fields) != 5 || s.Profile.Student.ID == "" {
		return nil, ErrProtocol
	}
	if id, err := strconv.ParseUint(s.SessionID, 10, 64); err != nil || id == 0 {
		return nil, ErrProtocol
	}
	if s.Provider != "" && s.Provider != ENTEclatBFC {
		return nil, ErrProtocol
	}
	validFields := false
	for _, v := range [][]int{{2023, 1, 0}, {2024, 3, 9}, {2025, 1, 3}} {
		d := dialectFor(v)
		if strings.Join(s.Fields, "|") == strings.Join([]string{d.order, d.request, d.secure, d.data, d.signature}, "|") {
			validFields = true
		}
	}
	if !validFields {
		return nil, ErrProtocol
	}
	u, _ := url.Parse(base)
	if u.Scheme != "https" && !opts.AllowHTTP {
		return nil, ErrProtocol
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, ErrProtocol
	}
	hc := http.Client{Timeout: 30 * time.Second}
	if opts.HTTPClient != nil {
		hc = *opts.HTTPClient
	}
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !sameOrigin(req.URL, u) {
			return fmt.Errorf("%w : redirection de session", ErrSessionExpired)
		}
		return nil
	}
	j, err := newSessionJar()
	if err != nil {
		return nil, err
	}
	if err = j.restore(s.Cookies, u, false); err != nil {
		return nil, err
	}
	hc.Jar = j
	s.Profile.Periods = append([]Period(nil), s.Profile.Periods...)
	c := &Client{
		allowWrites:  opts.AllowWrites,
		base:         u,
		http:         &hc,
		loc:          loc,
		gate:         make(chan struct{}, 1),
		cipher:       sessionCipher{key: append([]byte(nil), s.Key...), iv: append([]byte(nil), s.IV...)},
		session:      s.SessionID,
		space:        s.Space,
		order:        s.Order,
		compress:     s.Compress,
		encrypt:      s.Encrypt,
		mobile:       s.Mobile,
		ent:          s.Provider,
		dialect:      dialect{s.Fields[0], s.Fields[1], s.Fields[2], s.Fields[3], s.Fields[4]},
		profile:      s.Profile,
		reconnect:    s.Reconnect,
		tabs:         map[int]bool{},
		endings:      append([]string(nil), s.Endings...),
		blocksPerDay: s.BlocksPerDay,
		blockMinutes: s.BlockMinutes,
	}
	for _, tab := range s.Tabs {
		c.tabs[tab] = true
	}
	return c, nil
}
