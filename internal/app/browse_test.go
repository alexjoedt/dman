package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"

	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/snapshot"
)

// newTestBrowse builds a model over a throwaway repo containing rels.
func newTestBrowse(t *testing.T, rels ...string) *browseModel {
	t.Helper()

	repo := t.TempDir()
	home := filepath.Join(repo, "home")

	pairs := make([]dotfile.Pair, 0, len(rels))
	for _, rel := range rels {
		src := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, dotfile.Pair{Src: src, Dst: filepath.Join(home, rel)})
	}

	m := newBrowseModel(context.Background(), &App{HomeDir: home}, &Config{Path: repo}, "default", pairs)
	m.width, m.height = 100, 24
	m.resizePanes()
	return m
}

func keys(rows []row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.key
	}
	return out
}

func (m *browseModel) visibleKeys() []string {
	out := make([]string, len(m.visible))
	for i, idx := range m.visible {
		out[i] = m.rows[idx].key
	}
	return out
}

func TestBuildRowsDepthFirst(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_config/nvim/init.lua", "dot_config/git/config")

	want := []string{
		"dot_config",
		"dot_config/git",
		"dot_config/git/config",
		"dot_config/nvim",
		"dot_config/nvim/init.lua",
		"dot_zshrc",
	}
	if got := keys(m.rows); !equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}

	wantDepth := []int{0, 1, 2, 1, 2, 0}
	for i, d := range wantDepth {
		if m.rows[i].depth != d {
			t.Errorf("rows[%d] (%s) depth = %d, want %d", i, m.rows[i].key, m.rows[i].depth, d)
		}
	}

	wantKind := []rowKind{rowDir, rowDir, rowFile, rowDir, rowFile, rowFile}
	for i, k := range wantKind {
		if m.rows[i].kind != k {
			t.Errorf("rows[%d] (%s) kind = %d, want %d", i, m.rows[i].key, m.rows[i].kind, k)
		}
	}
}

func TestVisibleFollowsExpansion(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_config/nvim/init.lua", "dot_config/git/config")

	// Everything starts collapsed, so only the top level shows.
	if got, want := m.visibleKeys(), []string{"dot_config", "dot_zshrc"}; !equal(got, want) {
		t.Fatalf("collapsed = %v, want %v", got, want)
	}

	m.expanded["dot_config"] = true
	m.recomputeVisible()
	want := []string{"dot_config", "dot_config/git", "dot_config/nvim", "dot_zshrc"}
	if got := m.visibleKeys(); !equal(got, want) {
		t.Fatalf("one level = %v, want %v", got, want)
	}

	m.expanded["dot_config/nvim"] = true
	m.recomputeVisible()
	want = []string{"dot_config", "dot_config/git", "dot_config/nvim", "dot_config/nvim/init.lua", "dot_zshrc"}
	if got := m.visibleKeys(); !equal(got, want) {
		t.Fatalf("two levels = %v, want %v", got, want)
	}
}

func TestFilterFlattensToMatchingFiles(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_config/nvim/init.lua", "dot_config/git/config")

	m.filter = "init"
	m.recomputeVisible()
	if got, want := m.visibleKeys(), []string{"dot_config/nvim/init.lua"}; !equal(got, want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}

	// Matching is on the whole path, so "config" reaches everything under
	// dot_config/ regardless of expansion, and never yields a dir row.
	m.filter = "config"
	m.recomputeVisible()
	want := []string{"dot_config/git/config", "dot_config/nvim/init.lua"}
	if got := m.visibleKeys(); !equal(got, want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}
	for _, idx := range m.visible {
		if m.rows[idx].kind != rowFile {
			t.Errorf("filter returned a directory row: %q", m.rows[idx].key)
		}
	}

	m.filter = ""
	m.recomputeVisible()
	if got, want := m.visibleKeys(), []string{"dot_config", "dot_zshrc"}; !equal(got, want) {
		t.Fatalf("cleared = %v, want %v", got, want)
	}
}

func TestCursorClampsAndOffsetFollows(t *testing.T) {
	rels := make([]string, 40)
	for i := range rels {
		rels[i] = "dot_f" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	m := newTestBrowse(t, rels...)

	m.moveTo(-5)
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
	if m.offset != 0 {
		t.Errorf("offset = %d, want 0", m.offset)
	}

	last := len(m.visible) - 1
	m.moveTo(999)
	if m.cursor != last {
		t.Errorf("cursor = %d, want %d", m.cursor, last)
	}
	h := m.treeInnerHeight()
	if m.cursor < m.offset || m.cursor >= m.offset+h {
		t.Errorf("cursor %d outside window [%d,%d)", m.cursor, m.offset, m.offset+h)
	}

	m.moveTo(0)
	if m.offset != 0 {
		t.Errorf("offset = %d after returning to the top, want 0", m.offset)
	}
}

func TestMarksSurviveRebuild(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_config/nvim/init.lua")

	m.expanded["dot_config"] = true
	m.expanded["dot_config/nvim"] = true
	m.recomputeVisible()

	// Put the cursor on init.lua and mark it.
	m.moveTo(2)
	if got := m.currentKey(); got != "dot_config/nvim/init.lua" {
		t.Fatalf("cursor on %q, want dot_config/nvim/init.lua", got)
	}
	m.toggleCurrent()
	if !m.marked["dot_config/nvim/init.lua"] {
		t.Fatal("space did not mark the file")
	}
	if m.countMarked() != 1 {
		t.Fatalf("countMarked = %d, want 1", m.countMarked())
	}

	// A pull or profile switch rebuilds the rows; the mark must stay put.
	m.setRows(buildRows([]dotfile.Pair{
		{Src: m.rows[2].pair.Src, Dst: m.rows[2].pair.Dst},
		{Src: m.rows[3].pair.Src, Dst: m.rows[3].pair.Dst},
	}, m.cfg.Path))

	if !m.marked["dot_config/nvim/init.lua"] {
		t.Error("mark was dropped by the rebuild")
	}
	if got := m.currentKey(); got != "dot_config/nvim/init.lua" {
		t.Errorf("cursor moved to %q across the rebuild", got)
	}
}

func TestSpaceTogglesDirectoryInsteadOfMarking(t *testing.T) {
	m := newTestBrowse(t, "dot_config/nvim/init.lua")

	if got := m.currentKey(); got != "dot_config" {
		t.Fatalf("cursor on %q, want dot_config", got)
	}
	m.toggleCurrent()
	if !m.expanded["dot_config"] {
		t.Error("space did not expand the directory")
	}
	if m.countMarked() != 0 {
		t.Error("space marked a directory")
	}
	m.toggleCurrent()
	if m.expanded["dot_config"] {
		t.Error("space did not collapse the directory")
	}
}

func TestActionsAreSerialized(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc")

	noop := func() tea.Msg { return nil }
	if cmd := m.start("applying", noop); cmd == nil {
		t.Fatal("first action was refused")
	}
	if m.busy != "applying" {
		t.Fatalf("busy = %q, want applying", m.busy)
	}
	if cmd := m.start("saving", noop); cmd != nil {
		t.Error("a second action started while one was in flight")
	}
	if m.busy != "applying" {
		t.Errorf("busy = %q, want the first action to still own it", m.busy)
	}

	m.Update(actionDoneMsg{verb: "apply", keys: []string{"dot_zshrc"}})
	if m.busy != "" {
		t.Errorf("busy = %q after completion, want empty", m.busy)
	}
	if cmd := m.start("saving", noop); cmd == nil {
		t.Error("no action could start after the first finished")
	}
}

func TestActionErrorSurfacesAndClearsBusy(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc")
	m.busy = "applying"

	m.Update(actionDoneMsg{verb: "apply", err: errors.New("boom")})

	if m.busy != "" {
		t.Errorf("busy = %q, want empty", m.busy)
	}
	if !strings.Contains(m.status, "apply failed") || !strings.Contains(m.status, "boom") {
		t.Errorf("status = %q, want it to mention the failed apply", m.status)
	}
}

func TestActionClearsMarksItTouched(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_vimrc")

	m.marked["dot_zshrc"] = true
	m.marked["dot_vimrc"] = true
	m.busy = "applying"

	m.Update(actionDoneMsg{verb: "apply", keys: []string{"dot_zshrc"}})

	if m.marked["dot_zshrc"] {
		t.Error("applied file is still marked")
	}
	if !m.marked["dot_vimrc"] {
		t.Error("an untouched file lost its mark")
	}
}

func TestTargetsPrefersMarksOverCursor(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_vimrc")

	// Nothing marked: the row under the cursor is the target.
	dsts, ks := m.targets()
	if len(dsts) != 1 || ks[0] != "dot_vimrc" {
		t.Fatalf("targets = %v / %v, want the cursor row dot_vimrc", dsts, ks)
	}

	m.marked["dot_zshrc"] = true
	_, ks = m.targets()
	if !equal(ks, []string{"dot_zshrc"}) {
		t.Fatalf("targets = %v, want only the marked file", ks)
	}
}

func TestSanitizeStripsControlSequences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"escape sequence", "a\x1b[31mred\x1b[0mb", "a[31mred[0mb"},
		{"carriage return", "a\rb", "ab"},
		{"bell and backspace", "a\x07\x08b", "ab"},
		{"c1 control", "a\u009bb", "ab"},
		{"invalid byte becomes the replacement rune", "a\x9bb", "a\ufffdb"},
		{"delete", "a\x7fb", "ab"},
		{"newlines survive", "a\nb\n", "a\nb\n"},
		{"tabs expand to the next stop", "\tx", "    x"},
		{"tabs are column aware", "ab\tx", "ab  x"},
		{"tabs reset each line", "abc\n\tx", "abc\n    x"},
		{"utf8 survives", "héllo ✓", "héllo ✓"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitize(tt.in); got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestColorizeDiffANSI(t *testing.T) {
	in := "--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n+new\n ctx\n"
	want := "\033[1m--- a/f\n\033[0m" +
		"\033[1m+++ b/f\n\033[0m" +
		"\033[36m@@ -1 +1 @@\n\033[0m" +
		"\033[31m-old\n\033[0m" +
		"\033[32m+new\n\033[0m" +
		" ctx\n"

	if got := colorizeDiffANSI(in); got != want {
		t.Errorf("colorizeDiffANSI() = %q, want %q", got, want)
	}
}

func TestPaneGeometryFitsTheTerminal(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc")

	tests := []struct{ w, h int }{{100, 24}, {200, 60}, {79, 30}, {40, 12}}
	for _, tt := range tests {
		m.width, m.height = tt.w, tt.h
		g := m.geometry()

		if g.stacked != (tt.w < stackWidth) {
			t.Errorf("%dx%d: stacked = %v", tt.w, tt.h, g.stacked)
		}
		if g.stacked {
			if g.treeH+g.prevH > m.height-2 {
				t.Errorf("%dx%d: panes %d+%d overflow %d content rows", tt.w, tt.h, g.treeH, g.prevH, m.height-2)
			}
			continue
		}
		if g.treeW+g.prevW != tt.w {
			t.Errorf("%dx%d: pane widths %d+%d != %d", tt.w, tt.h, g.treeW, g.prevW, tt.w)
		}
		if g.treeH != tt.h-2 {
			t.Errorf("%dx%d: pane height = %d, want %d", tt.w, tt.h, g.treeH, tt.h-2)
		}
	}
}

func TestTreeLineIsExactlyPaneWidth(t *testing.T) {
	m := newTestBrowse(t, "dot_a", "dot_a_very_long_dotfile_name_that_will_not_fit")

	for _, r := range m.rows {
		for _, w := range []int{20, 40, 8} {
			got := len([]rune(stripANSI(m.treeLine(r, false, w))))
			if got != w {
				t.Errorf("treeLine(%q, w=%d) rendered %d cells", r.key, w, got)
			}
		}
	}
}

// stripANSI removes SGR sequences so tests can measure printable width.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestViewFillsTheTerminalExactly(t *testing.T) {
	m := newTestBrowse(t,
		"dot_zshrc", "dot_vimrc",
		"dot_config/nvim/init.lua", "dot_config/git/config",
	)
	m.expanded["dot_config"] = true
	m.recomputeVisible()

	m.snapshots = []snapshot.Meta{
		{ID: "a", CreatedAt: time.Now(), FileCount: 12, Message: "auto: before apply"},
		{ID: "b", CreatedAt: time.Now(), FileCount: 3},
	}
	m.snapMeta = m.snapshots[0]

	sizes := []struct{ w, h int }{{96, 20}, {120, 40}, {80, 24}, {79, 30}, {60, 16}}
	overlays := []overlayKind{overlayNone, overlayHelp, overlayConfirm, overlaySnapshots, overlayProfiles}
	sources := []sourceKind{sourceRepo, sourceSnapshot}

	for _, src := range sources {
		for _, o := range overlays {
			m.source = src
			m.overlay = o
			m.confirmText = "Save 3 file(s) to the repo?"
			m.profiles = []string{"default", "work"}
			for _, s := range sizes {
				mm, _ := m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
				lines := strings.Split(mm.(*browseModel).View().Content, "\n")

				if len(lines) != s.h {
					t.Errorf("overlay %d at %dx%d: %d lines, want %d", o, s.w, s.h, len(lines), s.h)
				}
				for i, l := range lines {
					w := lipgloss.Width(l)
					// The compositor trims trailing blanks, so an overlaid frame
					// may come up short; overflowing is what breaks the layout.
					if w > s.w || (o == overlayNone && w != s.w) {
						t.Errorf("source %d overlay %d at %dx%d: line %d is %d cells, want %d", src, o, s.w, s.h, i, w, s.w)
					}
				}
			}
		}
	}
}

// enterSnapshot puts the model into snapshot mode over the given manifest.
func enterSnapshot(t *testing.T, m *browseModel, files []snapshot.File) {
	t.Helper()
	mm, _ := m.Update(snapOpenMsg{
		meta:  snapshot.Meta{ID: "20260101-120000.000000000", FileCount: len(files)},
		files: files,
	})
	if mm.(*browseModel).source != sourceSnapshot {
		t.Fatalf("still in repo mode; status = %q", m.status)
	}
}

func TestBuildSnapshotRowsMatchesRepoShape(t *testing.T) {
	m := newTestBrowse(t)
	m.rows = buildSnapshotRows([]snapshot.File{
		{Path: ".zshrc", Checksum: "aaa"},
		{Path: ".config/nvim/init.lua", Checksum: "bbb"},
		{Path: ".config/git/config", Checksum: "ccc"},
	}, "/home/u")

	want := []string{
		".config",
		".config/git",
		".config/git/config",
		".config/nvim",
		".config/nvim/init.lua",
		".zshrc",
	}
	if got := keys(m.rows); !equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}

	for _, r := range m.rows {
		if r.kind != rowFile {
			continue
		}
		if r.checksum == "" {
			t.Errorf("%q has no checksum", r.key)
		}
		if want := filepath.Join("/home/u", r.key); r.pair.Dst != want {
			t.Errorf("%q Dst = %q, want %q", r.key, r.pair.Dst, want)
		}
		if r.pair.Src != "" {
			t.Errorf("%q has a repo Src: %q", r.key, r.pair.Src)
		}
	}
}

func TestSnapshotModePreservesRepoMarks(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc", "dot_vimrc")
	m.marked["dot_zshrc"] = true

	enterSnapshot(t, m, []snapshot.File{{Path: ".zshrc", Checksum: "aaa"}})

	// Snapshot keys are home-relative, so they never collide with repo keys.
	if m.countMarked() != 0 {
		t.Errorf("countMarked = %d in snapshot mode, want 0", m.countMarked())
	}
	m.marked[".zshrc"] = true

	m.leaveSnapshot()

	if m.source != sourceRepo {
		t.Fatal("did not return to repo mode")
	}
	if !m.marked["dot_zshrc"] {
		t.Error("repo mark was lost across the round trip")
	}
	if m.countMarked() != 1 {
		t.Errorf("countMarked = %d back in repo mode, want 1", m.countMarked())
	}
}

func TestRepoOnlyActionsRefusedInSnapshotMode(t *testing.T) {
	for _, key := range []string{"S", "P", "r"} {
		t.Run(key, func(t *testing.T) {
			m := newTestBrowse(t, "dot_zshrc")
			enterSnapshot(t, m, []snapshot.File{{Path: ".zshrc", Checksum: "aaa"}})

			if cmd := m.handleTreeKey(key); cmd != nil {
				t.Errorf("%q started an action in snapshot mode", key)
			}
			if !strings.Contains(m.status, "browsing a snapshot") {
				t.Errorf("status = %q, want it to explain the refusal", m.status)
			}
			if m.busy != "" {
				t.Errorf("busy = %q, want empty", m.busy)
			}
		})
	}
}

func TestRestoreAlwaysConfirmsEvenForOneFile(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc")
	enterSnapshot(t, m, []snapshot.File{{Path: ".zshrc", Checksum: "aaa"}})

	if cmd := m.handleTreeKey("enter"); cmd != nil {
		t.Error("restore started without confirming")
	}
	if m.overlay != overlayConfirm {
		t.Fatalf("overlay = %d, want overlayConfirm", m.overlay)
	}
	if !strings.Contains(m.confirmText, "Restore 1 file") {
		t.Errorf("confirmText = %q", m.confirmText)
	}
	if m.busy != "" {
		t.Errorf("busy = %q before confirming, want empty", m.busy)
	}

	// Cancelling must not start anything.
	m.handleOverlayKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.overlay != overlayNone || m.busy != "" || m.confirmCmd != nil {
		t.Errorf("cancel left overlay=%d busy=%q cmd=%v", m.overlay, m.busy, m.confirmCmd != nil)
	}
}

// isQuit reports whether a command would end the program. Other commands (a
// hash refresh, a spinner tick) are not quits, so identity is not enough.
func isQuit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestEscUnwindsFilterThenSnapshotThenQuits(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc")
	enterSnapshot(t, m, []snapshot.File{{Path: ".zshrc", Checksum: "aaa"}})

	esc := tea.KeyPressMsg{Code: tea.KeyEscape}

	m.filter = "zsh"
	m.recomputeVisible()
	if isQuit(t, m.handleKey(esc)) {
		t.Error("esc quit while a filter was active")
	}
	if m.filter != "" {
		t.Error("esc did not clear the filter first")
	}
	if m.source != sourceSnapshot {
		t.Error("esc left snapshot mode before clearing the filter")
	}

	if isQuit(t, m.handleKey(esc)) {
		t.Error("esc quit instead of leaving snapshot mode")
	}
	if m.source != sourceRepo {
		t.Fatal("esc did not leave snapshot mode")
	}

	if !isQuit(t, m.handleKey(esc)) {
		t.Error("esc did not quit from repo mode")
	}
}

func TestSnapshotDeletedWhileBrowsingFallsBackToRepo(t *testing.T) {
	m := newTestBrowse(t, "dot_zshrc")
	enterSnapshot(t, m, []snapshot.File{{Path: ".zshrc", Checksum: "aaa"}})

	// The index comes back without the snapshot being browsed.
	m.Update(snapListMsg{metas: []snapshot.Meta{{ID: "some-other-id"}}})

	if m.source != sourceRepo {
		t.Error("kept browsing a snapshot that no longer exists")
	}
}
