package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/hash"
	"github.com/alexjoedt/log"
	"github.com/gdamore/tcell/v2"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/rivo/tview"
)

type browseNode struct {
	pair    *dotfile.Pair
	changed bool
	marked  bool
}

type browseState struct {
	app      *App
	cfg      *Config
	profile  string
	pairs    []dotfile.Pair
	tvApp    *tview.Application
	tree     *tview.TreeView
	preview  *tview.TextView
	status   *tview.TextView
	header   *tview.TextView
	current  *browseNode
	viewMode string
}

const (
	browseViewDiff    = "diff"
	browseViewPreview = "preview"
)

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

	s := &browseState{
		app:      a,
		cfg:      cfg,
		profile:  profile,
		pairs:    dotfile.Merge(pairs),
		viewMode: browseViewDiff,
	}

	return s.run(ctx)
}

func (s *browseState) run(ctx context.Context) error {
	s.tvApp = tview.NewApplication()

	s.header = tview.NewTextView().
		SetDynamicColors(true).
		SetText(s.headerText())

	s.tree = tview.NewTreeView()
	s.tree.SetBorder(true).SetTitle(" files ")
	s.buildTree()

	s.preview = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	s.preview.SetBorder(true).SetTitle(" diff ")

	s.status = tview.NewTextView().
		SetDynamicColors(true).
		SetText(s.statusBarText())

	s.tree.SetChangedFunc(func(node *tview.TreeNode) {
		bn, ok := node.GetReference().(*browseNode)
		if !ok || bn == nil || bn.pair == nil {
			s.current = nil
			s.preview.Clear()
			return
		}
		s.current = bn
		s.renderPane()
	})

	s.tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			// Enter on a file node applies it (or the marked set).
			if s.current != nil && s.current.pair != nil {
				s.applyMarked(ctx)
			} else {
				// Toggle directory expand/collapse.
				return event
			}
			return nil
		case tcell.KeyTab:
			s.tvApp.SetFocus(s.preview)
			return nil
		}
		switch event.Rune() {
		case 'q', 'Q':
			s.tvApp.Stop()
			return nil
		case ' ':
			node := s.tree.GetCurrentNode()
			if node != nil {
				if bn, ok := node.GetReference().(*browseNode); ok && (bn == nil || bn.pair == nil) {
					node.SetExpanded(!node.IsExpanded())
				} else {
					s.toggleMark()
				}
			}
			return nil
		case 'l':
			if node := s.tree.GetCurrentNode(); node != nil {
				if bn, ok := node.GetReference().(*browseNode); ok && (bn == nil || bn.pair == nil) {
					node.SetExpanded(true)
					return nil
				}
			}
			return nil
		case 'h':
			if node := s.tree.GetCurrentNode(); node != nil {
				if bn, ok := node.GetReference().(*browseNode); ok && (bn == nil || bn.pair == nil) {
					node.SetExpanded(false)
					return nil
				}
			}
			return nil
		case 'S':
			s.saveMarked(ctx)
			return nil
		case 'd':
			s.viewMode = browseViewDiff
			s.renderPane()
			s.updateStatus()
			return nil
		case 'p':
			s.viewMode = browseViewPreview
			s.renderPane()
			s.updateStatus()
			return nil
		case 'r':
			s.showProfilePicker(ctx)
			return nil
		case 'P':
			s.pullRepo(ctx)
			return nil
		case '?':
			s.showHelp()
			return nil
		}
		return event
	})

	s.preview.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			s.tvApp.SetFocus(s.tree)
			return nil
		}
		switch event.Rune() {
		case 'q', 'Q':
			s.tvApp.Stop()
			return nil
		}
		return event
	})

	return s.tvApp.SetRoot(s.buildRootLayout(), true).EnableMouse(false).Run()
}

func (s *browseState) buildRootLayout() tview.Primitive {
	body := tview.NewFlex().
		AddItem(s.tree, 0, 1, true).
		AddItem(s.preview, 0, 2, false)

	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.header, 1, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(s.status, 1, 0, false)
}

func (s *browseState) headerText() string {
	return fmt.Sprintf("[::b]dman browse[::-]  profile:[green]%s[-]  [gray]d:diff  p:preview  space:mark  enter:apply↓  S:save↑  P:pull  r:profile  ?:help  q:quit[-]", s.profile)
}

func (s *browseState) statusBarText() string {
	marked := s.countMarked()
	if marked > 0 {
		return fmt.Sprintf("[yellow]%d file(s) marked[-]  view:[::b]%s[::-]  [gray]enter:apply↓  S:save↑[-]", marked, s.viewMode)
	}
	return fmt.Sprintf("view:[::b]%s[::-]  [gray]enter:apply↓ current  S:save↑ current[-]", s.viewMode)
}

func (s *browseState) updateStatus() {
	s.status.SetText(s.statusBarText())
}

func (s *browseState) countMarked() int {
	n := 0
	s.walkNodes(s.tree.GetRoot(), func(node *tview.TreeNode) {
		bn, ok := node.GetReference().(*browseNode)
		if ok && bn != nil && bn.marked {
			n++
		}
	})
	return n
}

func (s *browseState) walkNodes(node *tview.TreeNode, fn func(*tview.TreeNode)) {
	if node == nil {
		return
	}
	fn(node)
	for _, child := range node.GetChildren() {
		s.walkNodes(child, fn)
	}
}

func (s *browseState) buildTree() {
	root := tview.NewTreeNode("").SetSelectable(false)
	s.tree.SetRoot(root).SetCurrentNode(root)

	dirs := map[string]*tview.TreeNode{"": root}

	ensureDir := func(relDir string) *tview.TreeNode {
		if n, ok := dirs[relDir]; ok {
			return n
		}
		parts := strings.Split(relDir, string(filepath.Separator))
		cur := ""
		parent := root
		for _, part := range parts {
			next := filepath.Join(cur, part)
			if n, ok := dirs[next]; ok {
				parent = n
				cur = next
				continue
			}
			node := tview.NewTreeNode(part + "/").
				SetSelectable(true).
				SetExpanded(false).
				SetReference((*browseNode)(nil))
			parent.AddChild(node)
			dirs[next] = node
			parent = node
			cur = next
		}
		return dirs[relDir]
	}

	for i := range s.pairs {
		p := &s.pairs[i]
		rel, err := filepath.Rel(s.cfg.Path, p.Src)
		if err != nil {
			continue
		}
		dirPart := filepath.Dir(rel)
		name := filepath.Base(rel)

		changed := computeChanged(p)
		bn := &browseNode{pair: p, changed: changed}

		var parent *tview.TreeNode
		if dirPart == "." {
			parent = root
		} else {
			parent = ensureDir(dirPart)
		}

		node := tview.NewTreeNode(nodeLabel(name, bn)).
			SetSelectable(true).
			SetReference(bn)
		parent.AddChild(node)
	}

	s.selectFirstFile(root)
}

func (s *browseState) selectFirstFile(root *tview.TreeNode) {
	var first *tview.TreeNode
	s.walkNodes(root, func(node *tview.TreeNode) {
		if first != nil {
			return
		}
		if bn, ok := node.GetReference().(*browseNode); ok && bn != nil && bn.pair != nil {
			first = node
		}
	})
	if first != nil {
		s.tree.SetCurrentNode(first)
	}
}

func nodeLabel(name string, bn *browseNode) string {
	// Escape the checkbox so tview's dynamic-color parser does not treat
	// "[ ]" / "[x]" as color tags and strip them.
	check := tview.Escape("[ ]")
	if bn.marked {
		check = tview.Escape("[x]")
	}
	if bn.changed {
		return fmt.Sprintf("%s %s [red]●[-]", check, tview.Escape(name))
	}
	return fmt.Sprintf("%s %s", check, tview.Escape(name))
}

func (s *browseState) toggleMark() {
	node := s.tree.GetCurrentNode()
	if node == nil {
		return
	}
	bn, ok := node.GetReference().(*browseNode)
	if !ok || bn == nil || bn.pair == nil {
		return
	}
	bn.marked = !bn.marked
	node.SetText(nodeLabel(filepath.Base(bn.pair.Src), bn))
	s.updateStatus()
}

func (s *browseState) renderPane() {
	if s.current == nil || s.current.pair == nil {
		s.preview.Clear()
		return
	}
	p := s.current.pair

	rel, err := filepath.Rel(s.app.HomeDir, p.Dst)
	if err != nil {
		rel = p.Dst
	}

	switch s.viewMode {
	case browseViewPreview:
		s.preview.SetTitle(fmt.Sprintf(" preview: ~/%s ", rel))
		content, err := os.ReadFile(p.Src)
		if err != nil {
			s.preview.SetText(fmt.Sprintf("[red]error: %v[-]", err))
			return
		}
		if bytes.Contains(content, []byte{0}) {
			s.preview.SetText("[gray](binary file)[-]")
			return
		}
		s.preview.SetText(tview.Escape(string(content)))

	default:
		s.preview.SetTitle(fmt.Sprintf(" diff: ~/%s ", rel))
		repoContent, err := os.ReadFile(p.Src)
		if err != nil {
			s.preview.SetText(fmt.Sprintf("[red]error: %v[-]", err))
			return
		}
		var homeContent []byte
		if isExist(p.Dst) {
			homeContent, err = os.ReadFile(p.Dst)
			if err != nil {
				s.preview.SetText(fmt.Sprintf("[red]error: %v[-]", err))
				return
			}
		}
		if bytes.Equal(repoContent, homeContent) {
			s.preview.SetText("[green]file is up to date[-]")
			return
		}
		if bytes.Contains(repoContent, []byte{0}) || bytes.Contains(homeContent, []byte{0}) {
			s.preview.SetText("[gray]binary files differ[-]")
			return
		}
		aLabel := filepath.Join("a", rel)
		bLabel := filepath.Join("b", rel)
		edits := myers.ComputeEdits(span.URIFromPath(p.Src), string(homeContent), string(repoContent))
		unified := gotextdiff.ToUnified(aLabel, bLabel, string(homeContent), edits)
		s.preview.SetText(tvDiff(fmt.Sprint(unified)))
	}
	s.preview.ScrollToBeginning()
}

// tvDiff colorizes a unified diff string using tview color tags.
func tvDiff(diff string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString("[::b]" + tview.Escape(line) + "[::-]")
		case strings.HasPrefix(line, "@@"):
			b.WriteString("[cyan]" + tview.Escape(line) + "[-]")
		case strings.HasPrefix(line, "+"):
			b.WriteString("[green]" + tview.Escape(line) + "[-]")
		case strings.HasPrefix(line, "-"):
			b.WriteString("[red]" + tview.Escape(line) + "[-]")
		default:
			b.WriteString(tview.Escape(line))
		}
	}
	return b.String()
}

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

func (s *browseState) applyMarked(ctx context.Context) {
	var targets []string
	var affectedNodes []*tview.TreeNode

	s.walkNodes(s.tree.GetRoot(), func(node *tview.TreeNode) {
		bn, ok := node.GetReference().(*browseNode)
		if !ok || bn == nil || bn.pair == nil || !bn.marked {
			return
		}
		targets = append(targets, bn.pair.Dst)
		affectedNodes = append(affectedNodes, node)
	})

	currentNode := s.tree.GetCurrentNode()
	if len(targets) == 0 && s.current != nil && s.current.pair != nil {
		targets = []string{s.current.pair.Dst}
		affectedNodes = []*tview.TreeNode{currentNode}
	}

	if len(targets) == 0 {
		return
	}

	// We are running on tview's event-loop goroutine here (inside an input
	// capture handler). Use ForceDraw, which repaints synchronously and is
	// safe during direct event handling. Calling Draw() here would deadlock:
	// Draw() enqueues onto the updates channel and waits for the event loop
	// to drain it, but the loop is blocked inside this very handler.
	s.status.SetText("[yellow]applying…[-]")
	s.tvApp.ForceDraw()

	go func() {
		// Silence the default logger while applying so log.Step/log.Success
		// don't write to stdout and corrupt the TUI screen.
		prev := log.Default()
		log.SetDefault(log.NewCLILogger(log.WithWriter(io.Discard)))
		err := s.app.Apply(ctx, s.profile, false, true, true, targets)
		log.SetDefault(prev)

		s.tvApp.QueueUpdateDraw(func() {
			if err != nil {
				s.status.SetText(fmt.Sprintf("[red]apply error: %v[-]", err))
				return
			}
			for _, node := range affectedNodes {
				bn, ok := node.GetReference().(*browseNode)
				if !ok || bn == nil || bn.pair == nil {
					continue
				}
				bn.changed = computeChanged(bn.pair)
				bn.marked = false
				node.SetText(nodeLabel(filepath.Base(bn.pair.Src), bn))
			}
			if s.current != nil && s.current.pair != nil {
				s.renderPane()
			}
			s.updateStatus()
		})
	}()
}

func (s *browseState) saveMarked(ctx context.Context) {
	var targets []string
	var affectedNodes []*tview.TreeNode

	s.walkNodes(s.tree.GetRoot(), func(node *tview.TreeNode) {
		bn, ok := node.GetReference().(*browseNode)
		if !ok || bn == nil || bn.pair == nil || !bn.marked {
			return
		}
		targets = append(targets, bn.pair.Dst)
		affectedNodes = append(affectedNodes, node)
	})

	currentNode := s.tree.GetCurrentNode()
	if len(targets) == 0 && s.current != nil && s.current.pair != nil {
		targets = []string{s.current.pair.Dst}
		affectedNodes = []*tview.TreeNode{currentNode}
	}

	if len(targets) == 0 {
		return
	}

	doSave := func() {
		s.status.SetText("[yellow]saving…[-]")
		s.tvApp.ForceDraw()

		go func() {
			prev := log.Default()
			log.SetDefault(log.NewCLILogger(log.WithWriter(io.Discard)))
			err := s.app.SaveToRepo(ctx, s.profile, targets)
			log.SetDefault(prev)

			s.tvApp.QueueUpdateDraw(func() {
				if err != nil {
					s.status.SetText(fmt.Sprintf("[red]save error: %v[-]", err))
					return
				}
				for _, node := range affectedNodes {
					bn, ok := node.GetReference().(*browseNode)
					if !ok || bn == nil || bn.pair == nil {
						continue
					}
					bn.changed = computeChanged(bn.pair)
					bn.marked = false
					node.SetText(nodeLabel(filepath.Base(bn.pair.Src), bn))
				}
				if s.current != nil && s.current.pair != nil {
					s.renderPane()
				}
				s.updateStatus()
			})
		}()
	}

	// Single file: shift-key is guard enough.
	if len(targets) == 1 {
		doSave()
		return
	}

	// Bulk: one confirm modal.
	s.showConfirm(
		fmt.Sprintf("Save [yellow]%d[-] file(s) to repo? [gray](~/  →  repo)[-]", len(targets)),
		doSave,
	)
}

// showConfirm shows a small modal with a yes/no prompt.
// onConfirm is called (on the tview event-loop goroutine) when the user presses y/Y/Enter.
func (s *browseState) showConfirm(message string, onConfirm func()) {
	dismiss := func() {
		s.tvApp.SetRoot(s.buildRootLayout(), true).SetFocus(s.tree)
	}

	msg := tview.NewTextView().
		SetDynamicColors(true).
		SetText(fmt.Sprintf("%s\n\n[gray]y / Enter  confirm    n / Esc  cancel[-]", message))
	msg.SetBorder(true).SetTitle(" confirm ")

	msg.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			dismiss()
			onConfirm()
			return nil
		case tcell.KeyEscape:
			dismiss()
			return nil
		}
		switch event.Rune() {
		case 'y', 'Y':
			dismiss()
			onConfirm()
			return nil
		case 'n', 'N', 'q':
			dismiss()
			return nil
		}
		return event
	})

	modal := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(msg, 48, 0, true).
			AddItem(nil, 0, 1, false), 7, 0, true).
		AddItem(nil, 0, 1, false)

	s.tvApp.SetRoot(modal, true).SetFocus(msg)
}

// pullRepo pulls the dotfile repository from the remote and rebuilds the tree
// so the browser reflects any incoming changes.
func (s *browseState) pullRepo(ctx context.Context) {
	// See applyMarked: ForceDraw is safe on the event-loop goroutine; Draw
	// would deadlock here.
	s.status.SetText("[yellow]pulling…[-]")
	s.tvApp.ForceDraw()

	go func() {
		// Silence the default logger so git output doesn't corrupt the TUI.
		prev := log.Default()
		log.SetDefault(log.NewCLILogger(log.WithWriter(io.Discard)))
		err := s.app.Pull(ctx)
		log.SetDefault(prev)

		s.tvApp.QueueUpdateDraw(func() {
			if err != nil {
				s.status.SetText(fmt.Sprintf("[red]pull error: %v[-]", err))
				return
			}
			// Repository contents may have changed; rebuild from disk.
			pairs, cerr := s.app.collectTracked(s.cfg, s.profile)
			if cerr != nil {
				s.status.SetText(fmt.Sprintf("[red]reload error: %v[-]", cerr))
				return
			}
			s.pairs = dotfile.Merge(pairs)
			s.buildTree()
			s.current = nil
			s.preview.Clear()
			s.updateStatus()
		})
	}()
}

func (s *browseState) showProfilePicker(ctx context.Context) {
	profilesDir := filepath.Join(s.cfg.Path, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return
	}
	var profiles []string
	for _, e := range entries {
		if e.IsDir() {
			profiles = append(profiles, e.Name())
		}
	}
	if len(profiles) == 0 {
		return
	}

	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" select profile ")

	switchProfile := func(name string) {
		s.profile = name
		s.header.SetText(s.headerText())
		pairs, err := s.app.collectTracked(s.cfg, name)
		if err == nil {
			s.pairs = dotfile.Merge(pairs)
			s.buildTree()
			s.current = nil
			s.preview.Clear()
		}
		s.tvApp.SetRoot(s.buildRootLayout(), true).SetFocus(s.tree)
	}

	for _, p := range profiles {
		label := p
		if p == s.profile {
			label = "* " + p
		}
		pCopy := p
		list.AddItem(label, "", 0, func() { switchProfile(pCopy) })
	}

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || event.Rune() == 'q' {
			s.tvApp.SetRoot(s.buildRootLayout(), true).SetFocus(s.tree)
			return nil
		}
		// tview.List has no vim-style navigation; translate j/k to down/up
		// to match the file tree.
		switch event.Rune() {
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		return event
	})

	modal := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(list, 32, 0, true).
			AddItem(nil, 0, 1, false), 0, 1, true).
		AddItem(nil, 0, 1, false)

	s.tvApp.SetRoot(modal, true).SetFocus(list)
}

func (s *browseState) showHelp() {
	text := tview.NewTextView().
		SetDynamicColors(true).
		SetText(`[::b]dman browse — keybindings[::-]

  [yellow]↑ / ↓  j / k[-]  navigate tree
  [yellow]→ / ←[-]        expand / collapse directory
  [yellow]l / h[-]        expand / collapse directory
  [yellow]Space[-]        toggle file selection (checkbox) / expand-collapse directory
  [yellow]Enter[-]        apply marked files repo → ~/  (or current if none marked)
  [yellow]S[-]            save marked files ~/  → repo  (or current; bulk confirms)
  [yellow]d[-]            show diff in right pane
  [yellow]p[-]            show raw file content in right pane
  [yellow]P[-]            pull the dotfiles repo from remote
  [yellow]r[-]            switch profile
  [yellow]Tab[-]          switch focus between tree and preview
  [yellow]?[-]            toggle this help
  [yellow]q / Ctrl+C[-]   quit

Press any key to close.`)

	text.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		s.tvApp.SetRoot(s.buildRootLayout(), true).SetFocus(s.tree)
		return nil
	})

	s.tvApp.SetRoot(text, true).SetFocus(text)
}
