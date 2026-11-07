package gonote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ENTProvider identifies the explicitly selected authentication provider.
type ENTProvider string

const ENTEclatBFC ENTProvider = "eclat-bfc"

var (
	ErrSSOFlow        = errors.New("parcours ÉCLAT / EduConnect non reconnu")
	ErrSSOInteraction = errors.New("EduConnect demande une intervention dans le navigateur (vérification, consentement ou changement de mot de passe)")
)

// SSOError identifies a step without exposing URLs, cookies, assertions or HTML.
type SSOError struct {
	Stage string
	Err   error
}

func (e *SSOError) Error() string {
	return fmt.Sprintf("Connexion ÉCLAT / EduConnect · %s : %s", e.Stage, e.Err)
}
func (e *SSOError) Unwrap() error { return e.Err }

const eclatHost = "cas.eclat-bfc.fr"
const educonnectHost = "educonnect.education.gouv.fr"
const ssoUserAgent = "Mozilla/5.0 (X11; Linux x86_64) gonote/0.1"

type ssoForm struct {
	action   string
	method   string
	fields   url.Values
	editable []string
}

func parseSSOForms(body []byte) ([]ssoForm, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, ErrSSOFlow
	}
	var forms []ssoForm
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			f := ssoForm{action: attr(n, "action"), method: strings.ToUpper(attr(n, "method")), fields: url.Values{}}
			if f.method == "" {
				f.method = http.MethodGet
			}
			var controls func(*html.Node)
			controls = func(input *html.Node) {
				if input.Type == html.ElementNode && !hasAttr(input, "disabled") {
					name := attr(input, "name")
					kind := strings.ToLower(attr(input, "type"))
					if input.Data == "input" && name != "" {
						if kind == "hidden" {
							f.fields.Add(name, attr(input, "value"))
						} else if kind == "submit" {
							if name == "_eventId_proceed" {
								f.fields.Set(name, attr(input, "value"))
							}
						} else {
							f.editable = append(f.editable, name)
							if name == "j_username" || name == "j_password" {
								f.fields.Set(name, "")
							}
						}
					}
					if input.Data == "button" && name == "_eventId_proceed" {
						f.fields.Set(name, attr(input, "value"))
					}
					if (input.Data == "textarea" || input.Data == "select") && name != "" {
						f.editable = append(f.editable, name)
					}
				}
				for child := input.FirstChild; child != nil; child = child.NextSibling {
					controls(child)
				}
			}
			controls(n)
			forms = append(forms, f)
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return forms, nil
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

type eclatFlow struct {
	http               *http.Client
	base               *url.URL
	username, password string
	submitted          bool
	stage              string
}

func (c *Client) eclatBootstrap(ctx context.Context, cred Credentials, info node, cookies []SessionCookie) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cas, err := url.Parse(info.get("CAS").get("casURL").text())
	if err != nil || !info.get("CAS").get("actif").boolean() || cas.Scheme != "https" || cas.Host != eclatHost || cas.User != nil {
		return nil, &SSOError{Stage: "établissement", Err: fmt.Errorf("cet établissement n’annonce pas le CAS ÉCLAT-BFC")}
	}
	jar, err := newSessionJar()
	if err != nil {
		return nil, ErrSSOFlow
	}
	if err = jar.restore(cookies, c.base, true); err != nil {
		return nil, err
	}
	hc := *c.http
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	flow := eclatFlow{http: &hc, base: c.base, username: cred.Username, password: cred.Password, stage: "ouverture de PRONOTE"}
	u, _ := url.Parse(c.base.String() + "/mobile.eleve.html?fd=1")
	body, err := flow.run(ctx, u)
	if err != nil {
		return nil, err
	}
	// The cookie jar stays attached to the client and is included in explicit
	// session snapshots. Subsequent PRONOTE
	// requests retain the normal same-origin redirect policy of c.http.
	c.http.Jar = jar
	return body, nil
}

// allowed limits navigation to this instance's login pages and the two identity
// providers. It does not allow arbitrary ENT links or follow JavaScript.
func (f *eclatFlow) allowed(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") && u.Path != "/" {
		return false
	}
	if sameOrigin(u, f.base) {
		suffix := strings.TrimPrefix(u.Path, f.base.Path)
		return u.Path == f.base.Path || suffix == "/" || suffix == "/mobile.eleve.html" || suffix == "/eleve.html" || suffix == "/index.html"
	}
	if u.Host == eclatHost {
		switch u.Path {
		case "/login", "/delegate/redirect/EDU", "/clientredirect", "/saml/login", "/saml/SSO", "/saml/SAMLAssertionConsumer":
			return true
		}
	}
	return u.Host == educonnectHost && strings.HasPrefix(u.Path, "/idp/profile/SAML2/")
}

func (f *eclatFlow) run(ctx context.Context, u *url.URL) ([]byte, error) {
	method := http.MethodGet
	var fields url.Values
	var previous *url.URL
	fail := func(err error) ([]byte, error) { return nil, &SSOError{Stage: f.stage, Err: err} }
	for hop := 0; hop < 30; hop++ {
		if !f.allowed(u) {
			return fail(ErrSSOFlow)
		}
		var body io.Reader
		if fields != nil {
			body = strings.NewReader(fields.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return fail(ErrSSOFlow)
		}
		req.Header.Set("User-Agent", ssoUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9")
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if previous != nil {
			referer := previous.Scheme + "://" + previous.Host + "/"
			if sameOrigin(u, previous) {
				referer = previous.String()
			}
			req.Header.Set("Referer", referer)
			if method == http.MethodPost {
				req.Header.Set("Origin", previous.Scheme+"://"+previous.Host)
			}
		}
		res, err := f.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return fail(ErrUnavailable)
		}
		b, readErr := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		_ = res.Body.Close()
		if readErr != nil || len(b) > 4<<20 {
			return fail(ErrSSOFlow)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			return fail(ErrRateLimited)
		}
		if res.StatusCode >= 300 && res.StatusCode <= 399 {
			switch res.StatusCode {
			case 301, 302, 303, 307, 308:
			default:
				return fail(ErrSSOFlow)
			}
			next, err := u.Parse(res.Header.Get("Location"))
			if err != nil || res.Header.Get("Location") == "" || !f.allowed(next) {
				return fail(ErrSSOFlow)
			}
			// PRONOTE drops fd=1 when converting its CAS ticket to the final
			// identifiant handoff. Retain the native-client browser compatibility
			// flag without changing CAS's service URL or its ticket callback.
			if sameOrigin(next, f.base) && next.Query().Has("identifiant") {
				q := next.Query()
				q.Set("fd", "1")
				next.RawQuery = q.Encode()
			}
			if res.StatusCode == 307 || res.StatusCode == 308 {
				if method == http.MethodPost && !sameOrigin(u, next) {
					return fail(ErrSSOFlow)
				}
			} else {
				method = http.MethodGet
				fields = nil
			}
			previous, u = u, next
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fail(ErrUnavailable)
		}
		if sameOrigin(u, f.base) {
			if home, err := bootstrap(b); err == nil && home.get("e").text() != "" && home.get("f").text() != "" {
				return b, nil
			}
		}
		forms, err := parseSSOForms(b)
		if err != nil {
			return fail(err)
		}
		if next, selected, err := f.selectEduConnect(u, forms); err != nil {
			return fail(err)
		} else if selected {
			previous, u = u, next
			method = http.MethodGet
			fields = nil
			f.stage = "choix d’EduConnect"
			continue
		}
		next, nextFields, stage, err := f.chooseForm(u, forms)
		if err != nil {
			return fail(err)
		}
		previous, u = u, next
		fields = nextFields
		method = http.MethodPost
		f.stage = stage
	}
	return fail(fmt.Errorf("trop de redirections d’authentification"))
}

func (f *eclatFlow) selectEduConnect(current *url.URL, forms []ssoForm) (*url.URL, bool, error) {
	if current.Host != eclatHost || current.Path != "/login" || current.Query().Get("selection") == "EDU" {
		return nil, false, nil
	}
	for _, form := range forms {
		selection := false
		for _, name := range form.editable {
			if name == "selection" {
				selection = true
			}
		}
		if form.method != http.MethodGet || !selection {
			continue
		}
		next, err := current.Parse(form.action)
		if err != nil || !f.allowed(next) || next.Host != eclatHost || next.Path != "/login" {
			return nil, false, ErrSSOFlow
		}
		q := next.Query()
		if form.fields.Has("service") {
			q["service"] = form.fields["service"]
		} else if !q.Has("service") {
			q["service"] = current.Query()["service"]
		}
		q.Set("selection", "EDU")
		next.RawQuery = q.Encode()
		return next, true, nil
	}
	return nil, false, nil
}

func (f *eclatFlow) chooseForm(current *url.URL, forms []ssoForm) (*url.URL, url.Values, string, error) {
	for _, form := range forms {
		next, err := current.Parse(form.action)
		if err != nil || !f.allowed(next) || form.method != http.MethodPost {
			continue
		}
		switch {
		case form.fields.Has("SAMLRequest") && current.Host == eclatHost && next.Host == educonnectHost && len(form.editable) == 0:
			data := url.Values{"SAMLRequest": form.fields["SAMLRequest"]}
			if form.fields.Has("RelayState") {
				data["RelayState"] = form.fields["RelayState"]
			}
			return next, data, "requête SAML vers EduConnect", nil
		case form.fields.Has("SAMLResponse") && current.Host == educonnectHost && next.Host == eclatHost && next.Path == "/saml/SAMLAssertionConsumer" && len(form.editable) == 0:
			data := url.Values{"SAMLResponse": form.fields["SAMLResponse"]}
			if form.fields.Has("RelayState") {
				data["RelayState"] = form.fields["RelayState"]
			}
			return next, data, "retour SAML vers ÉCLAT", nil
		case form.fields.Has("j_username") && form.fields.Has("j_password") && current.Host == educonnectHost && next.Host == educonnectHost:
			if f.submitted {
				return nil, nil, "", ErrCredentials
			}
			if f.password == "" {
				return nil, nil, "", ErrCredentialsRequired
			}
			for _, name := range form.editable {
				if name != "j_username" && name != "j_password" {
					return nil, nil, "", ErrSSOInteraction
				}
			}
			form.fields.Set("j_username", f.username)
			form.fields.Set("j_password", f.password)
			form.fields.Set("_eventId_proceed", "")
			f.submitted = true
			return next, form.fields, "identification EduConnect", nil
		case current.Host == educonnectHost && next.Host == educonnectHost && storageForm(form):
			// Shibboleth probes browser localStorage between SAML stages. A
			// native client truthfully declares that facility unsupported.
			form.fields.Set("shib_idp_ls_supported", "false")
			form.fields.Set("_eventId_proceed", "")
			return next, form.fields, "continuité de session EduConnect", nil
		}
	}
	if current.Host == educonnectHost {
		return nil, nil, "", ErrSSOInteraction
	}
	return nil, nil, "", ErrSSOFlow
}

func storageForm(form ssoForm) bool {
	if len(form.editable) > 0 {
		return false
	}
	storage := false
	for key := range form.fields {
		if strings.HasPrefix(key, "shib_idp_ls_") {
			storage = true
			continue
		}
		if key != "csrf_token" && key != "_eventId_proceed" && key != "execution" {
			return false
		}
	}
	return storage
}
