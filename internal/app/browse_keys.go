package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

func (m *browseModel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return tea.Quit
	case "esc":
		// Unwind one layer at a time: filter, then snapshot mode, then quit.
		if m.filter != "" {
			m.filter = ""
			m.recomputeVisible()
			m.renderPreview()
			return nil
		}
		if m.source == sourceSnapshot {
			return m.leaveSnapshot()
		}
		return tea.Quit
	case "?":
		m.overlay = overlayHelp
		return nil
	case "tab":
		m.toggleFocus()
		m.resizePanes()
		return nil
	case "a":
		m.accordion = !m.accordion
		m.resizePanes()
		return nil
	case "/":
		m.filtering = true
		return nil
	case "s":
		m.snapIdx = 0
		m.overlay = overlaySnapshots
		return m.start("loading", snapListCmd(m.app))
	case "d":
		m.mode = viewDiff
		m.renderPreview()
		return nil
	case "p":
		m.mode = viewPreview
		m.renderPreview()
		return nil
	}

	if m.focus == panePreview {
		switch key {
		case " ", "enter", "S", "P", "r":
			// Actions target the selected file, so they work from either pane.
			return m.handleTreeKey(key)
		}
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return cmd
	}
	return m.handleTreeKey(key)
}

func (m *browseModel) toggleFocus() {
	if m.focus == paneTree {
		m.focus = panePreview
		return
	}
	m.focus = paneTree
}

func (m *browseModel) handleTreeKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup":
		m.moveCursor(-m.treeInnerHeight())
	case "pgdown":
		m.moveCursor(m.treeInnerHeight())
	case "home", "g":
		m.moveTo(0)
	case "end", "G":
		m.moveTo(len(m.visible) - 1)
	case "right", "l":
		m.setExpanded(true)
	case "left", "h":
		m.setExpanded(false)
	case " ":
		m.toggleCurrent()
	case "enter":
		if m.source == sourceSnapshot {
			return m.startRestore()
		}
		return m.startApply()
	case "S":
		if m.refuseInSnapshot("save") {
			return nil
		}
		return m.startSave()
	case "P":
		if m.refuseInSnapshot("pull") {
			return nil
		}
		return m.start("pulling", pullCmd(m.ctx, m.app))
	case "r":
		if m.refuseInSnapshot("switch profile") {
			return nil
		}
		m.profiles = m.listProfiles()
		if len(m.profiles) == 0 {
			m.status = "no profiles found"
			return nil
		}
		m.profileIdx = 0
		for i, p := range m.profiles {
			if p == m.profile {
				m.profileIdx = i
			}
		}
		m.overlay = overlayProfiles
	}
	return nil
}

// refuseInSnapshot reports whether an action is unavailable because a snapshot
// is being browsed, and says so in the status bar.
func (m *browseModel) refuseInSnapshot(action string) bool {
	if m.source != sourceSnapshot {
		return false
	}
	m.status = "cannot " + action + " while browsing a snapshot (esc to go back)"
	return true
}

// startRestore always confirms. Unlike save, no shift key stands in as a guard,
// and it overwrites live files in the home directory.
func (m *browseModel) startRestore() tea.Cmd {
	dsts, keys := m.targets()
	if len(dsts) == 0 {
		return nil
	}
	m.overlay = overlayConfirm
	m.confirmText = fmt.Sprintf("Restore %d file(s) from %s?  (snapshot → ~/)",
		len(dsts), m.snapMeta.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	m.confirmBusy = "restoring"
	m.confirmCmd = restoreCmd(m.ctx, m.app, m.snapMeta.ID, dsts, keys)
	m.confirmReturn = overlayNone
	return nil
}

func (m *browseModel) startApply() tea.Cmd {
	dsts, keys := m.targets()
	if len(dsts) == 0 {
		return nil
	}
	return m.start("applying", applyCmd(m.ctx, m.app, m.profile, dsts, keys))
}

func (m *browseModel) startSave() tea.Cmd {
	dsts, keys := m.targets()
	if len(dsts) == 0 {
		return nil
	}
	// A single file is guarded by the shift key alone; a bulk write back into
	// the repository asks first.
	if len(dsts) == 1 {
		return m.start("saving", saveCmd(m.ctx, m.app, m.profile, dsts, keys))
	}
	m.overlay = overlayConfirm
	m.confirmText = fmt.Sprintf("Save %d file(s) to the repo?  (~/  →  repo)", len(dsts))
	m.confirmBusy = "saving"
	m.confirmCmd = saveCmd(m.ctx, m.app, m.profile, dsts, keys)
	m.confirmReturn = overlayNone
	return nil
}

func (m *browseModel) moveCursor(delta int) {
	m.moveTo(m.cursor + delta)
}

func (m *browseModel) moveTo(idx int) {
	prev := m.cursor
	m.cursor = idx
	m.clampCursor()
	m.syncOffset()
	if m.cursor != prev {
		m.renderPreview()
	}
}

// setExpanded expands or collapses the directory under the cursor. On a file
// row it does nothing.
func (m *browseModel) setExpanded(want bool) {
	r := m.current()
	if r == nil || r.kind != rowDir || m.expanded[r.key] == want {
		return
	}
	m.expanded[r.key] = want
	m.recomputeVisible()
}

// toggleCurrent marks a file or expands a directory, matching space's dual
// role in the tree.
func (m *browseModel) toggleCurrent() {
	r := m.current()
	if r == nil {
		return
	}
	if r.kind == rowDir {
		m.expanded[r.key] = !m.expanded[r.key]
		m.recomputeVisible()
		return
	}
	if m.marked[r.key] {
		delete(m.marked, r.key)
		return
	}
	m.marked[r.key] = true
}

func (m *browseModel) handleFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
	case "enter":
		m.filtering = false
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
		}
	case "ctrl+c":
		return tea.Quit
	default:
		if s := msg.String(); len(s) == 1 && s >= " " {
			m.filter += s
		} else {
			return nil
		}
	}
	m.recomputeVisible()
	m.renderPreview()
	return nil
}

func (m *browseModel) handleOverlayKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}

	switch m.overlay {
	case overlayHelp:
		m.overlay = overlayNone

	case overlayConfirm:
		switch key {
		case "y", "Y", "enter":
			m.overlay = m.confirmReturn
			cmd := m.start(m.confirmBusy, m.confirmCmd)
			m.confirmCmd = nil
			return cmd
		case "n", "N", "q", "esc":
			m.overlay = m.confirmReturn
			m.confirmCmd = nil
		}

	case overlaySnapshots:
		switch key {
		case "up", "k":
			if m.snapIdx > 0 {
				m.snapIdx--
			}
		case "down", "j":
			if m.snapIdx < len(m.snapshots)-1 {
				m.snapIdx++
			}
		case "enter":
			if len(m.snapshots) == 0 {
				return nil
			}
			return m.start("opening", snapOpenCmd(m.app, m.snapshots[m.snapIdx]))
		case "c":
			return m.start("snapshotting", snapCreateCmd(m.ctx, m.app))
		case "x":
			if len(m.snapshots) == 0 {
				return nil
			}
			target := m.snapshots[m.snapIdx]
			m.overlay = overlayConfirm
			m.confirmText = fmt.Sprintf("Delete snapshot %s?  (%d file(s))",
				target.CreatedAt.Local().Format("2006-01-02 15:04:05"), target.FileCount)
			m.confirmBusy = "deleting"
			m.confirmCmd = snapDeleteCmd(m.ctx, m.app, target.ID)
			m.confirmReturn = overlaySnapshots
		case "q", "esc":
			m.overlay = overlayNone
		}

	case overlayProfiles:
		switch key {
		case "up", "k":
			if m.profileIdx > 0 {
				m.profileIdx--
			}
		case "down", "j":
			if m.profileIdx < len(m.profiles)-1 {
				m.profileIdx++
			}
		case "enter":
			m.overlay = overlayNone
			profile := m.profiles[m.profileIdx]
			if profile == m.profile {
				return nil
			}
			return m.start("loading", rescanCmd(m.app, m.cfg, profile))
		case "q", "esc":
			m.overlay = overlayNone
		}
	}
	return nil
}

func (m *browseModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	if m.overlay != overlayNone {
		return nil
	}

	inTree := m.pointInTree(mouse.X, mouse.Y)
	if inTree {
		m.focus = paneTree
	} else {
		m.focus = panePreview
	}
	m.resizePanes()

	switch msg.(type) {
	case tea.MouseWheelMsg:
		if !inTree {
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(msg)
			return cmd
		}
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.moveCursor(-3)
		case tea.MouseWheelDown:
			m.moveCursor(3)
		}

	case tea.MouseClickMsg:
		if !inTree || mouse.Button != tea.MouseLeft {
			return nil
		}
		idx, ok := m.rowAt(mouse.Y)
		if !ok {
			return nil
		}
		if idx == m.cursor && m.rows[m.visible[idx]].kind == rowDir {
			m.toggleCurrent()
			return nil
		}
		m.moveTo(idx)
	}
	return nil
}
