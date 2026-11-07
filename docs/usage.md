# Guide d’utilisation de gonote

Un CLI en français pour consulter sa journée de collège : **devoirs, contenus de
cours, notes et emploi du temps**. Écrit en Go, avec Lip Gloss et Huh de
Charmbracelet. Les commandes ont aussi des alias anglais.

Le CLI consulte les données scolaires et permet de marquer un devoir comme fait
ou de le remettre à faire. Ces modifications demandent une commande explicite.
Il ne dépose aucun rendu, ne marque aucun message comme lu et ne modifie aucun
réglage du compte. La bibliothèque Go reste en lecture seule par défaut ;
`Options.AllowWrites` active uniquement la modification du statut des devoirs.

## Essayer sans compte

Go **1.26 ou plus récent** est nécessaire. Depuis la racine du dépôt :

```sh
go build -trimpath -o bin/gonote ./cmd/gonote
./bin/gonote --demo
./bin/gonote --demo devoirs
./bin/gonote --demo edt --semaine prochaine --largeur 110
./bin/gonote --demo notes --resume
```

`--demo` utilise uniquement des données inventées. Il n’ouvre aucune connexion et
ne crée aucun compte. Ses références de détail sont enregistrées dans un cache
séparé. La date de démonstration suit la date réelle.

Sur un terminal large, l’emploi du temps présente une colonne par jour. Sur un
terminal étroit, il devient un agenda. `--agenda` force cette présentation.
Les couleurs sont désactivées dans les redirections et avec `NO_COLOR` ou
`--no-color`. `--color` les force, sauf si `NO_COLOR` est défini.

## Connecter un compte élève

```sh
./bin/gonote connexion
./bin/gonote
```

Le formulaire demande l’URL PRONOTE du collège, l’identifiant et le mot de passe
élève. La session authentifiée est conservée entre les commandes, avec ses cookies,
clés et compteur de requêtes. Une nouvelle commande réutilise cette session sans
redemander le mot de passe. Sa durée dépend du serveur ; le CLI n’impose pas une
expiration de quelques secondes. Un verrou évite que deux commandes utilisent
simultanément le même compteur. Une commande interrompue pendant une requête
provoque une nouvelle connexion, plutôt que de réutiliser un compteur incertain.

Le mot de passe est masqué et reste temporaire par défaut. Pour renouveler la
session automatiquement après sa véritable expiration, active sa mémorisation :

```sh
./bin/gonote connexion --memoriser
# Avec ÉCLAT / EduConnect :
./bin/gonote connexion --ent eclat-bfc --memoriser
```

Le stockage par défaut est le trousseau du système : Secret Service sous Linux,
Trousseau sous macOS, Gestionnaire d’identifiants sous Windows. Sur une machine
sans trousseau (par exemple WSL sans service Secret Service), choisis explicitement
un fichier local **en clair**, accessible au seul utilisateur :

```sh
./bin/gonote connexion --ent eclat-bfc --memoriser --stockage fichier
# Pour un compte déjà configuré, vérifier et mémoriser le mot de passe actuel :
./bin/gonote mot-de-passe --stockage fichier
# Supprimer uniquement le mot de passe local, en gardant la session actuelle :
./bin/gonote oublier-mot-de-passe
```

Il n’y a aucun repli silencieux du trousseau vers un fichier. `mot-de-passe` ne
change pas le mot de passe sur le serveur. Si celui-ci doit réellement être
renouvelé, fais-le sur le site officiel, puis mets à jour la copie locale.
Si PRONOTE fournit un jeton de reconnexion, celui-ci est aussi conservé.

L’authentification crée une session sur le serveur. Si PRONOTE exige un PIN,
l’enregistrement d’un appareil, un changement de mot de passe ou une autre action
de sécurisation, le client s’arrête. Il n’exécute aucune de ces actions.

### ÉCLAT-BFC avec EduConnect

```sh
./bin/gonote connexion --ent eclat-bfc
```

Entre **l’URL PRONOTE du collège**, puis tes identifiants **EduConnect élève**.
Par exemple : `https://college.index-education.net/pronote/`.
N’entre ni une URL EduConnect avec
`execution=…`, ni une URL contenant un ticket CAS ou un `identifiant=…` de session.

Le client démarre un nouveau parcours SSO et sélectionne EduConnect sur ÉCLAT.
Il conserve les cookies dans la session privée, reprend les champs cachés/CSRF
des formulaires courants et relaie `SAMLRequest`, `SAMLResponse` et `RelayState`.
Le mot de passe n’est envoyé qu’à `educonnect.education.gouv.fr`. La navigation
est limitée à l’instance PRONOTE choisie, `cas.eclat-bfc.fr` et EduConnect. Aucune
assertion SAML, page HTML ou trace d’authentification n’est enregistrée.

Après le retour vers PRONOTE, le client utilise les identifiants temporaires ENT
fournis par la page et le mode d’authentification `pourENT`. Il accepte les espaces
élèves mobile ou web selon la page de retour. Si un jeton mobile est fourni, les
reconnexions peuvent passer directement par PRONOTE. À l’expiration de la session
PRONOTE, le client essaie d’abord les cookies ÉCLAT/EduConnect encore valides.
Si EduConnect exige à nouveau les identifiants, il utilise le mot de passe mémorisé
ou le demande. Une lecture interrompue par l’expiration est retentée une seule
fois après reconnexion. Un téléchargement partiel n’est pas retenté.

Une erreur indique l’étape en cours sans révéler les paramètres de session. Une
nouvelle demande de mot de passe après soumission n’est pas retentée. CAPTCHA,
consentement, vérification supplémentaire ou modification de compte provoquent
un arrêt : ces opérations ne sont pas automatisées. Le parcours SSO est limité
à 30 requêtes et deux minutes pour la procédure d’authentification uniquement,
sans limiter la durée de la session. Les autres fournisseurs ENT ne sont pas inclus.

Pour une exécution non interactive, `GONOTE_PASSWORD` peut fournir le mot de
passe par l’environnement si une reconnexion devient nécessaire. Le fichier
`.env` n’est pas chargé automatiquement. Il n’existe pas d’option de mot de passe
en ligne de commande. Un mot de passe refusé n’est pas soumis plusieurs fois ;
le CLI invite à mettre à jour la copie locale avec `gonote mot-de-passe`.

L’implémentation est testée avec un serveur simulé et des réponses capturées dans
les projets de référence. Le parcours ÉCLAT-BFC/EduConnect a aussi été validé sur
un compte élève réel, jusqu’aux lectures des devoirs, de l’emploi du temps et des
notes. Les variantes d’instance et d’authentification peuvent nécessiter des
ajustements.

## Les commandes

| Commande | Alias anglais | Affichage |
| --- | --- | --- |
| `gonote` | — | Résumé du jour et prochaine journée de cours |
| `gonote devoirs` | `homework` | Devoirs non marqués faits, groupés par échéance |
| `gonote fait <réf>` | `done` | Marquer un devoir comme fait sur PRONOTE |
| `gonote a-faire <réf>` | `todo` | Remettre un devoir à faire sur PRONOTE |
| `gonote cours` | `lessons` | Contenus de cours publiés aujourd’hui |
| `gonote notes` | `grades` | Les 20 dernières notes de la période actuelle |
| `gonote emploi-du-temps` ou `edt` | `timetable` | Emploi du temps de la semaine |
| `gonote periodes` | `periods` | Périodes scolaires disponibles |
| `gonote voir <réf>` | `show` | Détail complet d’un élément déjà consulté |
| `gonote fichiers <réf>` | `files` | Pièces jointes de cet élément |
| `gonote telecharger <réf> [numéro]` | `download` | Télécharger une pièce jointe |
| `gonote connexion` | `login` | Configurer le compte élève |
| `gonote mot-de-passe` | — | Vérifier et mémoriser le mot de passe actuel |
| `gonote oublier-mot-de-passe` | — | Supprimer le mot de passe local |
| `gonote anglais` | — | Découvrir les alias anglais |

Exemples, après avoir construit le binaire et ajouté `bin` au `PATH` :

```sh
gonote devoirs --demain
gonote devoirs --tout
gonote devoirs --faits --matiere maths
gonote cours --hier
gonote cours --semaine precedente
gonote edt --semaine prochaine
gonote edt --le 2026-11-09 --agenda
gonote notes --matiere maths --tout
gonote notes --periode "Trimestre 1" --resume
gonote devoirs --json
```

Les références comme `h-1c81890d` restent stables après un rafraîchissement. Elles
sont créées lors de la consultation d’une liste ou du résumé. `voir` affiche le
détail enregistré ; `voir <réf> --actualiser` le recharge depuis PRONOTE. Les longs
détails utilisent `less` s’il est disponible dans un terminal ; `--no-pager`
désactive cette pagination.

```sh
gonote voir h-1c81890d
gonote fichiers h-1c81890d
gonote telecharger h-1c81890d 1 --vers exercice.pdf
```

Pour changer le statut d’un devoir, utilise sa référence affichée dans la liste :

```sh
gonote devoirs
gonote fait h-1234abcd
gonote devoirs --faits
gonote a-faire h-1234abcd
# Équivalents anglais : done et todo ; sortie JSON possible avec --json.
```

Le CLI relit le devoir avant la modification et vérifie ensuite l’état retourné
par PRONOTE. Si le devoir a déjà l’état demandé, aucune écriture n’est envoyée.
Les listes en cache qui pourraient contenir l’ancien état sont invalidées.
En cas de réponse perdue, il ne répète pas automatiquement l’écriture et invite
à vérifier avec `gonote devoirs --tout --actualiser`. Cocher « fait » ne dépose
aucun fichier et ne soumet aucun QCM. Les modifications ne sont disponibles ni
en mode hors ligne ni en démo.

Un téléchargement ne remplace jamais un fichier existant. Les liens vers des
sites externes sont affichés mais ne sont pas téléchargés. La limite d’un fichier
est de 100 Mio. Les pièces jointes de démonstration sont fictives.

## Dates et états

- `--demain` et `--hier` désignent les jours du calendrier, même le week-end.
- Sans option, le résumé cherche la prochaine journée avec un cours actif dans
  les 14 jours à venir. Il affiche aussi le travail à venir et les notes récentes.
- Les devoirs affichés par défaut couvrent 14 jours passés et 30 jours à venir.
  Une échéance passée n’implique pas que le professeur considère le travail manquant.
- « Fait » et « rendu déposé » sont deux informations distinctes, lues depuis PRONOTE.
- Les moyennes sont celles fournies par PRONOTE. Elles ne sont pas recalculées.
- Une absence, dispense ou note non publiée conserve son état, sans devenir zéro.
- Un cours annulé reste visible. S’il chevauche son remplacement, il est masqué
  par défaut ; `edt --annulations` le réaffiche.
- Le fuseau est `Europe/Paris` par défaut. La bibliothèque accepte un autre
  fuseau, et la configuration locale contient le champ `timezone`.

## Cache et confidentialité

Les données récentes sont réutilisées pendant dix minutes :

```sh
gonote --actualiser
gonote devoirs --hors-ligne
gonote edt --hors-ligne --demain
```

Le mode hors ligne ne se connecte jamais. Il demande un intervalle précédemment
enregistré, ou un intervalle inclus dans un enregistrement plus large. Il ne
combine pas plusieurs intervalles partiels. L’horodatage est toujours affiché.
Un échec réseau ne présente pas silencieusement une ancienne copie comme actuelle.

Les fichiers utilisent les dossiers standard du système, compatibles avec les
variables XDG sous Linux :

- configuration : `$XDG_CONFIG_HOME/gonote/compte.json` ou `~/.config/gonote/compte.json` ;
- sessions et cookies : sous-dossier `sessions/` du dossier de configuration ;
- mot de passe en mode fichier : sous-dossier `identifiants/` du même dossier ;
- cache : `$XDG_CACHE_HOME/gonote/` ou `~/.cache/gonote/`.

Les fichiers sont créés avec le mode `0600`, les dossiers avec `0700` et les
écritures utilisent un remplacement atomique. Le cache est séparé par compte.
Les sessions, cookies, jetons et données scolaires sont stockés en clair dans ces
fichiers privés : traite le dossier de configuration comme les sessions d’un
navigateur. Le mot de passe est stocké uniquement sur demande, dans le trousseau
ou dans le fichier choisi explicitement. Les sorties JSON ne contiennent ni clé,
ni cookie, ni mot de passe, ni jeton. Aucun journal de requête ou de réponse n’est écrit.

## Compatibilité avec le prototype

Si aucun compte gonote n’existe mais qu’un ancien `pronote/compte.json` est
présent dans le dossier de configuration du système, gonote réutilise ce dossier
et son cache. Aucun identifiant n’est recopié. `PRONOTE_PASSWORD` reste accepté
comme ancien nom de `GONOTE_PASSWORD`, qui est prioritaire.

Le mode `--stockage fichier` exige des permissions Unix ; sous Windows natif,
utilise le trousseau système. WSL utilise les permissions Linux.
