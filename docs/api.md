# Bibliothèque Go

Le package `github.com/lelag/gonote` expose des modèles typés et des méthodes
indépendantes du terminal. Il n’importe ni Cobra, ni Huh, ni Lip Gloss. Aucun
fichier de compte ni variable d’environnement n’est lu par la bibliothèque.

## Exemple

```go
package example

import (
    "context"
    "time"

    "github.com/lelag/gonote"
)

func Homework(ctx context.Context, url, username, password string) ([]gonote.Assignment, error) {
    client, err := gonote.Login(ctx, url, gonote.Credentials{
        Username: username,
        Password: password,
    }, gonote.Options{})
    if err != nil {
        return nil, err
    }
    start := time.Now()
    return client.Homework(ctx, gonote.DateRange{
        From: start,
        To: start.AddDate(0, 0, 7),
    })
}
```

Pour ÉCLAT-BFC, les identifiants sont ceux d’EduConnect et les options incluent
`ENT: gonote.ENTEclatBFC`. `SSOCookies` permet de réutiliser une session SSO.
Les autres méthodes publiques sont :

| Méthode | Résultat |
| --- | --- |
| `Profile()` | Élève, classe, établissement et périodes |
| `Lessons(ctx, dates)` | Contenus de cours |
| `Timetable(ctx, dates)` | Cours, salles, enseignants et changements |
| `Grades(ctx, periodID)` | Notes, états particuliers et moyennes fournies |
| `Download(ctx, attachment, writer)` | Fichier écrit dans un `io.Writer` |
| `SetHomeworkDone(ctx, id, done)` | Statut explicite fait / à faire |
| `Reconnect()` | Jeton mobile et identifiants d’appareil, si disponibles |
| `Snapshot(ctx)` | État courant du protocole, sans accès au disque |

Les intervalles incluent leurs deux dates. Les identifiants de la bibliothèque
sont les identifiants PRONOTE, pas les références locales `h-…` de la CLI.
Les pièces jointes externes ne sont pas téléchargées.

Les écritures sont désactivées par défaut. Pour `SetHomeworkDone`, passe
`Options{AllowWrites: true}` à `Login` ou à `RestoreSession`. C’est la seule
écriture prise en charge. Une erreur de transport peut laisser son résultat
inconnu : relis l’état avant de décider de répéter la modification.

## Erreurs et transport

`errors.Is` reconnaît notamment `ErrCredentials`, `ErrCredentialsRequired`,
`ErrSessionExpired`, `ErrAccessDenied`, `ErrUnavailable`, `ErrRateLimited`,
`ErrReadOnly`, `ErrProtocol`, `ErrSSOFlow` et `ErrSSOInteraction`.
`SSOError.Stage` précise l’étape d’une erreur SSO sans exposer ses paramètres.
`SecurityRequiredError` signale les actions de sécurité à terminer sur le site.

Un `HTTPClient` et un fuseau horaire peuvent être injectés dans `Options`.
`AllowHTTP` sert aux tests ou à une instance explicitement choisie sans TLS ;
la CLI ne l’active pas. La bibliothèque standard et `golang.org/x/net` fournissent
le transport, le traitement HTML SSO et les règles de domaines de cookies.

Le transport sérialise le compteur de requêtes, l’envoi, le décodage et les
changements cryptographiques. Les tests couvrent les dialectes avant 2024.3.9,
entre 2024.3.9 et 2025.1.3, et à partir de 2025.1.3, ainsi que les combinaisons de
compression et de chiffrement.

## Sessions et autres interfaces

`Snapshot(ctx)` et `RestoreSession(state, options)` conservent les clés, cookies,
compteur, profil et paramètres du protocole. Ces données sont des secrets.
Les permissions d’écriture ne sont pas conservées dans les instantanés.

L’application appelante doit conserver un verrou exclusif depuis la restauration
jusqu’à l’enregistrement du nouvel instantané. Ne partage pas un même instantané
entre plusieurs processus actifs. Avant une requête, marque la copie persistée
comme inutilisable ; après succès, enregistre le nouveau compteur. Ainsi, une
interruption ne provoque pas la réutilisation d’un compteur incertain.

La CLI fournit la mémorisation locale, le verrou, les références courtes et la
politique de reconnexion. Une future TUI peut appeler les mêmes méthodes ; la
politique commune de stockage/reconnexion pourra ensuite être extraite en service
applicatif partagé.

```text
*.go                    Protocole et modèles publics
cmd/gonote/             Point d’entrée du binaire
internal/cli/           Commandes, formulaires Huh et rendu Lip Gloss
internal/store/         Configuration, cache, sessions et références locales
internal/credentials/   Trousseau ou fichier explicite
internal/demo/          Données entièrement fictives
testdata/               Réponses de référence avec identifiants et noms synthétiques
```
