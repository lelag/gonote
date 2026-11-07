package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"golang.org/x/net/html"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/store"
)

type renderer struct {
	out   io.Writer
	width int
	color bool
}

func newRenderer(out io.Writer, o options) *renderer {
	w := o.width
	tty := false
	if f, ok := out.(*os.File); ok {
		tty = term.IsTerminal(f.Fd())
		if w == 0 {
			if cols, _, err := term.GetSize(f.Fd()); err == nil {
				w = cols
			}
		}
	}
	if w == 0 {
		w = 92
	}
	if w > 140 {
		w = 140
	}
	if w < 24 {
		w = 24
	}
	return &renderer{out: out, width: w, color: !o.noColor && os.Getenv("NO_COLOR") == "" && (tty || o.color)}
}
func (r *renderer) style(color string, bold bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if r.color {
		s = s.Foreground(lipgloss.Color(color)).Bold(bold)
	}
	return s
}
func (r *renderer) accent(s string) string  { return r.style("#B4A1FF", true).Render(s) }
func (r *renderer) muted(s string) string   { return r.style("#949CB0", false).Render(s) }
func (r *renderer) success(s string) string { return r.style("#7DD3AF", true).Render(s) }
func (r *renderer) warn(s string) string    { return r.style("#F2C879", true).Render(s) }
func (r *renderer) text(s string) string    { return r.style("#E6E9F2", false).Render(s) }
func (r *renderer) line() {
	fmt.Fprintln(r.out, "  "+r.muted(strings.Repeat("─", max(1, r.width-4))))
}
func (r *renderer) print(s string) { fmt.Fprintln(r.out, s) }
func (r *renderer) note(s string)  { r.print(indent(r.muted(wrap(terminalSafe(s), r.width-4)), 2)) }
func (r *renderer) header(title, subtitle string, p gonote.Profile, fetched time.Time, demo, cached, offline bool) {
	r.print("")
	badge := "ESPACE ÉLÈVE"
	if demo {
		badge = "DÉMO · " + badge
	}
	brand := r.accent("◈  gonote")
	if r.width >= 52 {
		r.print("  " + brand + strings.Repeat(" ", max(2, r.width-4-lipgloss.Width(brand)-lipgloss.Width(badge))) + r.muted(badge))
	} else {
		r.print("  " + brand)
		r.note(badge)
	}
	r.print("")
	r.print("  " + r.style("#F3F0FF", true).Render(wrap(title, r.width-4)))
	if subtitle != "" {
		r.note(subtitle)
	}
	if p.Student.Name != "" {
		identity := plain(p.Student.Name)
		if p.Class != "" {
			identity += " · " + plain(p.Class)
		}
		if p.School != "" && r.width >= 65 {
			identity += " · " + plain(p.School)
		}
		r.print("  " + r.text(wrap(identity, r.width-4)))
	}
	if !fetched.IsZero() {
		label := "Mis à jour"
		if cached {
			label = "Enregistré"
		}
		if offline {
			label = "Hors ligne · enregistré"
		}
		r.note(label + " le " + fetched.Format("02/01 à 15:04"))
	}
	r.print("")
	r.line()
	r.print("")
}
func (r *renderer) section(title string) {
	r.print("")
	r.print(indent(r.accent(wrap(strings.ToUpper(plain(title)), r.width-4)), 2))
	r.print("")
}
func (r *renderer) footer(parts ...string) {
	r.print("")
	r.line()
	r.note(strings.Join(parts, "   ·   "))
	r.print("")
}
func (r *renderer) metrics(lessons, homework, grades int) {
	if r.width < 64 {
		r.note(fmt.Sprintf("%d cours · %d devoirs à faire · %d notes récentes", lessons, homework, grades))
		return
	}
	w := (r.width - 6) / 3
	card := func(n int, label, color string) string {
		s := r.style(color, true).Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(w)
		if r.color {
			s = s.BorderForeground(lipgloss.Color("#3D4358"))
		}
		return s.Render(fmt.Sprintf("%d  %s", n, label))
	}
	r.print(indent(lipgloss.JoinHorizontal(lipgloss.Top, card(lessons, "cours", "#B4A1FF"), " ", card(homework, "à faire", "#F2C879"), " ", card(grades, "notes", "#7DD3AF")), 2))
}

var weekdays = []string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
var months = []string{"", "janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}

func (r *renderer) dateLabel(t time.Time) string {
	return fmt.Sprintf("%s %d %s", weekdays[t.Weekday()], t.Day(), months[t.Month()])
}
func (r *renderer) rangeLabel(d gonote.DateRange) string {
	if d.From.Equal(d.To) {
		return r.dateLabel(d.From)
	}
	return r.dateLabel(d.From) + " → " + r.dateLabel(d.To)
}
func wrap(s string, w int) string { return ansi.Wrap(s, max(1, w), "") }
func indent(s string, n int) string {
	padding := strings.Repeat(" ", n)
	return padding + strings.ReplaceAll(s, "\n", "\n"+padding)
}
func brief(s string, n int) string {
	s = strings.Join(strings.Fields(plain(s)), " ")
	return ansi.Truncate(s, n, "…")
}
func subject(s string) string {
	if s == "" {
		return "Matière non précisée"
	}
	return plain(s)
}
func (r *renderer) box(body string, color string) string {
	s := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).PaddingLeft(2).Width(r.width - 5)
	if r.color {
		s = s.BorderForeground(lipgloss.Color(color))
	}
	return indent(s.Render(body), 2)
}

func (r *renderer) homework(items []gonote.Assignment, now time.Time, compact bool) {
	if len(items) == 0 {
		r.note("Aucun devoir à afficher pour cette période et ces filtres.")
		return
	}
	last := ""
	for i, x := range items {
		if compact && i >= 5 {
			r.note(fmt.Sprintf("… et %d autres · gonote devoirs", len(items)-i))
			break
		}
		group := r.dateLabel(x.Due)
		if group != last {
			label := "Pour " + group
			if midnight(x.Due).Before(midnight(now)) && !x.Done {
				label += " · échéance passée, non marqué fait"
			}
			r.print("  " + r.warn(wrap(label, r.width-4)))
			r.print("")
			last = group
		}
		state := "○ À faire"
		color := "#B4A1FF"
		if x.Done {
			state = "✓ Fait"
			color = "#7DD3AF"
		}
		ref := store.Reference("h", x.ID)
		body := r.style(color, true).Render(subject(x.Subject.Name)) + "  " + r.muted(state) + "\n" + r.text(wrap(brief(x.Description, 180), r.width-10))
		meta := []string{ref}
		if x.DurationMinutes > 0 {
			meta = append(meta, fmt.Sprintf("%d min", x.DurationMinutes))
		}
		if x.Submission == 2 {
			meta = append(meta, "fichier à rendre")
		}
		if x.Submission == 4 {
			meta = append(meta, "audio à rendre")
		}
		if x.Submitted != nil {
			meta = append(meta, "rendu déposé")
		}
		if len(x.Attachments) > 0 {
			label := "1 pièce jointe"
			if len(x.Attachments) > 1 {
				label = fmt.Sprintf("%d pièces jointes", len(x.Attachments))
			}
			meta = append(meta, label)
		}
		body += "\n" + r.muted(wrap(strings.Join(meta, " · "), r.width-10))
		r.print(r.box(body, color))
		r.print("")
	}
}
func (r *renderer) lessons(items []gonote.Lesson) {
	if len(items) == 0 {
		r.note("Aucun contenu de cours publié pour cette période et ces filtres.")
		return
	}
	for _, x := range items {
		body := r.accent(subject(x.Subject.Name)) + "\n" + r.muted(r.dateLabel(x.Start)+" · "+x.Start.Format("15:04"))
		for _, co := range x.Contents {
			if co.Title != "" {
				body += "\n" + r.text(plain(co.Title))
			}
			if co.Description != "" {
				body += "\n" + r.text(wrap(brief(co.Description, 180), r.width-10))
			}
		}
		body += "\n" + r.muted(store.Reference("l", x.ID)+" · "+strings.Join(x.Teachers, ", "))
		r.print(r.box(body, "#B4A1FF"))
		r.print("")
	}
}
func (r *renderer) agenda(items []gonote.TimetableEntry) {
	if len(items) == 0 {
		r.note("Aucun cours prévu pour cette période et ces filtres.")
		return
	}
	last := ""
	for _, x := range items {
		date := r.dateLabel(x.Start)
		if date != last {
			r.print("  " + r.accent(date))
			r.print("")
			last = date
		}
		title := subject(x.Subject.Name)
		if x.Canceled {
			title += " · ANNULÉ"
		}
		if r.width < 40 {
			r.print("  " + r.muted(x.Start.Format("15:04")+"–"+x.End.Format("15:04")))
			r.print(indent(r.text(wrap(title, r.width-4)), 2))
			if len(x.Rooms) > 0 {
				r.note(strings.Join(x.Rooms, ", "))
			}
			if x.Status != "" {
				r.note(plain(x.Status))
			}
			r.note(store.Reference("t", x.ID))
			r.print("")
			continue
		}
		r.print("  " + r.muted(x.Start.Format("15:04")+"–"+x.End.Format("15:04")) + "  " + r.text(strings.ReplaceAll(wrap(title, r.width-19), "\n", "\n"+strings.Repeat(" ", 15))))
		meta := strings.Join(x.Rooms, ", ")
		if x.Status != "" {
			if meta != "" {
				meta += " · "
			}
			meta += plain(x.Status)
		}
		if meta != "" {
			r.print(indent(r.warn(wrap(meta, r.width-19)), 15))
		}
		r.print(indent(r.muted(store.Reference("t", x.ID)), 15))
		r.print("")
	}
}
func (r *renderer) timetable(items []gonote.TimetableEntry, d gonote.DateRange, agenda bool) {
	if agenda || r.width < 100 || d.From.Equal(d.To) || len(items) == 0 {
		r.agenda(items)
		return
	}
	days := []time.Time{}
	for day := d.From; !day.After(d.To); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			days = append(days, day)
		} else {
			for _, x := range items {
				if midnight(x.Start).Equal(day) {
					days = append(days, day)
					break
				}
			}
		}
	}
	if len(days) == 0 || len(days) > 7 {
		r.agenda(items)
		return
	}
	w := (r.width - 4 - (len(days) - 1)) / len(days)
	if w < 14 {
		r.agenda(items)
		return
	}
	columns := []string{}
	height := 0
	for _, day := range days {
		lines := []string{r.accent(strings.ToUpper(weekdays[day.Weekday()])), r.muted(day.Format("02/01")), ""}
		for _, x := range items {
			if !midnight(x.Start).Equal(day) {
				continue
			}
			lines = append(lines, r.muted(x.Start.Format("15:04")+"–"+x.End.Format("15:04")), r.text(wrap(subject(x.Subject.Name), w-4)))
			if x.Canceled {
				lines = append(lines, r.warn("ANNULÉ"))
			} else if x.Status != "" {
				lines = append(lines, r.warn(wrap(plain(x.Status), w-4)))
			}
			if len(x.Rooms) > 0 {
				lines = append(lines, r.muted(wrap(strings.Join(x.Rooms, ", "), w-4)))
			}
			lines = append(lines, r.muted(store.Reference("t", x.ID)), "")
		}
		if len(lines) == 3 {
			lines = append(lines, r.muted("Pas de cours"))
		}
		body := strings.Join(lines, "\n")
		height = max(height, lipgloss.Height(body))
		columns = append(columns, body)
	}
	styles := make([]lipgloss.Style, len(columns))
	height = 0
	for i, body := range columns {
		s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(w).Height(height).Padding(0, 1)
		if r.color {
			s = s.BorderForeground(lipgloss.Color("#3D4358"))
		}
		styles[i] = s.UnsetHeight()
		height = max(height, lipgloss.Height(styles[i].Render(body))-2)
	}
	joined := make([]string, 0, len(columns)*2)
	for i, body := range columns {
		if i > 0 {
			joined = append(joined, " ")
		}
		joined = append(joined, styles[i].Height(height+2).Render(body))
	}
	r.print(indent(lipgloss.JoinHorizontal(lipgloss.Top, joined...), 2))
}
func gradeText(v gonote.GradeValue) string {
	if v.Status == 0 && v.Points != nil {
		return strings.ReplaceAll(fmt.Sprintf("%g", *v.Points), ".", ",")
	}
	labels := map[int]string{1: "Absent", 2: "Dispensé", 3: "Non noté", 4: "Inapte", 5: "Non rendu", 6: "Absent (zéro)", 7: "Non rendu (zéro)", 8: "Félicitations"}
	if s, ok := labels[v.Status]; ok {
		return s
	}
	return "Non disponible"
}
func gradeFraction(v, out gonote.GradeValue) string {
	if v.Status != 0 || v.Points == nil {
		return gradeText(v)
	}
	if out.Points == nil {
		return gradeText(v)
	}
	return gradeText(v) + "/" + gradeText(out)
}
func (r *renderer) gradeList(items []gonote.Grade, compact bool) {
	if len(items) == 0 {
		r.note("Aucune note publiée pour cette période et ces filtres.")
		return
	}
	for _, x := range items {
		value := gradeFraction(x.Value, x.OutOf)
		line := r.success(value) + "  " + r.text(subject(x.Subject.Name))
		if r.width < 48 {
			line = r.text(subject(x.Subject.Name)) + "\n" + r.success(value)
		}
		body := line + "\n" + r.muted(wrap(plain(x.Comment), r.width-10))
		meta := x.Date.Format("02/01") + " · " + store.Reference("n", x.ID)
		if !compact {
			meta += fmt.Sprintf(" · coef. %g", x.Coefficient)
			if x.Bonus {
				meta += " · bonus"
			}
			if x.Optional {
				meta += " · facultative"
			}
		}
		body += "\n" + r.muted(wrap(meta, r.width-10))
		r.print(r.box(body, "#7DD3AF"))
		r.print("")
	}
}
func (r *renderer) grades(report gonote.GradeReport, summary bool) {
	if !summary {
		r.gradeList(report.Grades, false)
		return
	}
	if report.Average != nil {
		r.print("  " + r.success("Moyenne générale · "+gradeFraction(*report.Average, report.OutOf)))
	} else {
		r.note("Moyenne générale non communiquée par PRONOTE.")
	}
	if report.ClassAverage != nil {
		r.note("Moyenne de classe · " + gradeFraction(*report.ClassAverage, report.OutOf))
	}
	r.print("")
	for _, s := range report.Subjects {
		value := "Non communiquée"
		if s.Student != nil {
			value = gradeFraction(*s.Student, s.OutOf)
		}
		r.print("  " + r.text(subject(s.Subject.Name)) + " · " + r.success(value))
		if s.Class != nil {
			r.note("Classe : " + gradeFraction(*s.Class, s.OutOf))
		}
		r.print("")
	}
	r.note("Moyennes fournies par PRONOTE, sans recalcul local.")
}
func (r *renderer) files(files []gonote.Attachment) {
	if len(files) == 0 {
		r.note("Aucune pièce jointe.")
		return
	}
	for i, f := range files {
		r.print("  " + r.accent(fmt.Sprintf("%d", i+1)) + "  " + r.text(wrap(plain(f.Name), r.width-8)))
		if f.URL != "" {
			r.note("Lien : " + plain(f.URL))
		}
	}
}
func (r *renderer) detail(record store.Record) error {
	switch record.Kind {
	case "h":
		var x gonote.Assignment
		if err := json.Unmarshal(record.Data, &x); err != nil {
			return err
		}
		r.print("  " + r.accent(subject(x.Subject.Name)))
		r.note("Pour " + r.dateLabel(x.Due))
		if x.Done {
			r.note("✓ Marqué fait dans PRONOTE")
		} else {
			r.note("○ Non marqué fait dans PRONOTE")
		}
		r.print("")
		r.print(indent(wrap(plain(x.Description), r.width-4), 2))
		if x.DurationMinutes > 0 {
			r.print("")
			r.note(fmt.Sprintf("Durée estimée : %d min", x.DurationMinutes))
		}
		sub := map[int]string{0: "Aucun rendu demandé", 1: "Rendu sur papier", 2: "Fichier à rendre dans PRONOTE", 3: "Rendu via le kiosque", 4: "Enregistrement audio à rendre"}
		if s := sub[x.Submission]; s != "" {
			r.note(s)
		}
		if x.Submitted != nil {
			r.note("Rendu déposé : " + plain(x.Submitted.Name))
		}
		if x.LessonID != "" {
			r.note("Cours associé : " + store.Reference("l", x.LessonID) + " (après consultation de gonote cours)")
		}
		r.section("Pièces jointes")
		r.files(x.Attachments)
	case "l":
		var x gonote.Lesson
		if err := json.Unmarshal(record.Data, &x); err != nil {
			return err
		}
		r.print("  " + r.accent(subject(x.Subject.Name)))
		r.note(r.dateLabel(x.Start) + " · " + x.Start.Format("15:04"))
		r.note(strings.Join(x.Teachers, ", "))
		for _, co := range x.Contents {
			r.section(co.Title)
			r.note(co.Category)
			r.print(indent(wrap(plain(co.Description), r.width-4), 2))
			if len(co.Attachments) > 0 {
				r.print("")
				r.files(co.Attachments)
			}
		}
	case "n":
		var x gonote.Grade
		if err := json.Unmarshal(record.Data, &x); err != nil {
			return err
		}
		r.gradeList([]gonote.Grade{x}, false)
		if x.Feedback != "" {
			r.section("Appréciation")
			r.print(indent(wrap(plain(x.Feedback), r.width-4), 2))
		}
		if x.Average != nil {
			r.note("Moyenne de classe : " + gradeFraction(*x.Average, x.OutOf))
		}
		if len(x.Attachments) > 0 {
			r.section("Pièces jointes")
			r.files(x.Attachments)
		}
	case "t":
		var x gonote.TimetableEntry
		if err := json.Unmarshal(record.Data, &x); err != nil {
			return err
		}
		r.agenda([]gonote.TimetableEntry{x})
		r.note("Enseignant(s) : " + strings.Join(x.Teachers, ", "))
		if len(x.Groups) > 0 {
			r.note("Groupe(s) : " + strings.Join(x.Groups, ", "))
		}
		if x.LessonID != "" {
			r.note("Contenu du cours : " + store.Reference("l", x.LessonID))
		}
	default:
		return fmt.Errorf("type de détail inconnu")
	}
	r.footer("fichiers "+record.Ref, "telecharger "+record.Ref+" 1")
	return nil
}
func (r *renderer) help(cmd *cobra.Command) {
	r.header("Le collège, à portée de clavier.", "Devoirs · cours · notes · emploi du temps", gonote.Profile{}, time.Time{}, false, false, false)
	r.print("  " + r.accent("UTILISATION"))
	r.print("  " + cmd.UseLine())
	r.print("")
	if cmd.Parent() != nil {
		r.note(cmd.Short)
		r.print("")
	}
	if cmd.HasSubCommands() {
		r.print("  " + r.accent("TES COMMANDES"))
		commands := cmd.Commands()
		sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name() < commands[j].Name() })
		for _, c := range commands {
			if c.Hidden {
				continue
			}
			r.print("  " + r.text(c.Name()))
			r.print(indent(r.muted(wrap(c.Short, r.width-8)), 6))
		}
		r.print("")
	}
	r.print("  " + r.accent("OPTIONS"))
	r.print(indent(strings.TrimSpace(cmd.NonInheritedFlags().FlagUsagesWrapped(max(24, r.width-6))), 2))
	if cmd.HasInheritedFlags() {
		r.print(indent(strings.TrimSpace(cmd.InheritedFlags().FlagUsagesWrapped(max(24, r.width-6))), 2))
	}
	r.section("Pour commencer")
	r.note("gonote --demo")
	r.note("gonote connexion")
	r.note("gonote devoirs --demain")
	r.note("gonote edt --semaine prochaine")
	r.note("gonote notes --resume")
	r.footer("gonote anglais  les alias anglais", "fait <réf>  terminer un devoir")
}

// plain converts teacher-authored HTML into safe terminal text. Script/style
// contents, escape sequences, C0/C1 controls and bidi controls are removed.
func plain(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var out strings.Builder
	skip := 0
	links := []string{}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		token := z.Token()
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name := strings.ToLower(token.Data)
			if name == "script" || name == "style" {
				skip++
				continue
			}
			if skip > 0 {
				continue
			}
			if name == "a" {
				href := ""
				for _, attr := range token.Attr {
					if attr.Key == "href" && (strings.HasPrefix(attr.Val, "https://") || strings.HasPrefix(attr.Val, "http://")) {
						href = attr.Val
					}
				}
				links = append(links, href)
			}
			if name == "img" {
				alt := "Image dans le document original"
				for _, attr := range token.Attr {
					if attr.Key == "alt" && attr.Val != "" {
						alt = "Image : " + attr.Val
					}
				}
				out.WriteString("[" + alt + "]")
			}
			switch name {
			case "br", "p", "div", "ul", "ol", "tr", "h1", "h2", "h3":
				out.WriteByte('\n')
			case "li":
				out.WriteString("\n• ")
			}
		case html.EndTagToken:
			name := strings.ToLower(token.Data)
			if name == "script" || name == "style" {
				if skip > 0 {
					skip--
				}
				continue
			}
			if skip > 0 {
				continue
			}
			if name == "a" && len(links) > 0 {
				href := links[len(links)-1]
				links = links[:len(links)-1]
				if href != "" {
					out.WriteString(" (" + href + ")")
				}
			}
			switch name {
			case "p", "div", "li", "tr", "h1", "h2", "h3":
				out.WriteByte('\n')
			}
		case html.TextToken:
			if skip == 0 {
				out.WriteString(token.Data)
			}
		}
	}
	s = terminalSafe(out.String())
	lines := []string{}
	lastEmpty := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !lastEmpty {
				lines = append(lines, "")
			}
			lastEmpty = true
		} else {
			lines = append(lines, line)
			lastEmpty = false
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func terminalSafe(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(c rune) rune {
		if c == '\n' || c == '\t' {
			return c
		}
		if unicode.IsControl(c) || c >= 0x202a && c <= 0x202e || c >= 0x2066 && c <= 0x2069 {
			return -1
		}
		return c
	}, s)
}
