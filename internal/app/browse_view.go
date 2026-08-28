package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ANSI palette indices, so the TUI inherits whatever theme the terminal uses.
var (
	colRed    = lipgloss.Color("1")
	colGreen  = lipgloss.Color("2")
	colYellow = lipgloss.Color("3")
	colBlue   = lipgloss.Color("4")
	colCyan   = lipgloss.Color("6")
	colGray   = lipgloss.Color("8")
)

type styles struct {
	brand   lipgloss.Style
	muted   lipgloss.Style
	ok      lipgloss.Style
	err     lipgloss.Style
	warn    lipgloss.Style
	accent  lipgloss.Style
	title   lipgloss.Style
	dir     lipgloss.Style
	mark    lipgloss.Style
	changed lipgloss.Style
	cursor  lipgloss.Style
	pane    lipgloss.Style
	focused lipgloss.Style
	dialog  lipgloss.Style
	key     lipgloss.Style
}

func newStyles() styles {
	pane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colGray).
		Padding(0, 1)

	return styles{
		brand:   lipgloss.NewStyle().Bold(true),
		muted:   lipgloss.NewStyle().Foreground(colGray),
		ok:      lipgloss.NewStyle().Foreground(colGreen),
		err:     lipgloss.NewStyle().Foreground(colRed),
		warn:    lipgloss.NewStyle().Foreground(colYellow),
		accent:  lipgloss.NewStyle().Foreground(colGreen),
		title:   lipgloss.NewStyle().Foreground(colCyan).Bold(true),
		dir:     lipgloss.NewStyle().Foreground(colBlue),
		mark:    lipgloss.NewStyle().Foreground(colYellow),
		changed: lipgloss.NewStyle().Foreground(colRed),
		cursor:  lipgloss.NewStyle().Reverse(true),
		pane:    pane,
		focused: pane.BorderForeground(colCyan),
		dialog: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colCyan).
			Padding(1, 2),
		key: lipgloss.NewStyle().Foreground(colYellow),
	}
}

// geometry is the pixel-free layout of the two panes for the current terminal
// size, focus, and accordion setting.
type geometry struct {
	stacked bool
	treeX   int
	treeY   int
	treeW   int
	treeH   int
	prevX   int
	prevY   int
	prevW   int
	prevH   int
}

// paneInnerH is the number of body rows a pane of total height h can draw:
// two border rows and one title row come off the top.
func paneInnerH(h int) int { return max(h-3, 0) }

// paneInnerW is the usable text width of a pane of total width w, after two
// border columns and two padding columns.
func paneInnerW(w int) int { return max(w-4, 0) }

func (m *browseModel) geometry() geometry {
	contentH := max(m.height-2, 4) // header and status take one row each

	wt, wp := 1, 2
	if m.accordion {
		if m.focus == paneTree {
			wt *= 2
		} else {
			wp *= 2
		}
	}

	g := geometry{stacked: m.width < stackWidth}
	if g.stacked {
		g.treeW, g.prevW = m.width, m.width
		// A pane's box needs 3 rows (borders and title), and together the
		// panes must never claim more rows than the content area holds.
		const minPane = 3
		g.treeH = max(contentH*wt/(wt+wp), minPane)
		if g.treeH > contentH-minPane {
			g.treeH = contentH - minPane
		}
		g.prevH = contentH - g.treeH
		g.treeY = 1
		g.prevY = 1 + g.treeH
		return g
	}

	g.treeH, g.prevH = contentH, contentH
	g.treeW = max(m.width*wt/(wt+wp), 16)
	g.prevW = max(m.width-g.treeW, 16)
	g.treeY, g.prevY = 1, 1
	g.prevX = g.treeW
	return g
}

// resizePanes pushes the current geometry into the viewport and keeps the
// cursor on screen. Call it whenever the size, focus, or weights change.
func (m *browseModel) resizePanes() {
	g := m.geometry()
	m.preview.SetWidth(paneInnerW(g.prevW))
	m.preview.SetHeight(paneInnerH(g.prevH))
	m.preview.FillHeight = true
	m.syncOffset()
}

func (m *browseModel) treeInnerHeight() int {
	return max(paneInnerH(m.geometry().treeH), 1)
}

// syncOffset scrolls the tree pane just far enough to keep the cursor visible.
func (m *browseModel) syncOffset() {
	h := m.treeInnerHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if maxOffset := len(m.visible) - h; m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// pointInTree reports whether a mouse position falls in the tree pane. The
// axis it tests depends on how the panes are arranged.
func (m *browseModel) pointInTree(x, y int) bool {
	g := m.geometry()
	if g.stacked {
		return y >= g.treeY && y < g.treeY+g.treeH
	}
	return x >= g.treeX && x < g.treeX+g.treeW
}

// rowAt maps a screen row to an index into m.visible.
func (m *browseModel) rowAt(y int) (int, bool) {
	g := m.geometry()
	first := g.treeY + 2 // top border, then the pane title
	if y < first || y >= first+paneInnerH(g.treeH) {
		return 0, false
	}
	idx := m.offset + (y - first)
	if idx < 0 || idx >= len(m.visible) {
		return 0, false
	}
	return idx, true
}

func (m *browseModel) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	// Stacked panes need 3 rows each plus header and status.
	if m.width < 4 || m.height < 6 || (m.width < stackWidth && m.height < 8) {
		v.SetContent("terminal too small")
		return v
	}

	base := m.baseView()
	if m.overlay == overlayNone {
		v.SetContent(base)
		return v
	}

	dialog := m.overlayView()
	root := lipgloss.NewLayer(base).ID("base")
	root.AddLayers(lipgloss.NewLayer(dialog).
		X(max((m.width-lipgloss.Width(dialog))/2, 0)).
		Y(max((m.height-lipgloss.Height(dialog))/2, 0)).
		Z(1).
		ID("dialog"))
	v.SetContent(lipgloss.NewCompositor(root).Render())
	return v
}

func (m *browseModel) baseView() string {
	g := m.geometry()

	tree := m.renderPane(m.treeTitle(), m.treeLines(g), g.treeW, g.treeH, m.focus == paneTree)
	preview := m.renderPane(m.previewTitle(), strings.Split(m.preview.View(), "\n"), g.prevW, g.prevH, m.focus == panePreview)

	body := lipgloss.JoinHorizontal(lipgloss.Top, tree, preview)
	if g.stacked {
		body = lipgloss.JoinVertical(lipgloss.Left, tree, preview)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), body, m.statusView())
}

// renderPane draws a bordered box of exactly w x h cells. Body lines are
// truncated and the block is padded to height, so the border never shifts.
func (m *browseModel) renderPane(title string, body []string, w, h int, focused bool) string {
	innerW, innerH := paneInnerW(w), paneInnerH(h)

	lines := make([]string, 0, innerH+1)
	lines = append(lines, m.st.title.Render(truncate(title, innerW)))
	for _, l := range body {
		if len(lines) > innerH {
			break
		}
		lines = append(lines, l)
	}
	for len(lines) <= innerH {
		lines = append(lines, "")
	}

	st := m.st.pane
	if focused {
		st = m.st.focused
	}
	// lipgloss counts the border inside Width, so this is the pane's full size.
	return st.Width(w).Render(strings.Join(lines, "\n"))
}

func (m *browseModel) treeTitle() string {
	if m.filtering || m.filter != "" {
		return "filter: " + m.filter + "▏"
	}
	if m.source == sourceSnapshot {
		return fmt.Sprintf("snapshot files (%d)", len(m.visible))
	}
	return fmt.Sprintf("files (%d)", len(m.visible))
}

func (m *browseModel) previewTitle() string {
	r := m.current()
	if r == nil || r.kind != rowFile {
		return "—"
	}
	kind := "diff"
	if m.mode == viewPreview {
		kind = "preview"
	}
	if m.source == sourceSnapshot {
		kind = "restore"
		if m.mode == viewPreview {
			kind = "snapshot"
		}
	}
	return fmt.Sprintf("%s: ~/%s", kind, m.homeRel(r.pair.Dst))
}

func (m *browseModel) treeLines(g geometry) []string {
	innerW, innerH := paneInnerW(g.treeW), paneInnerH(g.treeH)

	lines := make([]string, 0, innerH)
	for i := m.offset; i < len(m.visible) && len(lines) < innerH; i++ {
		lines = append(lines, m.treeLine(m.rows[m.visible[i]], i == m.cursor, innerW))
	}
	return lines
}

// treeLine renders one row as "<changed> <box> <name>", padded to width so the
// selection highlight spans the whole pane.
func (m *browseModel) treeLine(r row, selected bool, w int) string {
	label := r.label
	indent := strings.Repeat("  ", r.depth)
	if m.filter != "" {
		// A filter flattens the tree, so show the path instead of the indent.
		indent, label = "", r.key
	}

	dot := " "
	box := "   "
	switch {
	case r.kind == rowDir && m.expanded[r.key]:
		box = " ▾ "
	case r.kind == rowDir:
		box = " ▸ "
	default:
		box = "[ ]"
		if m.marked[r.key] {
			box = "[x]"
		}
		if r.changed {
			dot = "●"
		}
	}

	const prefixW = 6 // dot + space + box + space
	text := truncate(indent+label, w-prefixW)
	gap := strings.Repeat(" ", max(w-prefixW-lipgloss.Width(text), 0))

	if selected {
		return m.st.cursor.Render(fmt.Sprintf("%s %s %s%s", dot, box, text, gap))
	}

	if dot == "●" {
		dot = m.st.changed.Render(dot)
	}
	if m.marked[r.key] {
		box = m.st.mark.Render(box)
	}
	if r.kind == rowDir {
		text = m.st.dir.Render(text)
	}
	return fmt.Sprintf("%s %s %s%s", dot, box, text, gap)
}

func (m *browseModel) headerView() string {
	left := m.st.brand.Render("dman browse") + "  profile: " + m.st.accent.Render(m.profile)
	if m.source == sourceSnapshot {
		left = m.st.brand.Render("dman browse") + "  snapshot: " +
			m.st.warn.Render(m.snapMeta.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	right := m.st.muted.Render("d diff · p preview · space mark · ? help")

	return m.bar(left, right)
}

func (m *browseModel) statusView() string {
	var left string
	switch {
	case m.busy != "":
		left = m.spin.View() + " " + m.st.warn.Render(m.busy+"…")
	case m.status != "":
		left = m.st.err.Render(m.status)
	case m.countMarked() > 0:
		left = m.st.warn.Render(fmt.Sprintf("%d marked", m.countMarked()))
	default:
		left = m.st.muted.Render("nothing marked")
	}

	right := m.st.muted.Render("enter apply↓ · S save↑ · P pull · r profile · s snapshots · q quit")
	if m.source == sourceSnapshot {
		right = m.st.muted.Render("enter restore↑ · s snapshots · / filter · esc back · q quit")
	}
	return m.bar(left, right)
}

// bar lays out a one-line header or status bar: left-aligned text, right-
// aligned hints, always exactly m.width cells. The hints drop out first when
// the terminal is too narrow to hold both.
func (m *browseModel) bar(left, right string) string {
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return pad(" "+truncate(left, m.width-1), m.width)
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (m *browseModel) overlayView() string {
	// MaxWidth/MaxHeight clip ANSI-aware, so a dialog can never outgrow the
	// terminal and shove the layout around.
	dialog := m.st.dialog.MaxWidth(m.width).MaxHeight(m.height)

	switch m.overlay {
	case overlayHelp:
		return dialog.Render(m.helpText())

	case overlayConfirm:
		return dialog.Render(truncate(m.confirmText, m.width-6) + "\n\n" +
			m.st.muted.Render("y / enter  confirm     n / esc  cancel"))

	case overlaySnapshots:
		return dialog.Render(m.snapshotPickerText())

	case overlayProfiles:
		lines := []string{m.st.title.Render("select profile"), ""}
		width := 0
		for _, p := range m.profiles {
			width = max(width, len(p)+4)
		}
		for i, p := range m.profiles {
			marker := "  "
			if p == m.profile {
				marker = m.st.accent.Render("* ")
			}
			line := pad(marker+p, width)
			if i == m.profileIdx {
				line = m.st.cursor.Render(pad("  "+p, width))
			}
			lines = append(lines, line)
		}
		lines = append(lines, "", m.st.muted.Render("enter select · esc cancel"))
		return dialog.Render(strings.Join(lines, "\n"))
	}
	return ""
}

func (m *browseModel) snapshotPickerText() string {
	lines := []string{m.st.title.Render("snapshots"), ""}

	if len(m.snapshots) == 0 {
		lines = append(lines, m.st.muted.Render("no snapshots yet"))
	}

	// The date column is fixed width, so only the message needs trimming.
	msgWidth := max(m.width-40, 12)
	for i, s := range m.snapshots {
		text := fmt.Sprintf("%s  %3d files", s.CreatedAt.Local().Format("2006-01-02 15:04:05"), s.FileCount)
		if s.Message != "" {
			text += "  " + truncate(s.Message, msgWidth)
		}
		marker := "  "
		if m.source == sourceSnapshot && s.ID == m.snapMeta.ID {
			marker = "* "
		}
		if i == m.snapIdx {
			lines = append(lines, m.st.cursor.Render(marker+text))
			continue
		}
		lines = append(lines, m.st.muted.Render(marker)+text)
	}

	lines = append(lines, "", m.st.muted.Render("enter open · c create · x delete · esc close"))
	return strings.Join(lines, "\n")
}

func (m *browseModel) helpText() string {
	k := m.st.key.Render
	rows := [][2]string{
		{"↑ ↓  j k", "move"},
		{"→ ←  l h", "expand / collapse directory"},
		{"space", "mark file, or expand directory"},
		{"enter", "apply marked repo → ~/, or restore in snapshot mode"},
		{"S", "save marked ~/ → repo  (bulk confirms)"},
		{"d / p", "diff / raw content in the right pane"},
		{"P", "pull the dotfiles repo"},
		{"r", "switch profile"},
		{"s", "browse snapshots (esc returns)"},
		{"/", "filter files"},
		{"tab", "switch pane focus"},
		{"a", "toggle accordion sizing"},
		{"?", "this help"},
		{"q  ctrl+c", "quit"},
	}

	width := 0
	for _, r := range rows {
		width = max(width, lipgloss.Width(r[0]))
	}

	// 2 border + 4 padding + 2 indent + 3 separator columns of chrome.
	descWidth := m.width - width - 11

	lines := []string{m.st.title.Render("dman browse — keybindings"), ""}
	for _, r := range rows {
		lines = append(lines, "  "+k(pad(r[0], width))+"   "+truncate(r[1], descWidth))
	}
	lines = append(lines, "", m.st.muted.Render("press any key to close"))
	return strings.Join(lines, "\n")
}

// truncate clips s to width terminal cells. It measures display width, so
// double-width runes count as two cells, and it never breaks an ANSI escape.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func pad(s string, width int) string {
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
