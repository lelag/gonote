package gonote

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// Options customizes authentication, transport and school timezone. AllowHTTP is intended for
// local tests or an explicitly chosen non-TLS instance, never enabled by the CLI.
type Options struct {
	// AllowWrites enables the supported homework-status mutation. It is false by
	// default and must also be explicitly supplied when restoring a session.
	AllowWrites bool
	HTTPClient  *http.Client
	Location    *time.Location
	AllowHTTP   bool
	// ENT selects the initial identity provider. Token reconnections use PRONOTE
	// directly, even when an ENT was selected for the original login.
	ENT ENTProvider
	// SSOCookies permits renewing an ENT session without a password while the
	// identity provider's existing session is still valid.
	SSOCookies []SessionCookie
}

// Credentials are held only during authentication. This type intentionally has
// no JSON representation intended for persistence.
type Credentials struct {
	Username    string
	Password    string
	Token       string
	DeviceID    string
	NavigatorID string
}

// Reconnect contains a mobile token, never a password. Treat it as a secret.
type Reconnect struct {
	Username    string `json:"username"`
	Token       string `json:"token,omitempty"`
	DeviceID    string `json:"device_id"`
	NavigatorID string `json:"navigator_id,omitempty"`
}

const studentSpace = 6 // Mobile_Eleve; desktop Eleve uses 3.

type dialect struct{ order, request, secure, data, signature string }

func dialectFor(v []int) dialect {
	if versionAtLeast(v, 2025, 1, 3) {
		return dialect{"no", "id", "dataSec", "data", "Signature"}
	}
	if versionAtLeast(v, 2024, 3, 9) {
		return dialect{"numeroOrdre", "nom", "donneesSec", "data", "Signature"}
	}
	return dialect{"numeroOrdre", "nom", "donneesSec", "donnees", "_Signature_"}
}
func versionAtLeast(v []int, want ...int) bool {
	for i, w := range want {
		n := 0
		if i < len(v) {
			n = v[i]
		}
		if n != w {
			return n > w
		}
	}
	return true
}

// Client owns a single student session. Its methods are safe for concurrent use;
// PRONOTE operations are serialized, including response decoding and key changes.
type Client struct {
	base                               *url.URL
	http                               *http.Client
	loc                                *time.Location
	gate                               chan struct{}
	cipher                             sessionCipher
	session                            string
	space                              int
	ent                                ENTProvider
	allowWrites                        bool
	order                              int
	compress, encrypt, mobile, invalid bool
	dialect                            dialect
	profile                            Profile
	reconnect                          Reconnect
	tabs                               map[int]bool
	endings                            []string
	blocksPerDay                       int
	blockMinutes                       float64
}

// NormalizeURL removes page names, queries and fragments but preserves path case.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("URL PRONOTE invalide")
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".html") {
		u.Path = path.Dir(u.Path)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// Login authenticates a student and fetches school/account configuration. It
// never registers a device, verifies a PIN, changes a password or writes school
// data. Required security actions are returned as SecurityRequiredError.
func Login(ctx context.Context, rawURL string, credentials Credentials, opts Options) (*Client, error) {
	if opts.ENT != "" && opts.ENT != ENTEclatBFC {
		return nil, fmt.Errorf("fournisseur ENT inconnu")
	}
	if strings.TrimSpace(credentials.Username) == "" || (credentials.Password == "" && credentials.Token == "" && !(opts.ENT != "" && len(opts.SSOCookies) > 0)) {
		return nil, ErrCredentials
	}
	base, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(base)
	if u.Scheme != "https" && !opts.AllowHTTP {
		return nil, fmt.Errorf("HTTPS est nécessaire pour protéger les identifiants")
	}
	loc := opts.Location
	if loc == nil {
		loc, _ = time.LoadLocation("Europe/Paris")
	}
	hc := http.Client{Timeout: 30 * time.Second}
	if opts.HTTPClient != nil {
		hc = *opts.HTTPClient
	}
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != u.Scheme || req.URL.Host != u.Host {
			return fmt.Errorf("redirection refusée")
		}
		return nil
	}
	c := &Client{
		base:         u,
		http:         &hc,
		loc:          loc,
		gate:         make(chan struct{}, 1),
		tabs:         map[int]bool{},
		blocksPerDay: 0,
		blockMinutes: 30,
		space:        studentSpace,
		ent:          opts.ENT,
		allowWrites:  opts.AllowWrites,
	}
	if hc.Jar == nil {
		hc.Jar, err = newSessionJar()
		if err != nil {
			return nil, err
		}
	}
	if credentials.DeviceID == "" {
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			return nil, err
		}
		credentials.DeviceID = hex.EncodeToString(b)
	}
	c.reconnect = Reconnect{Username: credentials.Username, DeviceID: credentials.DeviceID, NavigatorID: credentials.NavigatorID}
	if err = c.login(ctx, credentials, opts.SSOCookies); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) login(ctx context.Context, cred Credentials, cookies []SessionCookie) (loginErr error) {
	entLogin := c.ent != "" && cred.Token == ""
	stage := "initialisation de PRONOTE"
	defer func() {
		if loginErr != nil && entLogin {
			if _, wrapped := loginErr.(*SSOError); !wrapped {
				loginErr = &SSOError{Stage: stage, Err: loginErr}
			}
		}
	}()
	b, err := c.get(ctx, "/infoMobileApp.json?id=0D264427-EEFC-4810-A9E9-346942A862A4")
	if err != nil {
		return err
	}
	info, err := parseJSON(b)
	if err != nil {
		return err
	}
	version := ints(info.get("version"))
	if len(version) < 3 {
		return ErrProtocol
	}
	c.dialect = dialectFor(version)
	c.profile.School = info.get("nomEtab").text()
	if len(info.get("espaces").list()) > 0 {
		found := false
		for _, s := range info.get("espaces").list() {
			if s.get("genreEspace").v != nil && s.get("genreEspace").integer() == studentSpace {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("cet établissement ne propose pas d’espace élève")
		}
	}
	c.mobile = cred.Token != ""
	if entLogin {
		b, err = c.eclatBootstrap(ctx, cred, info, cookies)
	} else {
		b, err = c.get(ctx, "/mobile.eleve.html?fd=1&login=true&bydlg=A6ABB224-12DD-4E31-AD3E-8A39A1C2C335")
	}
	if err != nil {
		return err
	}
	home, err := bootstrap(b)
	if err != nil {
		return err
	}
	space := home.get("a").integer()
	if space != studentSpace && !(entLogin && space == 3) {
		return ErrProtocol
	}
	c.space = space
	loginUsername, loginSecret := cred.Username, cred.Password
	if entLogin {
		loginUsername = home.get("e").text()
		loginSecret = home.get("f").text()
		if loginUsername == "" || loginSecret == "" {
			return &SSOError{Stage: "initialisation de PRONOTE", Err: ErrProtocol}
		}
	}
	c.session = home.get("h").text()
	if session, parseErr := strconv.ParseUint(c.session, 10, 64); parseErr != nil || session == 0 {
		return ErrProtocol
	}
	if versionAtLeast(version, 2025, 1, 3) {
		c.compress = home.get("CoA").boolean()
		c.encrypt = home.get("CrA").boolean()
	} else {
		c.compress = !home.get("sCoA").boolean()
		c.encrypt = !home.get("sCrA").boolean()
	}
	iv := make([]byte, 16)
	if _, err = rand.Read(iv); err != nil {
		return err
	}
	uuid := iv
	if home.has("MR") || home.get("http").boolean() {
		// Legacy PRONOTE public RSA modulus, shared by instances that do not
		// announce their own key. This is public protocol data, not a secret.
		n := new(big.Int)
		n.SetString("130337874517286041778445012253514395801341480334668979416920989365464528904618150245388048105865059387076357492684573172203245221386376405947824377827224846860699130638566643129067735803555082190977267155957271492183684665050351182476506458843580431717209261903043895605014125081521285387341454154194253026277", 10)
		e := 65537
		if home.has("MR") {
			if _, ok := n.SetString(home.get("MR").text(), 16); !ok {
				return ErrProtocol
			}
		}
		if home.has("ER") {
			ex, er := strconv.ParseInt(home.get("ER").text(), 16, 32)
			if er != nil {
				return ErrProtocol
			}
			e = int(ex)
		}
		uuid, err = rsa.EncryptPKCS1v15(rand.Reader, &rsa.PublicKey{N: n, E: e}, iv)
		if err != nil {
			return ErrProtocol
		}
	}
	var navigator any
	if cred.NavigatorID != "" {
		navigator = cred.NavigatorID
	}
	stage = "paramètres de PRONOTE"
	params, err := c.call(ctx, "FonctionParametres", map[string]any{"Uuid": base64.StdEncoding.EncodeToString(uuid), "identifiantNav": navigator}, nil, iv)
	if err != nil {
		return err
	}
	g := params.get("General")
	c.profile.FirstMonday, err = g.get("PremierLundi").date(c.loc)
	if err != nil {
		return err
	}
	c.blocksPerDay = g.get("PlacesParJour").integer()
	if n := g.get("PlacesParHeure").number(); n > 0 {
		c.blockMinutes = 60 / n
	}
	for _, h := range g.get("listeHeuresFin").list() {
		c.endings = append(c.endings, h.get("L").text())
	}
	if c.profile.School == "" {
		c.profile.School = g.get("NomEtablissement").text()
	}
	for _, p := range g.get("ListePeriodes").list() {
		c.profile.Periods = append(c.profile.Periods, Period{Resource: p.resource(), Start: optionalDate(p.get("dateDebut"), c.loc), End: optionalDate(p.get("dateFin"), c.loc)})
	}
	c.reconnect.NavigatorID = params.get("identifiantNav").text()
	stage = "identification PRONOTE"
	identity, err := c.call(ctx, "Identification", map[string]any{
		"demandeConnexionAppliMobile": !c.mobile && !entLogin, "demandeConnexionAppliMobileJeton": false,
		"demandeConnexionAuto": false, "enConnexionAppliMobile": c.mobile, "enConnexionAuto": false,
		"genreConnexion": 0, "genreEspace": c.space, "identifiant": loginUsername,
		"informationsAppareil": map[string]any{"modele": "Terminal", "platforme": "Go"},
		"loginTokenSAV":        "", "pourENT": entLogin, "uuidAppliMobile": cred.DeviceID,
	}, nil, nil)
	if err != nil {
		return err
	}
	if identity.get("challenge").text() == "" {
		return ErrProtocol
	}
	username := loginUsername
	secret := loginSecret
	if c.mobile {
		secret = cred.Token
	}
	if identity.get("modeCompLog").boolean() {
		username = strings.ToLower(username)
	}
	if identity.get("modeCompMdp").boolean() {
		secret = strings.ToLower(secret)
	}
	hash := sha256.Sum256([]byte(identity.get("alea").text() + strings.TrimSpace(secret)))
	temp := sessionCipher{key: []byte(username + strings.ToUpper(hex.EncodeToString(hash[:]))), iv: iv}
	var decoded []byte
	if entLogin {
		// ENT supplies a temporary secret in Start(...). Its challenge uses
		// SHA256(secret), without the username/salt or alternating-byte step.
		hash = sha256.Sum256([]byte(loginSecret))
		temp.key = []byte(strings.ToUpper(hex.EncodeToString(hash[:])))
		decoded = []byte(identity.get("challenge").text())
	} else {
		challenge, decryptErr := temp.decryptText(identity.get("challenge").text())
		if decryptErr != nil {
			return ErrCredentials
		}
		// Challenges generated by this protocol are ASCII strings.
		decoded = make([]byte, 0, len(challenge)/2)
		for i := 0; i < len(challenge); i += 2 {
			decoded = append(decoded, challenge[i])
		}
	}
	stage = "authentification PRONOTE"
	auth, err := c.call(ctx, "Authentification", map[string]any{"challenge": hex.EncodeToString(temp.encrypt(decoded)), "connexion": 0, "espace": c.space}, nil, nil)
	if err != nil {
		return err
	}
	if access := auth.get("Acces").integer(); access != 0 {
		if access == 1 {
			return ErrCredentials
		}
		return ErrAccessDenied
	}
	final, err := temp.decryptText(auth.get("cle").text())
	if err != nil {
		return ErrCredentials
	}
	c.cipher.key = nil
	for _, b := range strings.Split(string(final), ",") {
		i, e := strconv.Atoi(b)
		if e != nil || i < 0 || i > 255 {
			return ErrProtocol
		}
		c.cipher.key = append(c.cipher.key, byte(i))
	}
	if actions := ints(auth.get("actionsDoubleAuth")); len(actions) > 0 {
		return &SecurityRequiredError{Actions: actions}
	}
	c.reconnect.Token = auth.get("jetonConnexionAppliMobile").text()
	if entLogin && c.reconnect.Token != "" {
		c.reconnect.Username = loginUsername
	}
	if identity.get("login").text() != "" {
		c.reconnect.Username = identity.get("login").text()
	}
	stage = "lecture du compte PRONOTE"
	user, err := c.call(ctx, "ParametresUtilisateur", map[string]any{}, nil, nil)
	if err != nil {
		return err
	}
	resource := user.get("ressource")
	c.profile.Student = resource.resource()
	if c.profile.Student.ID == "" {
		return ErrProtocol
	}
	c.profile.Class = resource.get("classeDEleve").get("L").text()
	var walk func(node)
	walk = func(n node) {
		for _, t := range n.list() {
			c.tabs[t.get("G").integer()] = true
			walk(t.get("Onglet"))
		}
	}
	walk(user.get("listeOnglets"))
	for _, t := range resource.get("listeOngletsPourPeriodes").list() {
		if t.get("G").integer() == 198 {
			c.profile.DefaultGradePeriod = t.get("periodeParDefaut").get("N").text()
		}
	}
	return nil
}

func (c *Client) Profile() Profile {
	p := c.profile
	p.Periods = append([]Period(nil), p.Periods...)
	return p
}
func (c *Client) Reconnect() Reconnect { return c.reconnect }

func (c *Client) userAgent() string {
	if c.ent != "" {
		return ssoUserAgent
	}
	return "gonote/0.1"
}
func (c *Client) WeekNumber(t time.Time) int {
	// Use calendar days, not elapsed 24-hour intervals across DST changes.
	t = t.In(c.loc)
	f := c.profile.FirstMonday.In(c.loc)
	a := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, time.UTC)
	days := int(a.Sub(b).Hours() / 24)
	weeks := days / 7
	if days < 0 && days%7 != 0 {
		weeks--
	}
	return 1 + weeks
}

func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+endpoint, nil)
	if err != nil {
		return nil, ErrProtocol
	}
	return c.send(req)
}
func (c *Client) send(req *http.Request) ([]byte, error) {
	req.Header.Set("User-Agent", c.userAgent())
	if c.mobile {
		req.AddCookie(&http.Cookie{Name: "appliMobile", Value: "1"})
	}
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) {
			return nil, ErrSessionExpired
		}
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, fmt.Errorf("connexion à PRONOTE impossible")
	}
	defer res.Body.Close()
	if res.StatusCode == 429 {
		return nil, ErrRateLimited
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, ErrAccessDenied
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, ErrUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(b) > 16<<20 {
		return nil, fmt.Errorf("réponse PRONOTE trop volumineuse")
	}
	return b, nil
}

func allowedRPC(name string) bool {
	switch name {
	case "FonctionParametres", "Identification", "Authentification", "ParametresUtilisateur", "PageCahierDeTexte", "PageEmploiDuTemps", "DernieresNotes", "SaisieTAFFaitEleve":
		return true
	}
	return false
}

func (c *Client) call(ctx context.Context, name string, data, signature any, nextIV []byte) (node, error) {
	if !allowedRPC(name) || name == "SaisieTAFFaitEleve" && !c.allowWrites {
		return node{}, ErrReadOnly
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return node{}, ctx.Err()
	}
	if c.invalid {
		return node{}, ErrSessionExpired
	}
	c.order++
	order := c.cipher.encryptText(strconv.Itoa(c.order))
	d := c.dialect
	payload := map[string]any{}
	if data != nil {
		payload[d.data] = data
	}
	if signature != nil {
		payload[d.signature] = signature
	}
	var secure any = payload
	if c.compress || c.encrypt {
		b, _ := json.Marshal(payload)
		if c.compress {
			var buf bytes.Buffer
			w, _ := flate.NewWriter(&buf, 6)
			_, _ = w.Write([]byte(hex.EncodeToString(b)))
			_ = w.Close()
			b = buf.Bytes()
		}
		if c.encrypt {
			b = c.cipher.encrypt(b)
		}
		secure = hex.EncodeToString(b)
	}
	session, _ := strconv.ParseUint(c.session, 10, 64)
	body, _ := json.Marshal(map[string]any{"session": session, d.order: order, d.request: name, d.secure: secure})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/appelfonction/"+strconv.Itoa(c.space)+"/"+c.session+"/"+order, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	b, err := c.send(req)
	if err != nil {
		c.invalid = true
		return node{}, err
	}
	c.order++
	if nextIV != nil {
		c.cipher.iv = bytes.Clone(nextIV)
	}
	response, err := parseJSON(b)
	if err != nil {
		c.invalid = true
		return node{}, classifyError(string(b))
	}
	if response.get("Erreur").truthy() {
		c.invalid = true
		return node{}, classifyError(response.get("Erreur").get("Titre").text())
	}
	inner := response.get(d.secure)
	if s, ok := inner.v.(string); ok {
		b, err = hex.DecodeString(s)
		if err == nil && c.encrypt {
			b, err = c.cipher.decrypt(b)
		}
		if err == nil && c.compress {
			r := flate.NewReader(bytes.NewReader(b))
			b, err = io.ReadAll(io.LimitReader(r, 16<<20+1))
			_ = r.Close()
			if len(b) > 16<<20 {
				err = ErrProtocol
			}
		}
		if err == nil {
			inner, err = parseJSON(b)
		}
		if err != nil {
			c.invalid = true
			return node{}, ErrProtocol
		}
	}
	if inner.get(d.signature).get("Erreur").truthy() {
		return node{}, ErrAccessDenied
	}
	if inner.v == nil {
		return node{}, ErrProtocol
	}
	return inner.get(d.data), nil
}

func classifyError(s string) error {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "page a expir"):
		return ErrSessionExpired
	case strings.Contains(s, "adresse ip"), strings.Contains(s, "vous avez d"):
		return ErrRateLimited
	case strings.Contains(s, "refus"):
		return ErrAccessDenied
	case strings.Contains(s, "indisponible"), strings.Contains(s, "page dem"), strings.Contains(s, "impossible d'a"):
		return ErrUnavailable
	}
	return ErrProtocol
}
