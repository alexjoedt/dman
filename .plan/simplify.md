Du bist ein Senior Go Software Engineer. Deine Aufgabe ist es, das CLI-Tool "dman" (ein opinionated dotfile manager) umfassend zu refactoren. 
Das Tool nutzt `github.com/urfave/cli/v3` für die CLI. 

Wir wechseln von einer komplexen CAS/BoltDB- und Git-Branch-basierten Architektur zu einer simplen, verzeichnisbasierten Overlay-Architektur (Base + Profil-Overrides) mit Flat-File-Backups.

Hier sind die strikten architektonischen Vorgaben für das Refactoring:

### 1. Zu entfernender Code (Deprecations)
- GANZ WICHTIG: Entferne jegliche Abhängigkeit zu `go.etcd.io/bbolt` (BoltDB) und dem aktuellen CAS (Content-Addressable Storage) github.com/alexjoedt/blobf System.
- Entferne die Kommandos: `backup`, `snapshots`, `restore`, `list`, `cat` und `env`.
- Entferne die Logik, die Git-Branches nutzt, um Umgebungen zu wechseln.

### 2. Neue Verzeichnisstruktur im Dotfile-Repository
Das Repository, das dman verwaltet, hat ab sofort folgende Struktur:
.
├── base/                   # Enthält Basis-Dotfiles für alle Systeme (mit `dot_` Prefix)
│   └── dot_zshrc
├── profiles/               # Enthält umgebungsspezifische Overrides und Skripte
│   ├── work/
│   │   ├── dotfiles/       # Überschreibt oder ergänzt Dateien aus base/
│   │   └── scripts/        # Ausführbare Skripte (z.B. 01_install.sh)
│   └── private/
└── dman.toml               # Deklarative Konfiguration des Repos (vorerst leer/einfach)

### 3. Konfiguration & lokaler State
Trennung zwischen operativem State und deklarativer Repo-Config:
- **Operativer State (`~/.config/dman/dman.json`)**: Speichert den Pfad zum lokalen Dotfile-Repo und das aktuell aktive Profil (z.B. `{"repository_path": "~/.local/share/dman", "active_profile": "work"}`).
- **Repo Config (`dman.toml`)**: Liegt im Root des Dotfile-Repos.

### 4. Das Backup-System (KISS-Prinzip)
Wenn `dman apply` eine Datei in `~/` überschreibt, wird die alte Datei als einfaches Flat-File in `~/.local/state/dman/backups/` kopiert.
Format: `~/.local/state/dman/backups/<filename>_<YYYYMMDD_HHMMSS>.bak`

### 5. Aktualisierte Kern-Kommandos
Schreibe / Refactore die folgenden CLI-Commands:

- `dman init <repo-url>`: 
  Cloned das Repo nach `~/.local/share/dman`, erstellt `~/.config/dman/dman.json` und setzt das Profil standardmäßig auf `default`.

- `dman apply [--profile <name>]`:
  1. Liest das aktive Profil aus der `dman.json` (oder überschreibt es via Flag).
  2. Kopiert alle Dateien aus `base/` nach `~/` (wandelt `dot_` in `.` um). Erstellt vorher Backups.
  3. Kopiert alle Dateien aus `profiles/<name>/dotfiles/` nach `~/` (überschreibt Base-Dateien).
  4. Findet alle Skripte in `profiles/<name>/scripts/`, sortiert sie lexikografisch (z.B. `01_...`, `02_...`) und führt sie via `os/exec` nacheinander aus.

- `dman add <file>`:
  Nimmt eine Datei aus dem Home-Verzeichnis (z.B. `~/.zshrc`), wandelt den Namen in `dot_zshrc` um und kopiert sie in den korrekten Ordner im lokalen Repo. 
  Wenn ein Profil aktiv ist, schaube ob diese datei bereits im profil liegt, wenn ja überschreibe sie dort, wenn nicht lege sie in base ab.
  Wenn der Nutze eine datei explizit zu einem profil hinzufügen möchte die dort noch nicht vorhanden ist, muss man `--profile` bei `add` mit angeben: `dman add --profile work .zshrc`

### Anforderungen an den Go-Code:
- Schreibe idiomatisches, sauberes Go.
- Nutze `os.ReadDir` und `path/filepath` für sicheres Datei-Handling.
- Vermeide Symlinks komplett (nutze `io.Copy` für Dateiübertragungen).
- Gehe schrittweise vor. Beginne mit dem Löschen des alten Codes und der Anpassung der Structs, danach implementiere `apply`.
