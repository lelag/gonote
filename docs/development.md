# Développement

## Couverture actuelle et limites

Les projets TypeScript sont des références de protocole, pas des dépendances
exécutées par le programme Go. Le tableau décrit la couverture de gonote.

| Fonction | Bibliothèque Go | CLI |
| --- | --- | --- |
| Connexion élève directe, jeton mobile, ÉCLAT/EduConnect | Oui | `connexion` |
| Instantané et reprise de session avec cookies et compteur | `Snapshot`, `RestoreSession` | Automatique, avec verrou entre processus |
| Renouvellement automatique / mot de passe local | Primitives de connexion ; politique à la charge de l’appelant | Oui, mot de passe sur demande |
| Profil élève et périodes | `Profile` | En-têtes, `periodes` |
| Devoirs et statut fait / à faire | `Homework`, `SetHomeworkDone` | `devoirs`, `fait`, `a-faire` |
| Contenus de cours | `Lessons` | `cours`, `voir` |
| Emploi du temps et changements de cours | `Timetable` | `edt`, vue semaine / agenda |
| Notes et moyennes fournies par PRONOTE | `Grades` | `notes`, `notes --resume` |
| Pièces jointes internes PRONOTE | `Download` vers un `io.Writer` | `fichiers`, `telecharger` |
| Filtres, références courtes, cache hors ligne, JSON | Politique d’interface, sans dépendance dans l’API | Oui |
| Téléversement / suppression de rendu de devoir | Non | Non |
| QCM : questions, réponses, validation | Non | Non |
| Discussions, destinataires, brouillons, réponses et suppression | Non | Non |
| Actualités, accusés de réception et sondages | Non | Non |
| Absences, retards et carnet de vie scolaire détaillé | Non | Non |
| Compétences / évaluations et bulletins PDF | Non | Non |
| Menus de cantine et informations personnelles détaillées | Non | Non |
| Recherche d’établissement et export iCalendar | Non | Non |
| Comptes parent / enseignant et sélection d’enfant | Non | Non |
| Connexion par QR code, autres ENT, étapes PIN / sécurité | Non ; étapes de sécurité signalées par erreur typée | Non |

Pawnote contient notamment les endpoints de messagerie, actualités, vie scolaire,
compétences, bulletins et menus encore manquants chez nous. Blocksnote fournit
une référence plus ciblée pour les devoirs, notes, emplois du temps, rendus et
QCM. Les lectures principales et ÉCLAT/EduConnect ont été vérifiés sur un compte
élève réel ; les changements de statut sont testés contre des serveurs simulés,
sans modifier de vrai devoir.

Le protocole, les modèles et les erreurs restent dans le package public `gonote`,
sans Cobra, Huh, Lip Gloss ni accès aux dossiers de l’utilisateur. La CLI gère les
références courtes, les formulaires, le rendu, la mémorisation locale et la politique
de reconnexion. Une future TUI peut appeler directement la même API. Pour partager
également la politique complète de reconnexion/cache entre plusieurs interfaces,
la prochaine étape sera de l’extraire de la CLI vers un service applicatif commun.

## Développement

```sh
make build
make test
make check
```

`make check` exécute `go vet` et les tests avec le détecteur de races. Aucun test
ne contacte un établissement. Les tests couvrent le défi d’authentification,
les dialectes, le chiffrement/compression, les lectures concurrentes, les
changements de statut et leurs vérifications, les refus des autres écritures, les états de notes, les réponses capturées, le cache hors ligne et
la largeur des affichages. Le parcours ÉCLAT/EduConnect est testé en mémoire,
avec CSRF, cookies par domaine, formulaires SAML, redirections, défi ENT,
reconnexion par jeton, refus des destinations étrangères et arrêt des étapes
interactives. Aucun identifiant réel n’est utilisé par les tests.
