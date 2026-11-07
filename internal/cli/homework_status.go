package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/store"
)

func (a *App) setHomeworkDone(ref string, done bool) error {
	if a.opts.offline {
		return fmt.Errorf("modifier un devoir nécessite une connexion ; retire --hors-ligne")
	}
	if a.opts.demo {
		return fmt.Errorf("le mode démo ne modifie aucun devoir ; utilise un compte configuré")
	}
	if err := a.prepare(); err != nil {
		return err
	}
	record, err := a.Store.Find(ref)
	if err != nil {
		return fmt.Errorf("devoir introuvable : lance gonote devoirs --tout et utilise une référence h-…")
	}
	if record.Kind != "h" {
		return fmt.Errorf("cette référence n’est pas un devoir ; utilise une référence h-…")
	}
	var previous gonote.Assignment
	if err = json.Unmarshal(record.Data, &previous); err != nil {
		return err
	}
	if previous.ID == "" || previous.Due.IsZero() || store.Reference("h", previous.ID) != ref {
		return fmt.Errorf("référence de devoir invalide : relance gonote devoirs --tout")
	}
	a.writes = true
	r := gonote.DateRange{From: midnight(previous.Due), To: midnight(previous.Due)}
	var items []gonote.Assignment
	read := func() error { var e error; items, e = a.client.Homework(a.ctx, r); return e }
	if err = a.withSession(read, true); err != nil {
		return err
	}
	find := func() (gonote.Assignment, error) {
		for _, x := range items {
			if x.ID == previous.ID {
				return x, nil
			}
		}
		return gonote.Assignment{}, fmt.Errorf("ce devoir n’est plus disponible : relance gonote devoirs --tout --actualiser")
	}
	current, err := find()
	if err != nil {
		return err
	}
	changed := current.Done != done
	// Clear every cached view before writing, including on a lost response. Never
	// show an optimistic success or retain a stale cached status after uncertainty.
	if err = a.Store.InvalidateHomework(ref); err != nil {
		return err
	}
	if changed {
		if err = a.withSession(func() error { return a.client.SetHomeworkDone(a.ctx, current.ID, done) }, false); err != nil {
			return fmt.Errorf("modification non confirmée : vérifie avec gonote devoirs --tout --actualiser avant de réessayer : %w", err)
		}
		if err = a.withSession(read, true); err != nil {
			return fmt.Errorf("modification envoyée, mais vérification impossible : lance gonote devoirs --tout --actualiser : %w", err)
		}
		current, err = find()
		if err != nil {
			return err
		}
		if current.Done != done {
			return fmt.Errorf("PRONOTE n’a pas confirmé le nouvel état : relance gonote devoirs --tout --actualiser")
		}
	}
	a.fetched = a.Now()
	a.cache = false
	b, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if err = a.Store.Save(rangeKey("homework", r), store.Envelope{Fetched: a.fetched, Profile: a.profile, Data: b}); err != nil {
		return fmt.Errorf("état confirmé sur PRONOTE, mais cache local non enregistré : %w", err)
	}
	if _, err = a.Store.Remember("h", current.ID, current, a.fetched); err != nil {
		return fmt.Errorf("état confirmé sur PRONOTE, mais détail local non enregistré : %w", err)
	}
	if a.opts.json {
		return a.emit(struct {
			Ref        string            `json:"ref"`
			Changed    bool              `json:"changed"`
			Assignment gonote.Assignment `json:"assignment"`
		}{ref, changed, current})
	}
	label := "À faire"
	undo := "fait"
	if done {
		label = "Fait"
		undo = "a-faire"
	}
	if !changed {
		label = "Déjà " + label
	}
	fmt.Fprintln(a.Out, a.ui.success("✓ "+label+" · "+ref+" · "+plain(current.Subject.Name)))
	a.ui.note("gonote " + undo + " " + ref + "  pour changer cet état")
	return nil
}
