package gonote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func (c *Client) checkTab(tab int) error {
	if len(c.tabs) > 0 && !c.tabs[tab] {
		return ErrAccessDenied
	}
	return nil
}
func (c *Client) dateRange(r DateRange) (DateRange, error) {
	if r.From.IsZero() || r.To.IsZero() {
		return r, fmt.Errorf("dates de début et de fin nécessaires")
	}
	r.From = day(r.From, c.loc)
	r.To = day(r.To, c.loc)
	if r.To.Before(r.From) || r.To.After(r.From.AddDate(1, 0, 0)) {
		return r, fmt.Errorf("intervalle de dates invalide (maximum un an)")
	}
	return r, nil
}
func day(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}
func inRange(t time.Time, r DateRange) bool {
	return !t.Before(r.From) && t.Before(r.To.AddDate(0, 0, 1))
}
func (c *Client) notebook(ctx context.Context, r DateRange, tab int) (node, DateRange, error) {
	if err := c.checkTab(tab); err != nil {
		return node{}, r, err
	}
	r, err := c.dateRange(r)
	if err != nil {
		return node{}, r, err
	}
	weeks := fmt.Sprintf("[%d..%d]", c.WeekNumber(r.From), c.WeekNumber(r.To))
	n, err := c.call(ctx, "PageCahierDeTexte", map[string]any{"domaine": map[string]any{"_T": 8, "V": weeks}, "sansRequeteRP": true}, map[string]any{"onglet": tab}, nil)
	return n, r, err
}

func (c *Client) Homework(ctx context.Context, r DateRange) ([]Assignment, error) {
	n, r, err := c.notebook(ctx, r, 88)
	if err != nil {
		return nil, err
	}
	items, err := decodeAssignments(n, c.loc)
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(items))
	for _, x := range items {
		if inRange(x.Due, r) {
			out = append(out, x)
		}
	}
	return out, nil
}

// SetHomeworkDone sets a student's homework status to an explicit value. It
// requires Options.AllowWrites. It does not upload work or submit an assignment.
// On transport failure the outcome may be unknown: read the homework status
// before deciding whether to repeat the operation.
func (c *Client) SetHomeworkDone(ctx context.Context, id string, done bool) error {
	if !c.allowWrites {
		return ErrReadOnly
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("identifiant de devoir nécessaire")
	}
	if err := c.checkTab(88); err != nil {
		return err
	}
	_, err := c.call(ctx, "SaisieTAFFaitEleve", map[string]any{"listeTAF": []any{map[string]any{"N": id, "E": 2, "TAFFait": done}}}, map[string]any{"onglet": 7}, nil)
	return err
}

func decodeAssignments(n node, loc *time.Location) ([]Assignment, error) {
	if !n.has("ListeTravauxAFaire") {
		return nil, ErrProtocol
	}
	out := []Assignment{}
	for _, x := range n.get("ListeTravauxAFaire").list() {
		due, err := x.get("PourLe").date(loc)
		if err != nil {
			return nil, err
		}
		a := Assignment{
			ID:              x.get("N").text(),
			Subject:         x.get("Matiere").resource(),
			Description:     x.get("descriptif").text(),
			Due:             due,
			Given:           optionalDate(x.get("DonneLe"), loc),
			Done:            x.get("TAFFait").boolean(),
			DurationMinutes: x.get("duree").integer(),
			Submission:      x.get("genreRendu").integer(),
			CanSubmit:       x.get("peuRendre").boolean(),
			LessonID:        x.get("cahierDeTextes").get("N").text(),
			Attachments:     decodeAttachments(x.get("ListePieceJointe")),
		}
		if x.get("documentRendu").get("N").text() != "" {
			v := decodeAttachment(x.get("documentRendu"))
			a.Submitted = &v
		}
		if a.ID == "" {
			return nil, ErrProtocol
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due.Equal(out[j].Due) {
			return out[i].Subject.Name < out[j].Subject.Name
		}
		return out[i].Due.Before(out[j].Due)
	})
	return out, nil
}

func (c *Client) Lessons(ctx context.Context, r DateRange) ([]Lesson, error) {
	n, r, err := c.notebook(ctx, r, 89)
	if err != nil {
		return nil, err
	}
	items, err := decodeLessons(n, c.loc)
	if err != nil {
		return nil, err
	}
	out := make([]Lesson, 0, len(items))
	for _, x := range items {
		if inRange(x.Start, r) {
			out = append(out, x)
		}
	}
	return out, nil
}
func decodeLessons(n node, loc *time.Location) ([]Lesson, error) {
	if !n.has("ListeCahierDeTextes") {
		return nil, ErrProtocol
	}
	out := []Lesson{}
	for _, x := range n.get("ListeCahierDeTextes").list() {
		start, err := x.get("Date").date(loc)
		if err != nil {
			return nil, err
		}
		l := Lesson{ID: x.get("N").text(), Subject: x.get("Matiere").resource(), Start: start, End: optionalDate(x.get("DateFin"), loc), Teachers: []string{}, Contents: []LessonContent{}}
		for _, p := range x.get("listeProfesseurs").list() {
			l.Teachers = append(l.Teachers, p.get("L").text())
		}
		for _, co := range x.get("listeContenus").list() {
			l.Contents = append(l.Contents, LessonContent{Title: co.get("L").text(), Category: co.get("categorie").get("L").text(), Description: co.get("descriptif").text(), Attachments: decodeAttachments(co.get("ListePieceJointe"))})
		}
		if l.ID == "" {
			return nil, ErrProtocol
		}
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (c *Client) Timetable(ctx context.Context, r DateRange) ([]TimetableEntry, error) {
	if err := c.checkTab(16); err != nil {
		return nil, err
	}
	r, err := c.dateRange(r)
	if err != nil {
		return nil, err
	}
	resource := map[string]any{"N": c.profile.Student.ID, "G": c.profile.Student.Kind, "L": c.profile.Student.Name}
	start, end := wireDate(r.From), wireDate(r.To.AddDate(0, 0, 1).Add(-time.Second))
	n, err := c.call(ctx, "PageEmploiDuTemps", map[string]any{
		"dateDebut": start, "DateDebut": start, "dateFin": end, "DateFin": end, "ressource": resource, "Ressource": resource,
		"avecAbsencesEleve": false, "avecAbsencesRessource": true, "avecConseilDeClasse": true, "avecCoursSortiePeda": true,
		"avecDisponibilites": true, "avecInfosPrefsGrille": true, "avecRessourcesLibrePiedHoraire": false, "avecRetenuesEleve": true,
		"estEDTAnnuel": false, "estEDTPermanence": false, "edt": map[string]any{"G": 16, "L": "Emploi du temps"},
	}, map[string]any{"onglet": 16}, nil)
	if err != nil {
		return nil, err
	}
	items, err := c.decodeTimetable(n)
	if err != nil {
		return nil, err
	}
	out := make([]TimetableEntry, 0, len(items))
	for _, x := range items {
		if inRange(x.Start, r) {
			out = append(out, x)
		}
	}
	return out, nil
}
func (c *Client) decodeTimetable(n node) ([]TimetableEntry, error) {
	if !n.has("ListeCours") {
		return nil, ErrProtocol
	}
	out := []TimetableEntry{}
	for _, x := range n.get("ListeCours").list() {
		start, err := x.get("DateDuCours").date(c.loc)
		if err != nil {
			return nil, err
		}
		e := TimetableEntry{
			ID:       x.get("N").text(),
			Start:    start,
			End:      start.Add(time.Duration(x.get("duree").number() * c.blockMinutes * float64(time.Minute))),
			Status:   x.get("Statut").text(),
			Canceled: x.get("estAnnule").boolean(),
			Kind:     "lesson",
			LessonID: x.get("cahierDeTextes").get("N").text(),
			Teachers: []string{},
			Rooms:    []string{},
			Groups:   []string{},
		}
		if x.has("dateDuCoursFin") {
			e.End, err = x.get("dateDuCoursFin").date(c.loc)
			if err != nil {
				return nil, err
			}
		} else if c.blocksPerDay > 0 && len(c.endings) > 0 {
			index := x.get("place").integer()%c.blocksPerDay + x.get("duree").integer() - 1
			if index >= 0 && index < len(c.endings) {
				var h, m int
				if _, er := fmt.Sscanf(c.endings[index], "%dh%d", &h, &m); er == nil {
					end := time.Date(start.Year(), start.Month(), start.Day(), h, m, 0, 0, c.loc)
					if end.After(start) {
						e.End = end
					}
				}
			}
		}
		if x.get("estRetenue").boolean() {
			e.Kind = "detention"
			e.Subject.Name = "Retenue"
		}
		if x.get("estSortiePedagogique").boolean() {
			e.Kind = "activity"
			e.Subject.Name = x.get("motif").text()
		}
		for _, co := range x.get("ListeContenus").list() {
			switch co.get("G").integer() {
			case 16:
				e.Subject = co.resource()
			case 3:
				e.Teachers = append(e.Teachers, co.get("L").text())
			case 17:
				e.Rooms = append(e.Rooms, co.get("L").text())
			case 2:
				e.Groups = append(e.Groups, co.get("L").text())
			}
		}
		if e.ID == "" {
			return nil, ErrProtocol
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// FilterCanceledReplacements hides a canceled lesson only when an active entry
// overlaps it. Raw entries remain available for an audit/detail view.
func FilterCanceledReplacements(entries []TimetableEntry) []TimetableEntry {
	out := make([]TimetableEntry, 0, len(entries))
	for _, e := range entries {
		hidden := false
		if e.Canceled {
			for _, other := range entries {
				if !other.Canceled && other.Start.Before(e.End) && e.Start.Before(other.End) {
					hidden = true
					break
				}
			}
		}
		if !hidden {
			out = append(out, e)
		}
	}
	return out
}

func (c *Client) Grades(ctx context.Context, periodID string) (GradeReport, error) {
	if err := c.checkTab(198); err != nil {
		return GradeReport{}, err
	}
	if periodID == "" {
		periodID = c.profile.DefaultGradePeriod
	}
	var period Period
	for _, p := range c.profile.Periods {
		if p.ID == periodID {
			period = p
			break
		}
	}
	if period.ID == "" {
		return GradeReport{}, fmt.Errorf("période de notes inconnue ; consultez les périodes disponibles")
	}
	kind := period.Kind
	n, err := c.call(ctx, "DernieresNotes", map[string]any{"Periode": map[string]any{"N": period.ID, "G": kind, "L": period.Name}}, map[string]any{"onglet": 198}, nil)
	if err != nil {
		return GradeReport{}, err
	}
	return decodeGrades(n, period, c.loc)
}
func decodeGrades(n node, p Period, loc *time.Location) (GradeReport, error) {
	if !n.has("listeDevoirs") || !n.has("listeServices") {
		return GradeReport{}, ErrProtocol
	}
	out := GradeReport{Period: p, Grades: []Grade{}, Subjects: []SubjectAverage{}, Average: optionalGrade(n.get("moyGenerale")), ClassAverage: optionalGrade(n.get("moyGeneraleClasse")), OutOf: gradeValue(first(n, "baremeMoyGenerale", "baremeMoyGeneraleParDefaut"))}
	for _, s := range n.get("listeServices").list() {
		out.Subjects = append(out.Subjects, SubjectAverage{Subject: s.resource(), Student: optionalGrade(s.get("moyEleve")), Class: optionalGrade(s.get("moyClasse")), OutOf: gradeValue(first(s, "baremeMoyEleve", "baremeMoyEleveParDefaut"))})
	}
	for _, x := range n.get("listeDevoirs").list() {
		d, err := x.get("date").date(loc)
		if err != nil {
			return GradeReport{}, err
		}
		g := Grade{
			PeriodID:    p.ID,
			ID:          x.get("N").text(),
			Subject:     x.get("service").resource(),
			Date:        d,
			Value:       gradeValue(x.get("note")),
			OutOf:       gradeValue(first(x, "bareme", "baremeParDefaut")),
			Coefficient: x.get("coefficient").number(),
			Comment:     x.get("commentaire").text(),
			Feedback:    x.get("commentaireSurNote").text(),
			Average:     optionalGrade(x.get("moyenne")),
			Bonus:       x.get("estBonus").boolean(),
			Optional:    x.get("estFacultatif").boolean(),
			Attachments: []Attachment{},
		}
		for _, f := range []struct{ key, category string }{{"libelleSujet", "DevoirSujet"}, {"libelleCorrige", "DevoirCorrige"}} {
			if name := x.get(f.key).text(); name != "" {
				g.Attachments = append(g.Attachments, Attachment{ID: g.ID, Name: name, Kind: 1, Category: f.category})
			}
		}
		if g.ID == "" {
			return GradeReport{}, ErrProtocol
		}
		out.Grades = append(out.Grades, g)
	}
	sort.SliceStable(out.Grades, func(i, j int) bool { return out.Grades[i].Date.After(out.Grades[j].Date) })
	return out, nil
}
func first(n node, keys ...string) node {
	for _, k := range keys {
		if v := n.get(k); v.v != nil && v.text() != "" {
			return v
		}
	}
	return node{}
}
func decodeAttachment(n node) Attachment {
	return Attachment{ID: n.get("N").text(), Name: n.get("L").text(), Kind: n.get("G").integer(), URL: n.get("url").text()}
}
func decodeAttachments(n node) []Attachment {
	out := []Attachment{}
	for _, x := range n.list() {
		out = append(out, decodeAttachment(x))
	}
	return out
}

// Download streams a PRONOTE file to dst. Links to external sites are never
// fetched. The caller chooses the local filename and owns overwrite policy.
func (c *Client) Download(ctx context.Context, a Attachment, dst io.Writer) error {
	if a.ID == "" || a.Name == "" || a.Kind == 0 || a.URL != "" {
		return fmt.Errorf("cette pièce jointe est un lien ou un format non pris en charge")
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if c.invalid {
		return ErrSessionExpired
	}
	// Field order matches the protocol's reference representation.
	fields := fmt.Sprintf(`{"N":%s,"Actif":true`, jsonString(a.ID))
	if a.Category != "" {
		fields += `,"G":` + jsonString(a.Category)
	}
	fields += "}"
	link := c.base.String() + "/FichiersExternes/" + c.cipher.encryptText(fields) + "/" + url.PathEscape(a.Name) + "?Session=" + url.QueryEscape(c.session)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	req.Header.Set("User-Agent", c.userAgent())
	if c.mobile {
		req.AddCookie(&http.Cookie{Name: "appliMobile", Value: "1"})
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ErrUnavailable
	}
	if strings.Contains(res.Header.Get("Content-Type"), "text/html") && !strings.HasSuffix(strings.ToLower(a.Name), ".html") {
		return ErrSessionExpired
	}
	n, err := io.Copy(dst, io.LimitReader(res.Body, 100<<20+1))
	if n > 100<<20 {
		return fmt.Errorf("pièce jointe trop volumineuse (maximum 100 Mio)")
	}
	return err
}
func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
