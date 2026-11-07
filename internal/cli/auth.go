package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/lelag/gonote"
	"github.com/lelag/gonote/internal/credentials"
)

type schoolClient interface {
	Profile() gonote.Profile
	Reconnect() gonote.Reconnect
	Snapshot(context.Context) (gonote.SessionState, error)
	Homework(context.Context, gonote.DateRange) ([]gonote.Assignment, error)
	SetHomeworkDone(context.Context, string, bool) error
	Lessons(context.Context, gonote.DateRange) ([]gonote.Lesson, error)
	Timetable(context.Context, gonote.DateRange) ([]gonote.TimetableEntry, error)
	Grades(context.Context, string) (gonote.GradeReport, error)
	Download(context.Context, gonote.Attachment, io.Writer) error
}

func (a *App) Close() error {
	if a.session != nil {
		err := a.session.Close()
		a.session = nil
		return err
	}
	return nil
}
func (a *App) lockSession() error {
	if a.session != nil {
		return nil
	}
	s, err := a.Store.LockSession(a.ctx, a.cfg)
	if err != nil {
		return err
	}
	a.session = s
	return nil
}
func (a *App) connect() error {
	if a.authErr != nil {
		return a.authErr
	}
	if a.client != nil {
		return nil
	}
	if a.opts.offline {
		return fmt.Errorf("aucune connexion en mode hors ligne")
	}
	if err := a.lockSession(); err != nil {
		return err
	}
	// Another command may have rotated a token while this process waited.
	if cfg, err := a.Store.LoadConfig(); err == nil && cfg.CredentialKey() == a.cfg.CredentialKey() {
		a.cfg = cfg
	}
	saved, err := a.session.Load()
	if err == nil && saved.State.URL == a.cfg.URL && saved.State.Provider == a.cfg.ENT {
		a.saved = saved
		if saved.Usable {
			c, e := a.restore(saved.State, gonote.Options{AllowWrites: a.writes})
			if e == nil {
				a.client = c
				a.profile = c.Profile()
				return nil
			}
		}
	}
	err = a.authenticate()
	if err != nil {
		a.authErr = err
	}
	return err
}
func (a *App) accept(c schoolClient) error {
	a.client = c
	a.profile = c.Profile()
	a.cfg.Profile = a.profile
	a.cfg.Auth = c.Reconnect()
	if err := a.Store.SaveConfig(a.cfg); err != nil {
		return err
	}
	return a.saveSession(true)
}
func (a *App) saveSession(usable bool) error {
	if a.session == nil {
		return fmt.Errorf("session non verrouillée")
	}
	if usable {
		state, err := a.client.Snapshot(a.ctx)
		if err != nil {
			return err
		}
		a.saved.State = state
	}
	a.saved.Usable = usable
	if err := a.session.Save(a.saved); err != nil {
		return fmt.Errorf("impossible de mémoriser la session : %w", err)
	}
	return nil
}
func (a *App) authenticate() error {
	opts := gonote.Options{AllowWrites: a.writes, Location: a.Now().Location(), ENT: a.cfg.ENT}
	// Reuse the browser-like CAS/IdP session before considering any password.
	if a.cfg.ENT != "" && len(a.saved.State.Cookies) > 0 {
		opts.SSOCookies = a.saved.State.Cookies
		c, err := a.loginClient(a.ctx, a.cfg.URL, gonote.Credentials{Username: a.cfg.Username()}, opts)
		if err == nil {
			return a.accept(c)
		}
		if !errors.Is(err, gonote.ErrCredentialsRequired) && !errors.Is(err, gonote.ErrCredentials) && !errors.Is(err, gonote.ErrSessionExpired) {
			return err
		}
	}
	opts.SSOCookies = nil
	cred := a.credentials()
	if cred.Token != "" {
		c, err := a.loginClient(a.ctx, a.cfg.URL, cred, opts)
		if err == nil {
			return a.accept(c)
		}
		if !errors.Is(err, gonote.ErrCredentials) && !errors.Is(err, gonote.ErrSessionExpired) {
			return err
		}
		a.cfg.Auth.Token = ""
	}
	cred = gonote.Credentials{Username: a.cfg.Username(), DeviceID: a.cfg.Auth.DeviceID, NavigatorID: a.cfg.Auth.NavigatorID}
	cred.Password = os.Getenv("GONOTE_PASSWORD")
	if cred.Password == "" {
		cred.Password = os.Getenv("PRONOTE_PASSWORD")
	}
	remembered := false
	if cred.Password == "" && a.cfg.PasswordStorage != "" {
		var err error
		cred.Password, err = a.Vault.Get(a.cfg.PasswordStorage, a.cfg.CredentialKey())
		if err != nil && !errors.Is(err, credentials.ErrNotFound) {
			return err
		}
		remembered = err == nil
	}
	if cred.Password == "" {
		if !a.interactive() {
			return fmt.Errorf("session expirée : lance gonote connexion ou définis GONOTE_PASSWORD ; --memoriser permet la reconnexion automatique")
		}
		if err := a.password(&cred.Password); err != nil {
			return err
		}
	}
	c, err := a.loginClient(a.ctx, a.cfg.URL, cred, opts)
	if errors.Is(err, gonote.ErrCredentials) && remembered {
		return fmt.Errorf("mot de passe mémorisé refusé : mets-le à jour avec gonote mot-de-passe")
	}
	if err != nil {
		return err
	}
	return a.accept(c)
}

// Each operation is bracketed by a durable dirty marker and an updated snapshot.
// Only one expired-session retry is allowed per command; never retry a password
// rejected by the provider, a mutation, or a partially completed download.
func (a *App) withSession(read func() error, retry bool) error {
	if err := a.connect(); err != nil {
		return err
	}
	run := func() error {
		if err := a.saveSession(false); err != nil {
			return err
		}
		if err := read(); err != nil {
			return err
		}
		return a.saveSession(true)
	}
	err := run()
	if retry && !a.renewed && errors.Is(err, gonote.ErrSessionExpired) {
		a.renewed = true
		a.client = nil
		if err = a.authenticate(); err != nil {
			a.authErr = err
			return err
		}
		err = run()
	}
	if err != nil {
		a.authErr = err
	}
	return err
}

func (a *App) forgetPassword() error {
	if a.opts.demo {
		return fmt.Errorf("le mode démo n’a pas de mot de passe")
	}
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.lockSession(); err != nil {
		return err
	}
	if err := a.Vault.Delete(a.cfg.PasswordStorage, a.cfg.CredentialKey()); err != nil {
		return err
	}
	a.cfg.PasswordStorage = ""
	if err := a.Store.SaveConfig(a.cfg); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Mot de passe local supprimé. La session actuelle reste disponible.")
	return nil
}
func (a *App) updatePassword() error {
	if a.opts.demo || a.opts.offline {
		return fmt.Errorf("cette commande nécessite un compte et le réseau")
	}
	if !a.interactive() {
		return fmt.Errorf("lance gonote mot-de-passe dans un terminal interactif")
	}
	if err := a.prepare(); err != nil {
		return err
	}
	mode := a.opts.storage
	if mode == "" {
		mode = a.cfg.PasswordStorage
	}
	if mode == "" {
		mode = credentials.Keyring
	}
	if !credentials.ValidMode(mode) {
		return fmt.Errorf("stockage inconnu : trousseau ou fichier")
	}
	var password string
	if err := a.password(&password); err != nil {
		return err
	}
	if err := a.lockSession(); err != nil {
		return err
	}
	c, err := a.loginClient(a.ctx, a.cfg.URL, gonote.Credentials{Username: a.cfg.Username(), Password: password}, gonote.Options{Location: a.Now().Location(), ENT: a.cfg.ENT})
	if err != nil {
		return err
	}
	if err = a.Vault.Set(mode, a.cfg.CredentialKey(), password); err != nil {
		return err
	}
	oldMode := a.cfg.PasswordStorage
	a.cfg.PasswordStorage = mode
	if err = a.accept(c); err != nil {
		return err
	}
	if oldMode != "" && oldMode != mode {
		if err = a.Vault.Delete(oldMode, a.cfg.CredentialKey()); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.Out, a.ui.success("Mot de passe local mis à jour · reconnexion automatique activée"))
	return nil
}
