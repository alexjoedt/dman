package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/hash"
	"github.com/alexjoedt/dman/internal/snapshot"
	"github.com/alexjoedt/log"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// stackWidth is the terminal width below which the two panes stack vertically
// instead of sitting side by side.
const stackWidth = 80

type rowKind uint8

const (
	rowDir rowKind = iota
	rowFile
)

// row is one line of the flattened file tree. Rows are held in depth-first
// order; which of them are visible is derived from the expanded set.
type row struct {
	kind  rowKind
	depth int
	label string // base name; directories carry a trailing separator
	// key identifies the row across rebuilds: repo-relative in repo mode,
	// home-relative in snapshot mode. The two namespaces never overlap.
	key string
	// pair.Dst is the absolute home path in both modes; pair.Src is set only
	// for repo rows.
	pair dotfile.Pair
	// checksum is the snapshot blob key. Empty means this is a repo row.
	checksum string
	changed  bool
}

// sourceKind is where the tree's contents come from.
type sourceKind uint8

const (
	sourceRepo sourceKind = iota
	sourceSnapshot
)

type focusPane uint8

const (
	paneTree focusPane = iota
	panePreview
)

type viewMode uint8

const (
	viewDiff viewMode = iota
	viewPreview
)

type overlayKind uint8

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlayConfirm
	overlayProfiles
	overlaySnapshots
)

type browseModel struct {
	// ctx is carried on the model because Bubble Tea commands are closures
	// with no other way to reach the caller's cancellation.
	ctx     context.Context
	app     *App
	cfg     *Config
	profile string
	st      styles

	rows    []row
	visible []int // indices into rows
	cursor  int   // index into visible
	offset  int   // first visible row drawn in the tree pane

	marked   map[string]bool // by row.key, survives a rebuild
	expanded map[string]bool // by directory row.key, survives a rebuild

	preview viewport.Model
	spin    spinner.Model

	mode      viewMode
	focus     focusPane
	accordion bool

	source    sourceKind
	snapStore *snapshot.Store
	snapMeta  snapshot.Meta
	snapshots []snapshot.Meta
	snapIdx   int

	overlay     overlayKind
	confirmText string
	confirmCmd  tea.Cmd
	confirmBusy string
	// confirmReturn is the overlay to show once the dialog is dismissed;
	// overlayNone for a confirm raised from the tree.
	confirmReturn overlayKind
	profiles      []string
	profileIdx    int

	filter    string
	filtering bool

	busy   string // non-empty while an action is in flight
	status string

	width, height int
}

func newBrowseModel(ctx context.Context, a *App, cfg *Config, profile string, pairs []dotfile.Pair) *browseModel {
	m := &browseModel{
		ctx:      ctx,
		app:      a,
		cfg:      cfg,
		profile:  profile,
		st:       newStyles(),
		marked:   map[string]bool{},
		expanded: map[string]bool{},
		preview:  viewport.New(),
		spin:     spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
	m.preview.MouseWheelEnabled = true
	m.setRows(buildRows(pairs, cfg.Path))
	return m
}

// Browse starts the interactive dotfile browser TUI.
func (a *App) Browse(ctx context.Context, profileFlag string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	pairs, err := a.collectTracked(cfg, profile)
	if err != nil {
		return err
	}

	// The TUI owns the screen for its whole lifetime, so silence the CLI
	// logger once here rather than around every action.
	prev := log.Default()
	log.SetDefault(log.NewCLILogger(log.WithWriter(io.Discard)))
	defer log.SetDefault(prev)

	m := newBrowseModel(ctx, a, cfg, profile, dotfile.Merge(pairs))
	_, err = tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

// flattenRows turns a set of relative paths into a depth-first row list,
// inserting a directory row whenever the path prefix changes. mk fills in the
// per-source fields of each file row.
func flattenRows(rels []string, mk func(rel string) row) []row {
	sort.Strings(rels)

	sep := string(filepath.Separator)
	rows := make([]row, 0, len(rels))
	var prev []string
	for _, rel := range rels {
		parts := strings.Split(rel, sep)
		dirs := parts[:len(parts)-1]

		common := 0
		for common < len(dirs) && common < len(prev) && dirs[common] == prev[common] {
			common++
		}
		for i := common; i < len(dirs); i++ {
			rows = append(rows, row{
				kind:  rowDir,
				depth: i,
				label: dirs[i] + sep,
				key:   strings.Join(dirs[:i+1], sep),
			})
		}

		r := mk(rel)
		r.kind = rowFile
		r.depth = len(dirs)
		r.label = parts[len(parts)-1]
		r.key = rel
		rows = append(rows, r)

		prev = dirs
	}
	return rows
}

// buildRows lays out the tracked dotfiles, keyed by their path in the repo.
func buildRows(pairs []dotfile.Pair, repoPath string) []row {
	byRel := make(map[string]dotfile.Pair, len(pairs))
	rels := make([]string, 0, len(pairs))
	for _, p := range pairs {
		rel, err := filepath.Rel(repoPath, p.Src)
		if err != nil {
			continue
		}
		if _, dup := byRel[rel]; dup {
			continue
		}
		byRel[rel] = p
		rels = append(rels, rel)
	}

	return flattenRows(rels, func(rel string) row {
		return row{pair: byRel[rel]}
	})
}

// buildSnapshotRows lays out a snapshot's manifest, keyed by the home-relative
// path the file was captured from.
func buildSnapshotRows(files []snapshot.File, homeDir string) []row {
	byRel := make(map[string]snapshot.File, len(files))
	rels := make([]string, 0, len(files))
	for _, f := range files {
		if _, dup := byRel[f.Path]; dup {
			continue
		}
		byRel[f.Path] = f
		rels = append(rels, f.Path)
	}

	return flattenRows(rels, func(rel string) row {
		f := byRel[rel]
		return row{
			pair:     dotfile.Pair{Dst: filepath.Join(homeDir, f.Path)},
			checksum: f.Checksum,
		}
	})
}

// setRows installs a new row set and recomputes what is visible. Marks and
// expansion state are keyed by path, so both survive the swap.
func (m *browseModel) setRows(rows []row) {
	m.rows = rows
	m.recomputeVisible()
}

// recomputeVisible derives m.visible from the expanded set and the active
// filter. A filter flattens the tree to matching files.
func (m *browseModel) recomputeVisible() {
	prevKey := m.currentKey()

	m.visible = m.visible[:0]
	filter := strings.ToLower(m.filter)
	hideBelow := -1

	for i, r := range m.rows {
		if hideBelow >= 0 {
			if r.depth > hideBelow {
				continue
			}
			hideBelow = -1
		}
		if filter != "" {
			if r.kind == rowFile && strings.Contains(strings.ToLower(r.key), filter) {
				m.visible = append(m.visible, i)
			}
			continue
		}
		m.visible = append(m.visible, i)
		if r.kind == rowDir && !m.expanded[r.key] {
			hideBelow = r.depth
		}
	}

	m.cursor = 0
	if prevKey != "" {
		for i, idx := range m.visible {
			if m.rows[idx].key == prevKey {
				m.cursor = i
				break
			}
		}
	}
	m.clampCursor()
	m.syncOffset()
}

func (m *browseModel) clampCursor() {
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// current returns the row under the cursor, or nil when the tree is empty.
func (m *browseModel) current() *row {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return nil
	}
	return &m.rows[m.visible[m.cursor]]
}

func (m *browseModel) currentKey() string {
	if r := m.current(); r != nil {
		return r.key
	}
	return ""
}

func (m *browseModel) countMarked() int {
	n := 0
	for _, r := range m.rows {
		if r.kind == rowFile && m.marked[r.key] {
			n++
		}
	}
	return n
}

// targets returns the destination paths and row keys the next action applies
// to: the marked files, or the file under the cursor when nothing is marked.
func (m *browseModel) targets() (dsts, keys []string) {
	for _, r := range m.rows {
		if r.kind == rowFile && m.marked[r.key] {
			dsts = append(dsts, r.pair.Dst)
			keys = append(keys, r.key)
		}
	}
	if len(dsts) > 0 {
		return dsts, keys
	}
	if r := m.current(); r != nil && r.kind == rowFile {
		return []string{r.pair.Dst}, []string{r.key}
	}
	return nil, nil
}

// Messages produced by background work.

type actionDoneMsg struct {
	verb string // "apply" | "save" | "pull"
	keys []string
	err  error
}

type rescanMsg struct {
	profile string
	pairs   []dotfile.Pair
	err     error
}

type changedMsg struct {
	changed map[string]bool
}

// snapListMsg carries the snapshot index for the picker.
type snapListMsg struct {
	metas []snapshot.Meta
	err   error
}

// snapOpenMsg carries one snapshot's manifest, switching the tree into
// snapshot mode.
type snapOpenMsg struct {
	meta  snapshot.Meta
	files []snapshot.File
	store *snapshot.Store
	err   error
}

func applyCmd(ctx context.Context, a *App, profile string, dsts, keys []string) tea.Cmd {
	return func() tea.Msg {
		// noPull=true: apply exactly what the diff pane showed, without pulling
		// underneath the user. Snapshots stay on, so every overwrite is undoable.
		err := a.Apply(ctx, profile, false, true, false, dsts)
		return actionDoneMsg{verb: "apply", keys: keys, err: err}
	}
}

func saveCmd(ctx context.Context, a *App, profile string, dsts, keys []string) tea.Cmd {
	return func() tea.Msg {
		err := a.SaveToRepo(ctx, profile, dsts)
		return actionDoneMsg{verb: "save", keys: keys, err: err}
	}
}

func pullCmd(ctx context.Context, a *App) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{verb: "pull", err: a.Pull(ctx)}
	}
}

func rescanCmd(a *App, cfg *Config, profile string) tea.Cmd {
	return func() tea.Msg {
		pairs, err := a.collectTracked(cfg, profile)
		return rescanMsg{profile: profile, pairs: dotfile.Merge(pairs), err: err}
	}
}

func restoreCmd(ctx context.Context, a *App, id string, dsts, keys []string) tea.Cmd {
	return func() tea.Msg {
		err := a.SnapshotRestore(ctx, id, dsts)
		return actionDoneMsg{verb: "restore", keys: keys, err: err}
	}
}

func snapCreateCmd(ctx context.Context, a *App) tea.Cmd {
	return func() tea.Msg {
		if err := a.SnapshotCreate(ctx, "manual: from browse"); err != nil {
			return snapListMsg{err: err}
		}
		return snapListCmd(a)()
	}
}

func snapDeleteCmd(ctx context.Context, a *App, id string) tea.Cmd {
	return func() tea.Msg {
		if err := a.SnapshotDelete(ctx, id); err != nil {
			return snapListMsg{err: err}
		}
		return snapListCmd(a)()
	}
}

// snapListCmd loads the snapshot index, newest first.
func snapListCmd(a *App) tea.Cmd {
	return func() tea.Msg {
		store, err := a.browseSnapshotStore()
		if err != nil {
			return snapListMsg{err: err}
		}
		metas, err := store.List()
		if err != nil {
			return snapListMsg{err: err}
		}
		slices.Reverse(metas)
		return snapListMsg{metas: metas}
	}
}

func snapOpenCmd(a *App, meta snapshot.Meta) tea.Cmd {
	return func() tea.Msg {
		store, err := a.browseSnapshotStore()
		if err != nil {
			return snapOpenMsg{err: err}
		}
		files, err := store.Files(meta.ID)
		return snapOpenMsg{meta: meta, files: files, store: store, err: err}
	}
}

// browseSnapshotStore opens the configured snapshot store. It re-reads the
// config each time so a store is never held across a config change.
func (a *App) browseSnapshotStore() (*snapshot.Store, error) {
	cfg, err := a.readConfig()
	if err != nil {
		return nil, err
	}
	return a.snapshotStore(cfg)
}

// hashCmd stats and hashes every file off the render path; the first frame
// draws immediately and the change markers fill in when this returns.
// In snapshot mode "changed" means the home file differs from the snapshot.
func hashCmd(rows []row) tea.Cmd {
	targets := make(map[string]row, len(rows))
	for _, r := range rows {
		if r.kind == rowFile {
			targets[r.key] = r
		}
	}
	return func() tea.Msg {
		changed := make(map[string]bool, len(targets))
		for key, r := range targets {
			if r.checksum != "" {
				changed[key] = snapshotChanged(r.pair.Dst, r.checksum)
				continue
			}
			changed[key] = computeChanged(&r.pair)
		}
		return changedMsg{changed: changed}
	}
}

// snapshotChanged reports whether the live home file differs from the snapshot
// version. A missing home file counts as changed: restoring it is the point.
func snapshotChanged(dst, checksum string) bool {
	if !isExist(dst) {
		return true
	}
	current, err := hash.GetHash(dst)
	if err != nil {
		return true
	}
	return current != checksum
}

func (m *browseModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, hashCmd(m.rows))
}

func (m *browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizePanes()
		m.renderPreview()
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.MouseWheelMsg, tea.MouseClickMsg:
		return m, m.handleMouse(msg.(tea.MouseMsg))

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if m.busy == "" {
			return m, nil
		}
		return m, cmd

	case changedMsg:
		for i := range m.rows {
			if c, ok := msg.changed[m.rows[i].key]; ok {
				m.rows[i].changed = c
			}
		}
		return m, nil

	case actionDoneMsg:
		return m, m.finishAction(msg)

	case rescanMsg:
		m.busy = ""
		if msg.err != nil {
			m.status = fmt.Sprintf("reload failed: %v", msg.err)
			return m, nil
		}
		m.profile = msg.profile
		m.source = sourceRepo
		m.setRows(buildRows(msg.pairs, m.cfg.Path))
		m.renderPreview()
		return m, hashCmd(m.rows)

	case snapListMsg:
		m.busy = ""
		if msg.err != nil {
			m.overlay = overlayNone
			m.status = fmt.Sprintf("snapshots: %v", msg.err)
			return m, nil
		}
		m.snapshots = msg.metas
		if m.snapIdx >= len(m.snapshots) {
			m.snapIdx = max(len(m.snapshots)-1, 0)
		}
		// The snapshot being browsed may have just been deleted.
		if m.source == sourceSnapshot && !slices.ContainsFunc(msg.metas, func(x snapshot.Meta) bool {
			return x.ID == m.snapMeta.ID
		}) {
			return m, m.leaveSnapshot()
		}
		return m, nil

	case snapOpenMsg:
		m.busy = ""
		m.overlay = overlayNone
		if msg.err != nil {
			m.status = fmt.Sprintf("open snapshot: %v", msg.err)
			return m, nil
		}
		m.snapStore = msg.store
		m.snapMeta = msg.meta
		m.source = sourceSnapshot
		m.setRows(buildSnapshotRows(msg.files, m.app.HomeDir))
		m.renderPreview()
		return m, hashCmd(m.rows)
	}

	return m, nil
}

// finishAction folds an action result back into the model and refreshes the
// rows it touched.
func (m *browseModel) finishAction(msg actionDoneMsg) tea.Cmd {
	m.busy = ""
	if msg.err != nil {
		m.status = fmt.Sprintf("%s failed: %v", msg.verb, msg.err)
		return nil
	}
	m.status = ""

	if msg.verb == "pull" {
		// The repository contents may have changed underneath us.
		m.busy = "reloading"
		return tea.Batch(m.spin.Tick, rescanCmd(m.app, m.cfg, m.profile))
	}

	touched := make(map[string]bool, len(msg.keys))
	for _, k := range msg.keys {
		touched[k] = true
		delete(m.marked, k)
	}
	for i := range m.rows {
		r := &m.rows[i]
		if r.kind == rowFile && touched[r.key] {
			r.changed = computeChanged(&r.pair)
		}
	}
	m.renderPreview()
	return nil
}

// leaveSnapshot returns the tree to the tracked dotfiles.
func (m *browseModel) leaveSnapshot() tea.Cmd {
	m.source = sourceRepo
	m.snapStore = nil
	m.snapMeta = snapshot.Meta{}
	pairs, err := m.app.collectTracked(m.cfg, m.profile)
	if err != nil {
		m.status = fmt.Sprintf("reload failed: %v", err)
		return nil
	}
	m.setRows(buildRows(dotfile.Merge(pairs), m.cfg.Path))
	m.renderPreview()
	return hashCmd(m.rows)
}

// start kicks off a background action, refusing while one is already running.
func (m *browseModel) start(busy string, cmd tea.Cmd) tea.Cmd {
	if m.busy != "" || cmd == nil {
		return nil
	}
	m.busy = busy
	m.status = ""
	return tea.Batch(m.spin.Tick, cmd)
}

// renderPreview refills the right pane from the row under the cursor.
func (m *browseModel) renderPreview() {
	m.preview.SetYOffset(0)
	m.preview.SetXOffset(0)

	r := m.current()
	if r == nil || r.kind != rowFile {
		m.preview.SetContent("")
		return
	}
	m.preview.SetContent(m.paneBody(r))
}

func (m *browseModel) paneBody(r *row) string {
	if r.checksum != "" {
		return m.snapshotPaneBody(r)
	}

	p := r.pair
	repoContent, err := os.ReadFile(p.Src)
	if err != nil {
		return m.st.err.Render(fmt.Sprintf("error: %v", err))
	}

	if m.mode == viewPreview {
		if bytes.Contains(repoContent, []byte{0}) {
			return m.st.muted.Render("(binary file)")
		}
		return sanitize(string(repoContent))
	}

	var homeContent []byte
	if isExist(p.Dst) {
		homeContent, err = os.ReadFile(p.Dst)
		if err != nil {
			return m.st.err.Render(fmt.Sprintf("error: %v", err))
		}
	}
	if bytes.Equal(repoContent, homeContent) {
		return m.st.ok.Render("file is up to date")
	}
	if bytes.Contains(repoContent, []byte{0}) || bytes.Contains(homeContent, []byte{0}) {
		return m.st.muted.Render("binary files differ")
	}

	rel := m.homeRel(p.Dst)
	edits := myers.ComputeEdits(span.URIFromPath(p.Src), sanitize(string(homeContent)), sanitize(string(repoContent)))
	unified := gotextdiff.ToUnified(filepath.Join("a", rel), filepath.Join("b", rel), sanitize(string(homeContent)), edits)
	return colorizeDiffANSI(fmt.Sprint(unified))
}

// snapshotPaneBody renders a snapshot row: the raw captured content, or a diff
// of the live home file against it. The direction matches repo mode, so green
// "+" lines are what a restore would give you.
func (m *browseModel) snapshotPaneBody(r *row) string {
	if m.snapStore == nil {
		return m.st.err.Render("snapshot store is not open")
	}

	rc, err := m.snapStore.Cat(m.ctx, r.checksum)
	if err != nil {
		return m.st.err.Render(fmt.Sprintf("error: %v", err))
	}
	snapContent, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return m.st.err.Render(fmt.Sprintf("error: %v", err))
	}

	if m.mode == viewPreview {
		if bytes.Contains(snapContent, []byte{0}) {
			return m.st.muted.Render("(binary file)")
		}
		return sanitize(string(snapContent))
	}

	var homeContent []byte
	if isExist(r.pair.Dst) {
		homeContent, err = os.ReadFile(r.pair.Dst)
		if err != nil {
			return m.st.err.Render(fmt.Sprintf("error: %v", err))
		}
	} else {
		return m.st.warn.Render("file no longer exists in ~/; restoring recreates it")
	}

	if bytes.Equal(homeContent, snapContent) {
		return m.st.ok.Render("file matches the snapshot")
	}
	if bytes.Contains(homeContent, []byte{0}) || bytes.Contains(snapContent, []byte{0}) {
		return m.st.muted.Render("binary files differ")
	}

	rel := m.homeRel(r.pair.Dst)
	now, snapped := sanitize(string(homeContent)), sanitize(string(snapContent))
	edits := myers.ComputeEdits(span.URIFromPath(r.pair.Dst), now, snapped)
	unified := gotextdiff.ToUnified(
		filepath.Join("a", rel)+" (now)",
		filepath.Join("b", rel)+" (snapshot)",
		now, edits,
	)
	return colorizeDiffANSI(fmt.Sprint(unified))
}

func (m *browseModel) homeRel(dst string) string {
	rel, err := filepath.Rel(m.app.HomeDir, dst)
	if err != nil {
		return dst
	}
	return rel
}

// sanitize makes arbitrary file bytes safe to hand to the renderer: control
// characters would otherwise move the cursor or inject styling, and unexpanded
// tabs break the viewport's width accounting.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	col := 0
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
			col = 0
		case r == '\t':
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			// Drop: C0 and C1 control characters, including ANSI escapes.
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

// computeChanged reports whether the repo copy differs from the home copy.
func computeChanged(p *dotfile.Pair) bool {
	srcFi, err := os.Lstat(p.Src)
	if err != nil {
		return false
	}
	if srcFi.Mode()&os.ModeSymlink != 0 {
		srcTarget, err := os.Readlink(p.Src)
		if err != nil {
			return false
		}
		dstFi, err := os.Lstat(p.Dst)
		if err != nil || dstFi.Mode()&os.ModeSymlink == 0 {
			return true
		}
		dstTarget, err := os.Readlink(p.Dst)
		if err != nil {
			return true
		}
		return srcTarget != dstTarget
	}
	if !isExist(p.Dst) {
		return true
	}
	srcHash, err := hash.GetHash(p.Src)
	if err != nil {
		return false
	}
	dstHash, err := hash.GetHash(p.Dst)
	if err != nil {
		return true
	}
	return srcHash != dstHash
}

// listProfiles returns the profile directories under the repository.
func (m *browseModel) listProfiles() []string {
	entries, err := os.ReadDir(filepath.Join(m.cfg.Path, "profiles"))
	if err != nil {
		return nil
	}
	var profiles []string
	for _, e := range entries {
		if e.IsDir() {
			profiles = append(profiles, e.Name())
		}
	}
	return profiles
}
