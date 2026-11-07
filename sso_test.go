package gonote

import (
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func htmlResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func redirectResponse(location string) *http.Response {
	r := htmlResponse("")
	r.StatusCode = 302
	r.Header.Set("Location", location)
	return r
}
func setCookie(r *http.Response, name, value, cookiePath string) {
	r.Header.Add("Set-Cookie", (&http.Cookie{Name: name, Value: value, Path: cookiePath, Secure: true, HttpOnly: true}).String())
}
func samlForm(action, field, value string) string {
	return fmt.Sprintf(`<form method="post" action="%s"><input type="hidden" name="%s" value="%s"><input type="hidden" name="RelayState" value="relay+&amp;state"></form>`, action, field, stdhtml.EscapeString(value))
}

const eduLoginForm = `<form method="post" action="?execution=login"><input type="hidden" name="csrf_token" value="fresh+&amp;csrf"><input name="j_username"><input type="password" name="j_password"><button name="_eventId_proceed">Connexion</button></form>`

type ssoFixture struct {
	t                          *testing.T
	rpc                        *fixtureServer
	credentialPosts, samlPosts int
	failedCredentials          bool
	unsupportedCAS             bool
	skipSelector               bool
	loginAction                string
	credentialRedirect         string
	visited                    []string
}

func (s *ssoFixture) transport(r *http.Request) (*http.Response, error) {
	s.visited = append(s.visited, r.URL.Host+r.URL.Path)
	if strings.Contains(r.URL.Path, "appelfonction") {
		return s.rpc.roundTrip(r)
	}
	if err := r.ParseForm(); err != nil {
		s.t.Fatal(err)
	}
	switch r.URL.Host {
	case "school.example":
		if strings.HasSuffix(r.URL.Path, "infoMobileApp.json") {
			casURL := "https://" + eclatHost
			if s.unsupportedCAS {
				casURL = "https://other.example"
			}
			return response(map[string]any{"version": []int{2026, 2, 8}, "nomEtab": "Test collège", "CAS": map[string]any{"actif": true, "casURL": casURL}, "espaces": []any{map[string]any{"genreEspace": 6}}}), nil
		}
		if r.URL.Query().Has("ticket") {
			if _, err := r.Cookie("EduSID"); err == nil {
				s.t.Error("EduConnect cookie leaked to school")
			}
			res := redirectResponse("/pronote/eleve.html?identifiant=FAKE-HANDOFF")
			setCookie(res, "PronoteSID", "pronote-cookie", "/pronote")
			return res, nil
		}
		if r.URL.Query().Has("identifiant") {
			if r.URL.Query().Get("fd") != "1" {
				return htmlResponse(`<html>Votre navigateur n’est pas compatible.</html>`), nil
			}
			cookie, err := r.Cookie("PronoteSID")
			if err != nil || cookie.Value != "pronote-cookie" {
				s.t.Error("PRONOTE cookie missing")
			}
			return htmlResponse(fmt.Sprintf(`<script>Start({h:42,a:%d,e:'ENT-GENERATED-USER',f:'ENT-GENERATED-SECRET',CoA:%t,CrA:%t,http:false})</script>`, s.rpc.space, s.rpc.compression, s.rpc.encryption)), nil
		}
		if r.URL.Query().Has("login") {
			s.t.Fatal("ENT flow tried to bypass SSO")
		}
		return redirectResponse("https://" + eclatHost + "/login?service=" + url.QueryEscape("https://school.example/pronote/mobile.eleve.html")), nil
	case eclatHost:
		if _, err := r.Cookie("EduSID"); err == nil {
			s.t.Error("EduConnect cookie leaked to CAS")
		}
		if r.URL.Path == "/login" {
			if r.URL.Query().Get("selection") != "EDU" && !s.skipSelector {
				res := htmlResponse(`<form method="get" action="/login"><input type="radio" name="selection" value="EDU"></form>`)
				setCookie(res, "CASSID", "cas-cookie", "/")
				return res, nil
			}
			if cookie, err := r.Cookie("CASSID"); !s.skipSelector && (err != nil || cookie.Value != "cas-cookie") {
				s.t.Error("CAS session lost")
			}
			if r.URL.Query().Get("service") != "https://school.example/pronote/mobile.eleve.html" {
				s.t.Error("CAS service was not preserved")
			}
			res := redirectResponse("/delegate/redirect/EDU?service=" + url.QueryEscape(r.URL.Query().Get("service")))
			if s.skipSelector {
				setCookie(res, "CASSID", "cas-cookie", "/")
			}
			return res, nil
		}
		if r.URL.Path == "/delegate/redirect/EDU" {
			return redirectResponse("/clientredirect?client_name=EDU&service=" + url.QueryEscape(r.URL.Query().Get("service"))), nil
		}
		if r.URL.Path == "/clientredirect" {
			if r.URL.Query().Get("client_name") != "EDU" {
				s.t.Fatal("wrong delegated identity provider")
			}
			markup := samlForm("https&#x3a;&#x2f;&#x2f;"+educonnectHost+"&#x2f;idp&#x2f;profile&#x2f;SAML2&#x2f;POST&#x2f;SSO", "SAMLRequest", "request+&value")
			return htmlResponse(markup), nil
		}
		if r.URL.Path == "/saml/SAMLAssertionConsumer" {
			s.samlPosts++
			if r.Method != "POST" || r.PostForm.Get("SAMLResponse") != "response+&value" || r.PostForm.Get("RelayState") != "relay+&state" {
				s.t.Error("SAML response corrupted")
			}
			if r.PostForm.Has("j_password") {
				s.t.Error("password leaked to CAS")
			}
			return redirectResponse("https://school.example/pronote/eleve.html?ticket=FAKE-TICKET"), nil
		}
	case educonnectHost:
		if r.PostForm.Has("SAMLRequest") {
			if r.PostForm.Get("SAMLRequest") != "request+&value" {
				s.t.Error("SAML request corrupted")
			}
			res := redirectResponse("https://" + educonnectHost + "/idp/profile/SAML2/Redirect/SSO?execution=start")
			setCookie(res, "EduSID", "edu-cookie", "/idp")
			return res, nil
		}
		if r.Method == "GET" {
			form := eduLoginForm
			if s.loginAction != "" {
				form = strings.Replace(form, "?execution=login", s.loginAction, 1)
			}
			return htmlResponse(form), nil
		}
		if r.PostForm.Has("j_password") {
			s.credentialPosts++
			if r.PostForm.Get("j_username") != "student" || r.PostForm.Get("j_password") != "edu-password" || r.PostForm.Get("csrf_token") != "fresh+&csrf" {
				s.t.Error("credentials or fresh CSRF missing")
			}
			if r.Header.Get("Origin") != "https://"+educonnectHost {
				s.t.Error("login Origin missing")
			}
			if s.failedCredentials {
				return htmlResponse(eduLoginForm), nil
			}
			if s.credentialRedirect != "" {
				res := redirectResponse(s.credentialRedirect)
				res.StatusCode = 307
				return res, nil
			}
			return htmlResponse(`<form method="post" action="?execution=storage"><input type="hidden" name="csrf_token" value="storage-csrf"><input type="hidden" name="shib_idp_ls_supported" value=""><input type="hidden" name="shib_idp_ls_success.shib_idp_session_ss" value="false"><button name="_eventId_proceed">Continue</button></form>`), nil
		}
		if r.URL.Query().Get("execution") == "storage" {
			if r.PostForm.Get("shib_idp_ls_supported") != "false" {
				s.t.Error("native client pretended to support localStorage")
			}
			return htmlResponse(samlForm("https://"+eclatHost+"/saml/SAMLAssertionConsumer", "SAMLResponse", "response+&value")), nil
		}
	}
	s.t.Fatalf("unexpected authentication request: %s %s %s", r.Method, r.URL.Host, r.URL.Path)
	return nil, ErrSSOFlow
}
func newSSOFixture(t *testing.T, space int, compression, encryption bool) *ssoFixture {
	return &ssoFixture{t: t, rpc: &fixtureServer{t: t, version: []int{2026, 2, 8}, ent: true, space: space, compression: compression, encryption: encryption}}
}
func (s *ssoFixture) login() (*Client, error) {
	return Login(context.Background(), "https://school.example/pronote", Credentials{Username: "student", Password: "edu-password", DeviceID: "device"}, Options{ENT: ENTEclatBFC, HTTPClient: &http.Client{Transport: transportFunc(s.transport)}})
}

func TestEclatCompleteSSOAndPronoteHandshake(t *testing.T) {
	for _, space := range []int{3, 6} {
		for _, encrypted := range []bool{false, true} {
			for _, compressed := range []bool{false, true} {
				t.Run(fmt.Sprintf("space=%d/encrypted=%t/compressed=%t", space, encrypted, compressed), func(t *testing.T) {
					s := newSSOFixture(t, space, compressed, encrypted)
					c, err := s.login()
					if err != nil {
						t.Fatal(err)
					}
					if c.Profile().Student.Name != "Alice" || c.space != space || s.credentialPosts != 1 || s.samlPosts != 1 {
						t.Fatalf("incomplete handoff: space=%d credentials=%d saml=%d", c.space, s.credentialPosts, s.samlPosts)
					}
					if c.Reconnect().Username != "Alice" || c.Reconnect().Token != "password" {
						t.Fatal("mobile reconnect data lost")
					}
					if _, err = c.Grades(context.Background(), ""); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
func TestEclatInvalidCredentialsAreNotRetried(t *testing.T) {
	s := newSSOFixture(t, 6, false, false)
	s.failedCredentials = true
	_, err := s.login()
	if !errors.Is(err, ErrCredentials) || s.credentialPosts != 1 || s.samlPosts != 0 {
		t.Fatalf("error=%v credential posts=%d", err, s.credentialPosts)
	}
	if strings.Contains(err.Error(), "edu-password") || strings.Contains(err.Error(), "csrf") {
		t.Fatal("secret exposed in error")
	}
}
func TestEclatDoesNotSubmitCredentialsToOtherOrigin(t *testing.T) {
	for _, action := range []string{"https://evil.example/login", "https://cas.eclat-bfc.fr/login", "http://educonnect.education.gouv.fr/idp/profile/SAML2/POST/SSO"} {
		t.Run(action, func(t *testing.T) {
			s := newSSOFixture(t, 6, false, false)
			s.loginAction = action
			_, err := s.login()
			if !errors.Is(err, ErrSSOInteraction) || s.credentialPosts != 0 {
				t.Fatalf("%v; posts=%d", err, s.credentialPosts)
			}
		})
	}
}
func TestEclatRequiresAnnouncedProviderAndHonorsSecurityActions(t *testing.T) {
	s := newSSOFixture(t, 6, false, false)
	s.unsupportedCAS = true
	if _, err := s.login(); err == nil || len(s.visited) != 1 {
		t.Fatal("accepted unrelated CAS")
	}
	s = newSSOFixture(t, 6, true, true)
	s.rpc.security = true
	_, err := s.login()
	var security *SecurityRequiredError
	if !errors.As(err, &security) || len(s.rpc.calls) != 3 {
		t.Fatalf("%v %v", err, s.rpc.calls)
	}
}
func TestENTTokenReconnectSkipsSSO(t *testing.T) {
	s := &fixtureServer{t: t, version: []int{2026, 2, 8}, compression: true, encryption: true}
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "school.example" {
			t.Fatal("token contacted ENT")
		}
		return s.roundTrip(r)
	})}
	if _, err := Login(context.Background(), "https://school.example/pronote", Credentials{Username: "Alice", Token: "password", DeviceID: "device"}, Options{ENT: ENTEclatBFC, HTTPClient: hc}); err != nil {
		t.Fatal(err)
	}
}
func TestEclatDoesNotForwardCredentialPostOnCrossOrigin307(t *testing.T) {
	s := newSSOFixture(t, 6, false, false)
	s.credentialRedirect = "https://" + eclatHost + "/login"
	_, err := s.login()
	if !errors.Is(err, ErrSSOFlow) || s.credentialPosts != 1 || s.samlPosts != 0 {
		t.Fatalf("error=%v posts=%d", err, s.credentialPosts)
	}
}

func TestEclatWorksWithoutIssuingAMobileToken(t *testing.T) {
	s := newSSOFixture(t, 3, true, true)
	s.rpc.omitToken = true
	c, err := s.login()
	if err != nil {
		t.Fatal(err)
	}
	if c.Reconnect().Token != "" || c.Profile().Student.Name != "Alice" {
		t.Fatal("tokenless browser session lost")
	}
}

func TestEclatDoesNotLoopOnSAMLPageWithoutSelectionParameter(t *testing.T) {
	s := newSSOFixture(t, 6, false, false)
	s.skipSelector = true
	if _, err := s.login(); err != nil {
		t.Fatal(err)
	}
	if s.credentialPosts != 1 || s.samlPosts != 1 {
		t.Fatal("canonical CAS handoff did not complete")
	}
}

func TestEclatDelegationRoutesRemainRestricted(t *testing.T) {
	base, _ := url.Parse("https://school.example/pronote")
	flow := eclatFlow{base: base}
	for _, route := range []string{"https://cas.eclat-bfc.fr/delegate/redirect/OTHER", "https://cas.eclat-bfc.fr/unrelated", "http://cas.eclat-bfc.fr/delegate/redirect/EDU", "https://evil.example/clientredirect"} {
		u, _ := url.Parse(route)
		if flow.allowed(u) {
			t.Errorf("unexpected authentication route allowed: %s", route)
		}
	}
}

func TestSSOStopsAtConsentAndCaptcha(t *testing.T) {
	u, _ := url.Parse("https://" + educonnectHost + "/idp/profile/SAML2/POST/SSO?execution=test")
	base, _ := url.Parse("https://school.example/pronote")
	for _, markup := range []string{
		`<form method="post"><input type="hidden" name="csrf_token" value="fake"><input type="checkbox" name="accept"><button name="_eventId_proceed">Accept</button></form>`,
		strings.Replace(eduLoginForm, "</form>", `<input name="captcha"></form>`, 1),
		`<form method="post"><input type="hidden" name="_eventId_accept" value="true"></form>`,
	} {
		forms, err := parseSSOForms([]byte(markup))
		if err != nil {
			t.Fatal(err)
		}
		flow := eclatFlow{base: base, username: "student", password: "secret"}
		if _, _, _, err = flow.chooseForm(u, forms); !errors.Is(err, ErrSSOInteraction) || flow.submitted {
			t.Fatalf("accepted interactive step: %v", err)
		}
	}
}

func TestEclatCookieOnlyRenewalAndExpiredCookies(t *testing.T) {
	s := newSSOFixture(t, 3, true, true)
	c, err := s.login()
	if err != nil {
		t.Fatal(err)
	}
	state, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			next := newSSOFixture(t, 3, true, true)
			next.rpc.omitToken = true
			transport := transportFunc(func(r *http.Request) (*http.Response, error) {
				if valid && r.URL.Host == educonnectHost && r.Method == "GET" {
					if _, err := r.Cookie("EduSID"); err != nil {
						t.Fatal("IdP cookie was not restored")
					}
					return htmlResponse(samlForm("https://"+eclatHost+"/saml/SAMLAssertionConsumer", "SAMLResponse", "response+&value")), nil
				}
				return next.transport(r)
			})
			renewed, err := Login(context.Background(), state.URL, Credentials{Username: "student"}, Options{ENT: ENTEclatBFC, SSOCookies: state.Cookies, HTTPClient: &http.Client{Transport: transport}})
			if valid {
				if err != nil {
					t.Fatal(err)
				}
				if renewed.Profile().Student.Name != "Alice" {
					t.Fatal("profile missing")
				}
			} else if !errors.Is(err, ErrCredentialsRequired) {
				t.Fatalf("expected credentials required: %v", err)
			}
			if next.credentialPosts != 0 {
				t.Fatal("cookie renewal submitted password")
			}
		})
	}
}
