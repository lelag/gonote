package gonote

import "errors"

var (
	ErrCredentials         = errors.New("identifiants incorrects")
	ErrCredentialsRequired = errors.New("la session SSO a expiré : identifiants nécessaires")
	ErrSessionExpired      = errors.New("session expirée : reconnectez-vous")
	ErrAccessDenied        = errors.New("accès non autorisé pour ce compte")
	ErrUnavailable         = errors.New("PRONOTE est momentanément indisponible")
	ErrRateLimited         = errors.New("trop de requêtes : réessayez plus tard")
	ErrReadOnly            = errors.New("opération interdite : client en lecture seule")
	ErrProtocol            = errors.New("réponse PRONOTE incompatible ou incomplète")
)

// SecurityRequiredError means authentication requires an account change or
// registration. This client deliberately never performs those actions.
type SecurityRequiredError struct{ Actions []int }

func (e *SecurityRequiredError) Error() string {
	return "PRONOTE demande une vérification PIN, un enregistrement d’appareil ou un changement de mot de passe ; terminez cette étape sur le site officiel"
}
