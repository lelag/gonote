# gonote

**Sa journée de collège, dans le terminal.** Un client PRONOTE non officiel en
Go pour les élèves : devoirs, contenus de cours, notes et emploi du temps.

Les commandes et les réponses sont en français, avec des alias anglais.
L’affichage utilise [Lip Gloss](https://github.com/charmbracelet/lipgloss) et les
formulaires [Huh](https://github.com/charmbracelet/huh), de Charmbracelet.
Le protocole PRONOTE est fourni par une bibliothèque Go réutilisable, indépendante
du terminal.

> Préversion. Les lectures principales et le parcours ÉCLAT-BFC/EduConnect ont été
> testés sur un compte élève ; la compatibilité peut varier selon l’établissement.
> Les changements de statut des devoirs sont vérifiés par des tests simulés.

## Installation

Go **1.26 ou plus récent** :

```sh
go install github.com/lelag/gonote/cmd/gonote@latest
gonote --demo
```

Ou depuis les sources :

```sh
git clone https://github.com/lelag/gonote.git
cd gonote
make build
./bin/gonote --demo
```

Sans `make` : `go build -o bin/gonote ./cmd/gonote`.
Le mode démo utilise exclusivement des données inventées, sans connexion.

## Premier compte

```sh
gonote connexion
gonote
```

Le formulaire demande l’adresse PRONOTE du collège et les identifiants **élève**.
Pour un établissement utilisant ÉCLAT-BFC et EduConnect :

```sh
gonote connexion --ent eclat-bfc
```

Utilise l’adresse PRONOTE, par exemple
`https://college.index-education.net/pronote/`, puis les identifiants EduConnect.
N’utilise pas une URL temporaire contenant un ticket ou un identifiant de session.

La session est conservée entre les commandes. Son expiration dépend du serveur ;
les cookies SSO encore valides permettent une reconnexion sans mot de passe.
Un mot de passe refusé n’est pas soumis en boucle.

## Les commandes

| Commande | Alias anglais | Usage |
| --- | --- | --- |
| `gonote` | — | Journée et prochains cours, devoirs et derniers résultats |
| `gonote devoirs` | `homework` | Travail à faire, classé par date |
| `gonote fait <réf>` | `done` | Marquer un devoir comme fait |
| `gonote a-faire <réf>` | `todo` | Remettre un devoir à faire |
| `gonote cours` | `lessons` | Contenus étudiés en classe |
| `gonote edt` | `timetable` | Emploi du temps de la semaine |
| `gonote notes` | `grades` | Notes et moyennes fournies par PRONOTE |
| `gonote periodes` | `periods` | Trimestres ou semestres disponibles |
| `gonote voir <réf>` | `show` | Détail d’un élément consulté |
| `gonote fichiers <réf>` | `files` | Pièces jointes |
| `gonote telecharger <réf> [numéro]` | `download` | Télécharger un fichier |
| `gonote anglais` | — | Découvrir les alias anglais |

```sh
gonote devoirs --demain
gonote devoirs --tout --matiere maths
gonote edt --semaine prochaine
gonote notes --resume
gonote cours --hier
gonote devoirs --json
```

Chaque devoir affiche une référence locale, comme `h-1234abcd` :

```sh
gonote fait h-1234abcd
gonote devoirs --faits
gonote a-faire h-1234abcd
```

Le CLI vérifie le statut sur PRONOTE avant et après une modification et met à jour
le cache. Il ne répète pas automatiquement une écriture dont la réponse a été
perdue. Marquer « fait » ne dépose aucun fichier et ne valide aucun QCM.

Sur un terminal large, l’emploi du temps forme une grille ; sur un écran étroit,
il devient un agenda. `--agenda` force la liste, `--no-color` coupe les couleurs,
et `--help` affiche les options de chaque commande.

## Sessions, mots de passe et mode hors ligne

Le mot de passe reste temporaire par défaut. Pour permettre une reconnexion
après expiration complète de la session SSO, utilise le trousseau du système :

```sh
gonote connexion --ent eclat-bfc --memoriser
# Ou mémoriser le mot de passe d’un compte déjà configuré :
gonote mot-de-passe
# Supprimer uniquement le mot de passe local :
gonote oublier-mot-de-passe
```

Sans trousseau, sous Linux ou macOS, `--stockage fichier` choisit explicitement un
fichier **en clair**, avec permissions `0600`. Aucun repli silencieux n’est effectué.
Les sessions, cookies et données scolaires sont aussi conservés dans des fichiers
privés. Traite le dossier de configuration comme les sessions d’un navigateur.

- Configuration : dossier système `gonote/` (`~/.config/gonote/` sous Linux).
- Cache : dossier de cache système `gonote/` (`~/.cache/gonote/` sous Linux).
- Les variables XDG sont respectées sous Linux. Les comptes du prototype sont
  réutilisés s’ils existent ; voir le [guide](docs/usage.md#compatibilité-avec-le-prototype).

```sh
gonote devoirs --actualiser
gonote devoirs --hors-ligne
```

Le cache ordinaire reste frais dix minutes. `--hors-ligne` n’accède jamais au réseau
et les modifications de devoirs nécessitent une connexion. Les scripts peuvent
utiliser `GONOTE_PASSWORD` ; les fichiers `.env` ne sont pas chargés automatiquement.

## Bibliothèque Go et contribution

Le module `github.com/lelag/gonote` expose `Login`, `Homework`, `Lessons`,
`Timetable`, `Grades`, `Download`, `Snapshot`, `RestoreSession` et
`SetHomeworkDone`. Les écritures sont désactivées par défaut et nécessitent
`Options{AllowWrites: true}`. Aucun composant de terminal n’est importé par l’API.

- [API, architecture et sessions](docs/api.md)
- [Guide détaillé](docs/usage.md)
- [Fonctions prises en charge, limites et développement](docs/development.md)

```sh
make check   # go vet et tests avec détecteur de races, sans compte réel
make build
```

La messagerie, les rendus de devoirs, les QCM, les actualités, la vie scolaire
détaillée, les menus et les comptes parent/enseignant ne sont pas encore inclus.
Les demandes de PIN, CAPTCHA, changement de mot de passe ou consentement doivent
être terminées sur le site officiel.

## Sources et licence

Projet sans affiliation avec INDEX ÉDUCATION. PRONOTE est un produit INDEX ÉDUCATION.

Le protocole a été étudié à partir de [Pawnote](https://github.com/LiterateInk/Pawnote.js),
[Blocksnote](https://github.com/BlocksHub/Blocksnote) et
[pronotepy](https://github.com/bain3/pronotepy). Les fichiers de test dérivés de
Pawnote utilisent des identifiants et noms synthétiques. Ces projets ne sont pas
exécutés par gonote.

Distribué sous **GPL-3.0-or-later**. Voir [LICENSE](LICENSE) et [NOTICE](NOTICE)
pour les attributions et les révisions de référence.
