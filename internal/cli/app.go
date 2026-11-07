package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/credentials"
	"github.com/lelag/gonote/internal/demo"
	"github.com/lelag/gonote/internal/store"
)

type options struct {
	ent, storage                      string
	remember, noPager                 bool
	demo, offline, refresh            bool
	json, noColor, color              bool
	all, done, summary                bool
	agenda, canceled                  bool
	tomorrow, yesterday, today        bool
	on, week, subject, period, output string
	width                             int
}
type App struct {
	Version     string
	In          io.Reader
	Out, Err    io.Writer
	Store       store.Store
	Now         func() time.Time
	opts        options
	client      schoolClient
	Vault       credentials.Store
	session     *store.SessionFile
	saved       store.SavedSession
	authErr     error
	renewed     bool
	writes      bool
	loginClient func(context.Context, string, gonote.Credentials, gonote.Options) (schoolClient, error)
	restore     func(gonote.SessionState, gonote.Options) (schoolClient, error)
	cfg         store.Config
	loaded      bool
	profile     gonote.Profile
	fetched     time.Time
	cache       bool
	ui          *renderer
	ctx         context.Context
}

func New(in io.Reader, out, errOut io.Writer, s store.Store) *App {
	loc, _ := time.LoadLocation("Europe/Paris")
	return &App{
		Version: "dev",
		In:      in,
		Out:     out,
		Err:     errOut,
		Store:   s,
		Vault:   credentials.New(s.ConfigDir),
		loginClient: func(ctx context.Context, u string, c gonote.Credentials, o gonote.Options) (schoolClient, error) {
			return gonote.Login(ctx, u, c, o)
		},
		restore: func(s gonote.SessionState, o gonote.Options) (schoolClient, error) {
			return gonote.RestoreSession(s, o)
		},
		Now: func() time.Time { return time.Now().In(loc) },
	}
}
func (a *App) Command() *cobra.Command {
	root := &cobra.Command{Use: "gonote", Version: a.Version, Short: "Ta journée de collège, dans le terminal.", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs}
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SetVersionTemplate("gonote {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	f := root.PersistentFlags()
	f.BoolVar(&a.opts.demo, "demo", false, "Explorer avec un compte fictif, sans connexion")
	f.BoolVar(&a.opts.offline, "hors-ligne", false, "Lire uniquement les données déjà enregistrées")
	f.BoolVar(&a.opts.refresh, "actualiser", false, "Récupérer les données depuis PRONOTE")
	f.BoolVar(&a.opts.json, "json", false, "Produire du JSON pour les scripts")
	f.BoolVar(&a.opts.noColor, "no-color", false, "Désactiver les couleurs")
	f.BoolVar(&a.opts.color, "color", false, "Activer les couleurs même dans un fichier")
	f.BoolVar(&a.opts.noPager, "no-pager", false, "Afficher les longs détails sans pagination")
	f.IntVar(&a.opts.width, "largeur", 0, "Largeur d’affichage (détectée automatiquement)")
	f.BoolVar(&a.opts.tomorrow, "demain", false, "Afficher la date de demain")
	f.BoolVar(&a.opts.yesterday, "hier", false, "Afficher la date d’hier")
	f.BoolVar(&a.opts.today, "aujourdhui", false, "Afficher la date d’aujourd’hui")
	f.StringVar(&a.opts.on, "le", "", "Choisir une date : AAAA-MM-JJ")
	f.StringVar(&a.opts.week, "semaine", "", "Choisir une semaine : courante, prochaine, precedente")
	f.StringVar(&a.opts.subject, "matiere", "", "Filtrer par matière (ex. maths)")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		a.ctx = cmd.Context()
		a.ui = newRenderer(a.Out, a.opts)
		if a.opts.offline && a.opts.refresh {
			return fmt.Errorf("--hors-ligne et --actualiser ne peuvent pas être utilisés ensemble")
		}
		if a.opts.noColor && a.opts.color {
			return fmt.Errorf("choisis --color ou --no-color")
		}
		if a.opts.width != 0 && a.opts.width < 24 {
			return fmt.Errorf("la largeur minimale est de 24 colonnes")
		}
		_, err := a.selection("day")
		return err
	}
	root.RunE = func(cmd *cobra.Command, args []string) error { return a.overview() }
	add := func(use, desc string, aliases []string, run func([]string) error) *cobra.Command {
		c := &cobra.Command{Use: use, Short: desc, Aliases: aliases, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return run(args) }}
		c.InitDefaultHelpFlag()
		c.Flags().Lookup("help").Usage = "Afficher l’aide"
		root.AddCommand(c)
		return c
	}
	hw := add("devoirs", "Ce qu’il reste à faire, par date de rendu", []string{"homework"}, func(_ []string) error { return a.homework() })
	hw.Flags().BoolVar(&a.opts.all, "tout", false, "Inclure les devoirs déjà faits")
	hw.Flags().BoolVar(&a.opts.done, "faits", false, "Afficher uniquement les devoirs faits")
	done := add("fait <réf>", "Marquer un devoir comme fait sur PRONOTE", []string{"done"}, func(args []string) error { return a.setHomeworkDone(args[0], true) })
	done.Args = cobra.ExactArgs(1)
	todo := add("a-faire <réf>", "Remettre un devoir à faire sur PRONOTE", []string{"todo"}, func(args []string) error { return a.setHomeworkDone(args[0], false) })
	todo.Args = cobra.ExactArgs(1)
	add("cours", "Ce qui a été étudié en classe", []string{"lessons"}, func(_ []string) error { return a.lessons() })
	gr := add("notes", "Les derniers résultats et les moyennes", []string{"grades"}, func(_ []string) error { return a.grades() })
	gr.Flags().BoolVar(&a.opts.summary, "resume", false, "Afficher les moyennes fournies par PRONOTE")
	gr.Flags().BoolVar(&a.opts.all, "tout", false, "Afficher toutes les notes de la période (20 par défaut)")
	gr.Flags().StringVar(&a.opts.period, "periode", "", "Choisir une période par nom ou identifiant")
	add("periodes", "Les trimestres ou semestres disponibles", []string{"periods"}, func(_ []string) error { return a.periods() })
	tt := add("emploi-du-temps", "La semaine de cours, les salles et les changements", []string{"timetable", "edt"}, func(_ []string) error { return a.timetable() })
	tt.Flags().BoolVar(&a.opts.agenda, "agenda", false, "Afficher une liste, même sur un écran large")
	tt.Flags().BoolVar(&a.opts.canceled, "annulations", false, "Inclure les cours annulés remplacés par un autre cours")
	show := add("voir <réf>", "Lire le détail d’un devoir, cours ou résultat", []string{"show"}, a.show)
	show.Args = cobra.ExactArgs(1)
	files := add("fichiers <réf>", "Lister les pièces jointes d’un élément", []string{"files"}, a.files)
	files.Args = cobra.ExactArgs(1)
	dl := add("telecharger <réf> [numéro]", "Enregistrer une pièce jointe sur cet ordinateur", []string{"download"}, a.download)
	dl.Args = cobra.RangeArgs(1, 2)
	dl.Flags().StringVar(&a.opts.output, "vers", "", "Fichier de destination (sans écrasement)")
	login := add("connexion", "Configurer le compte élève", []string{"login"}, func(_ []string) error { return a.login() })
	login.Flags().StringVar(&a.opts.ent, "ent", "", "Se connecter via un ENT : eclat-bfc (EduConnect)")
	login.Flags().BoolVar(&a.opts.remember, "memoriser", false, "Mémoriser le mot de passe pour renouveler la session")
	login.Flags().StringVar(&a.opts.storage, "stockage", "", "Stockage du mot de passe : trousseau (défaut) ou fichier (en clair, privé)")
	pw := add("mot-de-passe", "Mémoriser ou mettre à jour le mot de passe local", nil, func(_ []string) error { return a.updatePassword() })
	pw.Flags().StringVar(&a.opts.storage, "stockage", "", "Stockage : trousseau ou fichier (en clair, privé)")
	add("oublier-mot-de-passe", "Supprimer le mot de passe mémorisé localement", nil, func(_ []string) error { return a.forgetPassword() })
	add("anglais", "Découvrir les commandes équivalentes en anglais", nil, func(_ []string) error {
		fmt.Fprintln(a.Out, "devoirs → homework\nfait → done\na-faire → todo\ncours → lessons\nnotes → grades\nemploi-du-temps → timetable\nvoir → show\nfichiers → files\ntelecharger → download\nconnexion → login\n\nLes options et les réponses restent en français.")
		return nil
	})
	root.SetHelpCommand(&cobra.Command{Use: "aide [commande]", Aliases: []string{"help"}, Short: "Afficher l’aide", RunE: func(cmd *cobra.Command, args []string) error {
		target := root
		if len(args) > 0 {
			var err error
			target, _, err = root.Find(args)
			if err != nil {
				return err
			}
		}
		return target.Help()
	}})
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) { r := newRenderer(a.Out, a.opts); r.help(cmd) })
	root.InitDefaultVersionFlag()
	root.Flags().Lookup("version").Usage = "Afficher la version"
	root.InitDefaultHelpFlag()
	root.Flags().Lookup("help").Usage = "Afficher l’aide"
	return root
}

func (a *App) selection(defaultMode string) (gonote.DateRange, error) {
	now := a.Now()
	loc := now.Location()
	d := midnight(now)
	count := 0
	for _, b := range []bool{a.opts.tomorrow, a.opts.yesterday, a.opts.today, a.opts.on != "", a.opts.week != ""} {
		if b {
			count++
		}
	}
	if count > 1 {
		return gonote.DateRange{}, fmt.Errorf("choisis une seule option de date")
	}
	if a.opts.on != "" {
		var err error
		d, err = time.ParseInLocation("2006-01-02", a.opts.on, loc)
		if err != nil {
			return gonote.DateRange{}, fmt.Errorf("date invalide : utilise AAAA-MM-JJ")
		}
	}
	if a.opts.tomorrow {
		d = d.AddDate(0, 0, 1)
	}
	if a.opts.yesterday {
		d = d.AddDate(0, 0, -1)
	}
	if a.opts.week != "" || count == 0 && defaultMode == "week" {
		d = d.AddDate(0, 0, -(int(d.Weekday())+6)%7)
		switch fold(a.opts.week) {
		case "", "courante", "actuelle", "current":
		case "prochaine", "next":
			d = d.AddDate(0, 0, 7)
		case "precedente", "previous":
			d = d.AddDate(0, 0, -7)
		default:
			return gonote.DateRange{}, fmt.Errorf("semaine inconnue : courante, prochaine ou precedente")
		}
		return gonote.DateRange{From: d, To: d.AddDate(0, 0, 6)}, nil
	}
	if count == 0 && defaultMode == "upcoming" {
		return gonote.DateRange{From: d.AddDate(0, 0, -14), To: d.AddDate(0, 0, 30)}, nil
	}
	return gonote.DateRange{From: d, To: d}, nil
}
func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
func fold(s string) string {
	return strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "î", "i", "ï", "i", "ô", "o", "ù", "u", "û", "u", "ç", "c", "œ", "oe").Replace(strings.ToLower(s))
}
func (a *App) matches(s string) bool {
	q := fold(a.opts.subject)
	if q == "maths" {
		q = "math"
	}
	return strings.Contains(fold(s), q)
}
func (a *App) prepare() error {
	if a.loaded {
		return nil
	}
	a.loaded = true
	if a.opts.demo {
		a.Store.CacheDir = filepath.Join(a.Store.CacheDir, "demo")
		a.profile = demo.New(a.Now()).Profile
		return nil
	}
	cfg, err := a.Store.LoadConfig()
	if err != nil {
		a.loaded = false
		return fmt.Errorf("aucun compte configuré : lance gonote connexion")
	}
	a.cfg = cfg
	a.profile = cfg.Profile
	a.Store = a.Store.Account(cfg)
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return fmt.Errorf("fuseau horaire du compte invalide")
		}
		previous := a.Now
		a.Now = func() time.Time { return previous().In(loc) }
	}
	return nil
}
func (a *App) credentials() gonote.Credentials {
	cred := gonote.Credentials{Username: a.cfg.Auth.Username, Token: a.cfg.Auth.Token, DeviceID: a.cfg.Auth.DeviceID, NavigatorID: a.cfg.Auth.NavigatorID}
	if cred.Token == "" && a.cfg.ENT != "" {
		cred.Username = a.cfg.ENTUsername
	}
	return cred
}

func (a *App) interactive() bool { f, ok := a.In.(*os.File); return ok && term.IsTerminal(f.Fd()) }
func (a *App) password(dst *string) error {
	title := "Mot de passe PRONOTE"
	if a.cfg.ENT == gonote.ENTEclatBFC {
		title = "Mot de passe EduConnect"
	}
	return huh.NewForm(huh.NewGroup(huh.NewInput().Title(title).Description("Saisie masquée. Stockage local uniquement si tu l’actives.").EchoMode(huh.EchoModePassword).Value(dst))).WithInput(a.In).WithOutput(a.Err).WithTheme(huh.ThemeFunc(huh.ThemeCatppuccin)).WithKeyMap(frenchKeys()).RunWithContext(a.ctx)
}
func (a *App) login() error {
	if a.opts.storage != "" && (!a.opts.remember || !credentials.ValidMode(a.opts.storage)) {
		return fmt.Errorf("utilise --memoriser --stockage trousseau ou fichier")
	}

	if a.opts.ent != "" && a.opts.ent != string(gonote.ENTEclatBFC) {
		return fmt.Errorf("ENT inconnu : le fournisseur disponible est eclat-bfc")
	}
	if a.opts.demo {
		return fmt.Errorf("le mode démo n’a pas besoin de compte")
	}
	if a.opts.offline {
		return fmt.Errorf("la connexion nécessite un accès au réseau")
	}
	if a.opts.json {
		return fmt.Errorf("connexion est une commande interactive")
	}
	if !a.interactive() {
		return fmt.Errorf("lance gonote connexion dans un terminal interactif")
	}
	var school, user, password string
	usernameTitle := "Ton identifiant élève"
	passwordTitle := "Ton mot de passe"
	if a.opts.ent != "" {
		usernameTitle = "Ton identifiant EduConnect élève"
		passwordTitle = "Ton mot de passe EduConnect"
	}
	required := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("ce champ est nécessaire")
		}
		return nil
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("L’adresse PRONOTE du collège").Placeholder("https://college.index-education.net/pronote").Value(&school).Validate(func(s string) error { _, err := gonote.NormalizeURL(s); return err }),
		huh.NewInput().Title(usernameTitle).Value(&user).Validate(required),
		huh.NewInput().Title(passwordTitle).Description("La session sera conservée. Le mot de passe reste temporaire sauf avec --memoriser.").EchoMode(huh.EchoModePassword).Value(&password).Validate(required),
	)).WithInput(a.In).WithOutput(a.Err).WithTheme(huh.ThemeFunc(huh.ThemeCatppuccin)).WithKeyMap(frenchKeys())
	if err := form.RunWithContext(a.ctx); err != nil {
		return err
	}
	base, err := gonote.NormalizeURL(school)
	if err != nil {
		return err
	}
	cfg := store.Config{URL: base, LoginUsername: user, Timezone: "Europe/Paris", ENT: gonote.ENTProvider(a.opts.ent)}
	if cfg.ENT != "" {
		cfg.ENTUsername = user
	}
	// Preserve the remembered-password preference when reconnecting the same account.
	if old, e := a.Store.LoadConfig(); e == nil && old.CredentialKey() == cfg.CredentialKey() {
		cfg.PasswordStorage = old.PasswordStorage
	}
	a.cfg = cfg
	if err = a.lockSession(); err != nil {
		return err
	}
	c, err := a.loginClient(a.ctx, base, gonote.Credentials{Username: user, Password: password}, gonote.Options{ENT: cfg.ENT})
	if err != nil {
		return err
	}
	if a.opts.remember || a.cfg.PasswordStorage != "" {
		mode := a.opts.storage
		if mode == "" {
			mode = a.cfg.PasswordStorage
		}
		if mode == "" {
			mode = credentials.Keyring
		}
		if err = a.Vault.Set(mode, cfg.CredentialKey(), password); err != nil {
			return err
		}
		a.cfg.PasswordStorage = mode
	}
	password = ""
	if err = a.accept(c); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, a.ui.success("Compte prêt · "+plain(a.profile.Student.Name)))
	fmt.Fprintln(a.Out, "Session conservée : les prochaines commandes la réutiliseront.")
	fmt.Fprintln(a.Out, "Lance gonote pour découvrir ta journée.")
	return nil
}

func load[T any](a *App, key string, fetch func() (T, error)) (T, error) {
	var zero T
	if err := a.prepare(); err != nil {
		return zero, err
	}
	if !a.opts.demo && !a.opts.refresh {
		e, err := a.Store.Load(key)
		if covering, coverErr := a.Store.Covering(key); coverErr == nil && (err != nil || covering.Fetched.After(e.Fetched)) {
			e = covering
			err = nil
		}
		if err == nil && (a.opts.offline || a.Now().Sub(e.Fetched) < 10*time.Minute && a.Now().Sub(e.Fetched) >= 0) {
			var result T
			if err = json.Unmarshal(e.Data, &result); err == nil {
				a.profile = e.Profile
				a.fetched = e.Fetched
				a.cache = true
				return result, nil
			}
		}
	}
	if a.opts.offline && !a.opts.demo {
		return zero, fmt.Errorf("aucune donnée enregistrée pour cette période ; relance sans --hors-ligne")
	}
	if !a.opts.demo {
		if err := a.connect(); err != nil {
			return zero, err
		}
	}
	var result T
	var err error
	if a.opts.demo {
		result, err = fetch()
	} else {
		err = a.withSession(func() error { var e error; result, e = fetch(); return e }, true)
	}
	if err != nil {
		return zero, err
	}
	a.fetched = a.Now()
	a.cache = false
	if !a.opts.demo {
		b, err := json.Marshal(result)
		if err != nil {
			return zero, err
		}
		if err = a.Store.Save(key, store.Envelope{Fetched: a.fetched, Profile: a.profile, Data: b}); err != nil {
			return zero, err
		}
	}
	return result, nil
}
func rangeKey(kind string, r gonote.DateRange) string {
	return kind + ":" + r.From.Format("2006-01-02") + ":" + r.To.Format("2006-01-02")
}
func contains(t time.Time, r gonote.DateRange) bool {
	return !t.Before(r.From) && t.Before(r.To.AddDate(0, 0, 1))
}
func (a *App) getHomework(r gonote.DateRange) ([]gonote.Assignment, error) {
	items, err := load(a, rangeKey("homework", r), func() ([]gonote.Assignment, error) {
		if a.opts.demo {
			out := []gonote.Assignment{}
			for _, x := range demo.New(a.Now()).Homework {
				if contains(x.Due, r) {
					out = append(out, x)
				}
			}
			return out, nil
		}
		return a.client.Homework(a.ctx, r)
	})
	filtered := make([]gonote.Assignment, 0, len(items))
	for _, x := range items {
		if contains(x.Due, r) {
			filtered = append(filtered, x)
		}
	}
	items = filtered
	if err == nil {
		for _, x := range items {
			if _, err = a.Store.Remember("h", x.ID, x, a.fetched); err != nil {
				return nil, err
			}
		}
	}
	return items, err
}
func (a *App) getLessons(r gonote.DateRange) ([]gonote.Lesson, error) {
	items, err := load(a, rangeKey("lessons", r), func() ([]gonote.Lesson, error) {
		if a.opts.demo {
			out := []gonote.Lesson{}
			for _, x := range demo.New(a.Now()).Lessons {
				if contains(x.Start, r) {
					out = append(out, x)
				}
			}
			return out, nil
		}
		return a.client.Lessons(a.ctx, r)
	})
	filtered := make([]gonote.Lesson, 0, len(items))
	for _, x := range items {
		if contains(x.Start, r) {
			filtered = append(filtered, x)
		}
	}
	items = filtered
	if err == nil {
		for _, x := range items {
			if _, err = a.Store.Remember("l", x.ID, x, a.fetched); err != nil {
				return nil, err
			}
		}
	}
	return items, err
}
func (a *App) getTimetable(r gonote.DateRange) ([]gonote.TimetableEntry, error) {
	items, err := load(a, rangeKey("timetable", r), func() ([]gonote.TimetableEntry, error) {
		if a.opts.demo {
			out := []gonote.TimetableEntry{}
			for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
				for _, x := range demo.New(d).Timetable {
					if midnight(x.Start).Equal(d) {
						out = append(out, x)
					}
				}
			}
			return out, nil
		}
		return a.client.Timetable(a.ctx, r)
	})
	filtered := make([]gonote.TimetableEntry, 0, len(items))
	for _, x := range items {
		if contains(x.Start, r) {
			filtered = append(filtered, x)
		}
	}
	items = filtered
	if err == nil {
		for _, x := range items {
			if _, err = a.Store.Remember("t", x.ID, x, a.fetched); err != nil {
				return nil, err
			}
		}
	}
	return items, err
}
func (a *App) getGrades() (gonote.GradeReport, error) {
	if err := a.prepare(); err != nil {
		return gonote.GradeReport{}, err
	}
	id := a.profile.DefaultGradePeriod
	if a.opts.period != "" {
		id = ""
		for _, p := range a.profile.Periods {
			if p.ID == a.opts.period || fold(p.Name) == fold(a.opts.period) {
				id = p.ID
				break
			}
		}
		if id == "" {
			return gonote.GradeReport{}, fmt.Errorf("période inconnue : lance gonote periodes")
		}
	}
	var selected gonote.Period
	for _, p := range a.profile.Periods {
		if p.ID == id {
			selected = p
			break
		}
	}
	key := "grades:" + id
	if selected.Name != "" {
		key = fmt.Sprintf("grades:%s:%d:%s:%s", selected.Name, selected.Kind, selected.Start.Format("2006-01-02"), selected.End.Format("2006-01-02"))
	}
	result, err := load(a, key, func() (gonote.GradeReport, error) {
		if a.opts.demo {
			return demo.New(a.Now()).Grades, nil
		}
		current := a.client.Profile()
		resolved := current.DefaultGradePeriod
		if a.opts.period != "" {
			resolved = ""
			for _, p := range current.Periods {
				if selected.Name != "" && p.Name == selected.Name && p.Kind == selected.Kind && p.Start.Equal(selected.Start) && p.End.Equal(selected.End) {
					resolved = p.ID
					break
				}
			}
			if resolved == "" {
				return gonote.GradeReport{}, fmt.Errorf("période introuvable après reconnexion : lance gonote periodes")
			}
		}
		return a.client.Grades(a.ctx, resolved)
	})
	if err == nil {
		for _, x := range result.Grades {
			if _, err = a.Store.Remember("n", x.ID, x, a.fetched); err != nil {
				return result, err
			}
		}
	}
	return result, err
}
func (a *App) emit(data any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"demo": a.opts.demo, "cached": a.cache, "fetched_at": a.fetched, "profile": a.profile, "data": data})
}
func (a *App) header(title, subtitle string) {
	a.ui.header(title, subtitle, a.profile, a.fetched, a.opts.demo, a.cache, a.opts.offline)
}
func (a *App) footer() { a.ui.footer("voir <réf>  détail", "--help  aide") }

func (a *App) homework() error {
	if err := a.prepare(); err != nil {
		return err
	}
	r, err := a.selection("upcoming")
	if err != nil {
		return err
	}
	items, err := a.getHomework(r)
	if err != nil {
		return err
	}
	out := []gonote.Assignment{}
	for _, x := range items {
		if !a.matches(x.Subject.Name) {
			continue
		}
		if a.opts.done && !x.Done {
			continue
		}
		if !a.opts.done && !a.opts.all && x.Done {
			continue
		}
		out = append(out, x)
	}
	if a.opts.json {
		return a.emit(out)
	}
	a.header("Mes devoirs", a.ui.rangeLabel(r))
	a.ui.homework(out, a.Now(), false)
	if !a.opts.tomorrow && !a.opts.yesterday && !a.opts.today && a.opts.on == "" && a.opts.week == "" {
		a.ui.note("Période : 14 jours passés et 30 jours à venir. --tout inclut les devoirs faits.")
	}
	a.ui.footer("fait <réf>  terminer", "a-faire <réf>  rouvrir", "voir <réf>  détail")
	return nil
}
func (a *App) lessons() error {
	if err := a.prepare(); err != nil {
		return err
	}
	r, err := a.selection("day")
	if err != nil {
		return err
	}
	items, err := a.getLessons(r)
	if err != nil {
		return err
	}
	out := []gonote.Lesson{}
	for _, x := range items {
		if a.matches(x.Subject.Name) {
			out = append(out, x)
		}
	}
	if a.opts.json {
		return a.emit(out)
	}
	a.header("Mes cours", a.ui.rangeLabel(r))
	a.ui.lessons(out)
	a.footer()
	return nil
}
func (a *App) timetable() error {
	if err := a.prepare(); err != nil {
		return err
	}
	r, err := a.selection("week")
	if err != nil {
		return err
	}
	items, err := a.getTimetable(r)
	if err != nil {
		return err
	}
	if !a.opts.canceled {
		items = gonote.FilterCanceledReplacements(items)
	}
	out := []gonote.TimetableEntry{}
	for _, x := range items {
		if a.matches(x.Subject.Name) {
			out = append(out, x)
		}
	}
	if a.opts.json {
		return a.emit(out)
	}
	a.header("Mon emploi du temps", a.ui.rangeLabel(r))
	a.ui.timetable(out, r, a.opts.agenda)
	a.footer()
	return nil
}
func (a *App) grades() error {
	result, err := a.getGrades()
	if err != nil {
		return err
	}
	out := []gonote.Grade{}
	var dates *gonote.DateRange
	if a.opts.on != "" || a.opts.tomorrow || a.opts.yesterday || a.opts.today || a.opts.week != "" {
		if a.opts.summary {
			return fmt.Errorf("les moyennes concernent toute la période : enlève l’option de date ou --resume")
		}
		r, err := a.selection("day")
		if err != nil {
			return err
		}
		dates = &r
	}
	for _, x := range result.Grades {
		if a.matches(x.Subject.Name) && (dates == nil || contains(x.Date, *dates)) {
			out = append(out, x)
		}
	}
	if !a.opts.all && len(out) > 20 {
		out = out[:20]
	}
	result.Grades = out
	subjects := []gonote.SubjectAverage{}
	for _, x := range result.Subjects {
		if a.matches(x.Subject.Name) {
			subjects = append(subjects, x)
		}
	}
	result.Subjects = subjects
	if a.opts.json {
		return a.emit(result)
	}
	a.header("Mes notes", result.Period.Name)
	a.ui.grades(result, a.opts.summary)
	a.footer()
	return nil
}
func (a *App) periods() error {
	if err := a.prepare(); err != nil {
		return err
	}
	if a.opts.json {
		return a.emit(a.profile.Periods)
	}
	a.header("Mes périodes", "Calendrier du collège")
	for _, p := range a.profile.Periods {
		fmt.Fprintf(a.Out, "  %s · %s → %s\n", plain(p.Name), p.Start.Format("02/01"), p.End.Format("02/01"))
	}
	return nil
}

func (a *App) overview() error {
	if err := a.prepare(); err != nil {
		return err
	}
	r, err := a.selection("day")
	if err != nil {
		return err
	}
	// Look ahead two weeks to find the next scheduled school day, including holidays.
	scheduleRange := gonote.DateRange{From: r.From, To: r.From.AddDate(0, 0, 14)}
	items, ttErr := a.getTimetable(scheduleRange)
	target := r.From
	found := false
	for _, x := range items {
		if !x.Canceled && !x.Start.Before(target) {
			target = midnight(x.Start)
			found = true
			break
		}
	}
	if a.opts.today || a.opts.tomorrow || a.opts.yesterday || a.opts.on != "" {
		target = r.From
		found = false
		for _, x := range items {
			if !x.Canceled && midnight(x.Start).Equal(target) {
				found = true
			}
		}
	}
	todayItems := []gonote.TimetableEntry{}
	for _, x := range gonote.FilterCanceledReplacements(items) {
		if midnight(x.Start).Equal(target) && a.matches(x.Subject.Name) {
			todayItems = append(todayItems, x)
		}
	}
	homework, hwErr := a.getHomework(gonote.DateRange{From: r.From.AddDate(0, 0, -14), To: target.AddDate(0, 0, 7)})
	assignments := []gonote.Assignment{}
	for _, x := range homework {
		if !x.Done && a.matches(x.Subject.Name) {
			assignments = append(assignments, x)
		}
	}
	report, grErr := a.getGrades()
	filteredGrades := []gonote.Grade{}
	for _, g := range report.Grades {
		if a.matches(g.Subject.Name) {
			filteredGrades = append(filteredGrades, g)
		}
	}
	report.Grades = filteredGrades
	if len(report.Grades) > 4 {
		report.Grades = report.Grades[:4]
	}
	if ttErr != nil && hwErr != nil && grErr != nil {
		return fmt.Errorf("impossible de charger la journée : %w", ttErr)
	}
	warnings := []string{}
	for _, e := range []error{ttErr, hwErr, grErr} {
		if e != nil {
			warnings = append(warnings, e.Error())
		}
	}
	if a.opts.json {
		return a.emit(map[string]any{"date": target, "timetable": todayItems, "homework": assignments, "grades": report.Grades, "warnings": warnings})
	}
	a.header("Ma journée", a.ui.dateLabel(r.From))
	a.ui.metrics(len(todayItems), len(assignments), len(report.Grades))
	for _, w := range warnings {
		a.ui.note(w)
	}
	if ttErr == nil {
		title := "Aujourd’hui"
		if !target.Equal(r.From) {
			title = "Prochaine journée · " + a.ui.dateLabel(target)
		}
		a.ui.section(title)
		if !found {
			a.ui.note("Aucun cours trouvé pour cette date ou les 14 prochains jours.")
		}
		a.ui.agenda(todayItems)
	}
	if hwErr == nil {
		a.ui.section("À faire")
		a.ui.homework(assignments, a.Now(), true)
	}
	if grErr == nil {
		a.ui.section("Dernières notes")
		a.ui.gradeList(report.Grades, true)
	}
	a.ui.footer("devoirs  tout ce qui est prévu", "edt  la semaine", "notes  les résultats")
	return nil
}

func (a *App) find(ref string) (store.Record, error) {
	if err := a.prepare(); err != nil {
		return store.Record{}, err
	}
	r, err := a.Store.Find(ref)
	if err != nil {
		return r, fmt.Errorf("référence inconnue : affiche d’abord la liste des devoirs, cours, notes ou de l’emploi du temps")
	}
	if a.opts.refresh {
		if err = a.refreshRecord(r); err != nil {
			return r, err
		}
		return a.Store.Find(ref)
	}
	a.fetched = r.Fetched
	a.cache = true
	return r, nil
}

func (a *App) refreshRecord(r store.Record) error {
	found := false
	switch r.Kind {
	case "h":
		var x gonote.Assignment
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return err
		}
		items, err := a.getHomework(gonote.DateRange{From: midnight(x.Due), To: midnight(x.Due)})
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == x.ID {
				found = true
			}
		}
	case "l":
		var x gonote.Lesson
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return err
		}
		items, err := a.getLessons(gonote.DateRange{From: midnight(x.Start), To: midnight(x.Start)})
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == x.ID {
				found = true
			}
		}
	case "t":
		var x gonote.TimetableEntry
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return err
		}
		items, err := a.getTimetable(gonote.DateRange{From: midnight(x.Start), To: midnight(x.Start)})
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == x.ID {
				found = true
			}
		}
	case "n":
		var x gonote.Grade
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return err
		}
		a.opts.period = x.PeriodID
		report, err := a.getGrades()
		if err != nil {
			return err
		}
		for _, item := range report.Grades {
			if item.ID == x.ID {
				found = true
			}
		}
	}
	if !found {
		return fmt.Errorf("cet élément n’est plus disponible dans PRONOTE")
	}
	return nil
}
func (a *App) show(args []string) error {
	r, err := a.find(args[0])
	if err != nil {
		return err
	}
	if a.opts.json {
		return a.emit(r)
	}
	var buf bytes.Buffer
	ui := *a.ui
	ui.out = &buf
	ui.header("En détail", r.Ref, a.profile, a.fetched, a.opts.demo, a.cache, a.opts.offline)
	if err = ui.detail(r); err != nil {
		return err
	}
	return a.page(buf.Bytes())
}

func (a *App) page(content []byte) error {
	if !a.opts.noPager {
		if f, ok := a.Out.(*os.File); ok && term.IsTerminal(f.Fd()) {
			_, height, err := term.GetSize(f.Fd())
			pager, lookErr := exec.LookPath("less")
			if err == nil && lookErr == nil && bytes.Count(content, []byte("\n")) > height {
				cmd := exec.CommandContext(a.ctx, pager, "-R", "-F", "-X")
				cmd.Stdin = bytes.NewReader(content)
				cmd.Stdout = a.Out
				cmd.Stderr = a.Err
				return cmd.Run()
			}
		}
	}
	_, err := a.Out.Write(content)
	return err
}

func frenchKeys() *huh.KeyMap {
	k := huh.NewDefaultKeyMap()
	k.Quit.SetHelp("ctrl+c", "annuler")
	k.Input.Prev.SetHelp("maj+tab", "précédent")
	k.Input.Next.SetHelp("entrée", "suivant")
	k.Input.Submit.SetHelp("entrée", "connexion")
	k.Input.AcceptSuggestion.SetHelp("tab", "compléter")
	return k
}
func attachments(r store.Record) ([]gonote.Attachment, error) {
	switch r.Kind {
	case "h":
		var x gonote.Assignment
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return nil, err
		}
		return x.Attachments, nil
	case "l":
		var x gonote.Lesson
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return nil, err
		}
		out := []gonote.Attachment{}
		for _, c := range x.Contents {
			out = append(out, c.Attachments...)
		}
		return out, nil
	case "n":
		var x gonote.Grade
		if err := json.Unmarshal(r.Data, &x); err != nil {
			return nil, err
		}
		return x.Attachments, nil
	}
	return []gonote.Attachment{}, nil
}
func (a *App) files(args []string) error {
	r, err := a.find(args[0])
	if err != nil {
		return err
	}
	files, err := attachments(r)
	if err != nil {
		return err
	}
	if a.opts.json {
		return a.emit(files)
	}
	a.header("Pièces jointes", r.Ref)
	a.ui.files(files)
	return nil
}
func (a *App) download(args []string) error {
	if a.opts.demo {
		return fmt.Errorf("les pièces jointes du compte fictif n’existent pas")
	}
	if a.opts.offline {
		return fmt.Errorf("le téléchargement nécessite une connexion")
	}
	r, err := a.find(args[0])
	if err != nil {
		return err
	}
	files, err := attachments(r)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("aucune pièce jointe")
	}
	index := 1
	if len(args) == 2 {
		if _, err = fmt.Sscanf(args[1], "%d", &index); err != nil {
			return fmt.Errorf("numéro de pièce jointe invalide")
		}
	} else if len(files) > 1 {
		return fmt.Errorf("choisis un numéro avec gonote fichiers %s", r.Ref)
	}
	if index < 1 || index > len(files) {
		return fmt.Errorf("numéro de pièce jointe invalide")
	}
	if err = a.connect(); err != nil {
		return err
	}
	file := files[index-1]
	dest := a.opts.output
	if dest == "" {
		dest = filepath.Base(strings.ReplaceAll(file.Name, "\\", "/"))
		if dest == "." || dest == "/" || dest == "" {
			return fmt.Errorf("choisis un nom de fichier avec --vers")
		}
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("impossible de créer le fichier (aucun fichier existant ne sera écrasé)")
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(dest)
		}
	}()
	if err = a.withSession(func() error { return a.client.Download(a.ctx, file, f) }, false); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	if a.opts.json {
		return a.emit(map[string]string{"path": dest})
	}
	fmt.Fprintln(a.Out, a.ui.success("Enregistré · "+plain(dest)))
	return nil
}

// FriendlyError avoids printing server responses, credentials or sensitive URLs.
func FriendlyError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, huh.ErrUserAborted) {
		return "Opération annulée."
	}
	s := err.Error()
	s = strings.ReplaceAll(s, "unknown command", "commande inconnue")
	s = strings.ReplaceAll(s, "unknown flag", "option inconnue")
	return plain(s)
}
