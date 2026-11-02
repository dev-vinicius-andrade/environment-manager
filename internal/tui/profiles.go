package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeNewProfile
	modeEditKey
	modeEditValue
	modeConfirmDelete
	modeNotice
)

type pane int

const (
	paneList pane = iota
	paneVars
)

// notice is the box shown instead of exiting when stdout is a terminal.
type notice struct {
	title   string
	lines   []string
	oneLine string // the exact command offered for copying
}

// profilesScreen lists profiles (left) and the selected profile's variables
// (right).
type profilesScreen struct {
	env *env

	names   []string // every profile, sorted
	visible []string // names matching the filter
	cursor  int      // index in visible
	focus   pane

	current   profile.Profile // selected profile, loaded from the store
	keys      []string        // current.Vars keys, sorted
	varCursor int
	loadErr   error

	reveal bool
	mode   mode
	filter string

	filterInput textinput.Model
	nameInput   textinput.Model
	keyInput    textinput.Model
	valueInput  textinput.Model

	notice notice

	status    string
	statusErr bool
}

func newProfilesScreen(e *env) *profilesScreen {
	s := &profilesScreen{env: e}
	st := e.styles
	s.filterInput = st.newInput("/", 64)
	s.nameInput = st.newInput("name: ", 64)
	s.keyInput = st.newInput("variable: ", 128)
	s.valueInput = st.newInput("value: ", 0)
	s.reload("")
	return s
}

func (s *profilesScreen) Title() string { return "Profiles" }

func (s *profilesScreen) Capturing() bool { return s.mode != modeNormal }

func (s *profilesScreen) Status() (string, bool) {
	if s.status != "" {
		return s.status, s.statusErr
	}
	if s.mode != modeNormal {
		return "", false
	}
	if s.env.stdoutTTY {
		return "To activate from here, start the TUI as: " + s.wrapTUI(), false
	}
	return "enter activates the selected profile and exits; the code goes to your shell", false
}

func (s *profilesScreen) wrapTUI() string {
	return WrapCommand(s.env.kind, s.env.command+" tui --shell "+string(s.env.kind))
}

func (s *profilesScreen) setStatus(msg string, isErr bool) {
	s.status, s.statusErr = msg, isErr
}

// selected returns the highlighted profile name, or "".
func (s *profilesScreen) selected() string {
	if s.cursor >= 0 && s.cursor < len(s.visible) {
		return s.visible[s.cursor]
	}
	return ""
}

// selectedVar returns the highlighted variable name, or "".
func (s *profilesScreen) selectedVar() string {
	if s.varCursor >= 0 && s.varCursor < len(s.keys) {
		return s.keys[s.varCursor]
	}
	return ""
}

// reload re-reads the profile list and selects want (or keeps the current
// selection when want is "").
func (s *profilesScreen) reload(want string) {
	if want == "" {
		want = s.selected()
	}
	names, err := s.env.store.List()
	if err != nil {
		s.setStatus("cannot list profiles: "+err.Error(), true)
		names = []string{}
	}
	s.names = names
	s.applyFilter(want)
}

// applyFilter recomputes the visible names and keeps want selected when it is
// visible.
func (s *profilesScreen) applyFilter(want string) {
	s.visible = s.visible[:0]
	f := strings.ToLower(s.filter)
	for _, n := range s.names {
		if f == "" || strings.Contains(strings.ToLower(n), f) {
			s.visible = append(s.visible, n)
		}
	}
	idx := -1
	for i, n := range s.visible {
		if n == want {
			idx = i
		}
	}
	if idx >= 0 {
		s.cursor = idx
	}
	s.clampCursor()
	s.loadSelected()
}

func (s *profilesScreen) clampCursor() {
	if s.cursor >= len(s.visible) {
		s.cursor = len(s.visible) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// loadSelected reads the selected profile from the store.
func (s *profilesScreen) loadSelected() {
	prev := s.current.Name
	s.current, s.keys, s.loadErr = profile.Profile{}, nil, nil
	name := s.selected()
	if name == "" {
		s.varCursor = 0
		return
	}
	p, err := s.env.store.Get(name)
	if err != nil {
		s.loadErr = err
		s.current = profile.Profile{Name: name}
		s.varCursor = 0
		return
	}
	s.current = p
	for k := range p.Vars {
		s.keys = append(s.keys, k)
	}
	sort.Strings(s.keys)
	if prev != name {
		s.varCursor = 0
	}
	if s.varCursor >= len(s.keys) {
		s.varCursor = len(s.keys) - 1
	}
	if s.varCursor < 0 {
		s.varCursor = 0
	}
}

// Update implements Screen.
func (s *profilesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, s.updateInputs(msg)
	}
	switch s.mode {
	case modeFilter:
		return s, s.updateFilter(km)
	case modeNewProfile:
		return s, s.updateNewProfile(km)
	case modeEditKey, modeEditValue:
		return s, s.updateEditVar(km)
	case modeConfirmDelete:
		return s, s.updateConfirmDelete(km)
	case modeNotice:
		return s, s.updateNotice(km)
	}
	return s, s.updateNormal(km)
}

// updateInputs forwards non-key messages (paste results, ...) to the focused
// input.
func (s *profilesScreen) updateInputs(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch s.mode {
	case modeFilter:
		s.filterInput, cmd = s.filterInput.Update(msg)
	case modeNewProfile:
		s.nameInput, cmd = s.nameInput.Update(msg)
	case modeEditKey:
		s.keyInput, cmd = s.keyInput.Update(msg)
	case modeEditValue:
		s.valueInput, cmd = s.valueInput.Update(msg)
	}
	return cmd
}

func (s *profilesScreen) updateNormal(km tea.KeyMsg) tea.Cmd {
	s.setStatus("", false)
	switch km.String() {
	case "up", "k":
		s.move(-1)
	case "down", "j":
		s.move(1)
	case "home", "g":
		s.move(-1 << 30)
	case "end", "G":
		s.move(1 << 30)
	case "left", "h":
		s.focus = paneList
	case "right", "l":
		if s.selected() != "" {
			s.focus = paneVars
		}
	case "enter", "a":
		return s.activate()
	case "d":
		return s.deactivate()
	case "n":
		s.mode = modeNewProfile
		s.nameInput.Reset()
		return s.nameInput.Focus()
	case "e":
		return s.startEdit()
	case "x":
		s.deleteVar()
	case "D":
		if s.selected() == "" {
			s.setStatus("no profile selected", true)
			return nil
		}
		s.mode = modeConfirmDelete
	case "r":
		s.reveal = !s.reveal
	case "/":
		s.mode = modeFilter
		s.filterInput.SetValue(s.filter)
		s.filterInput.CursorEnd()
		return s.filterInput.Focus()
	}
	return nil
}

func (s *profilesScreen) move(delta int) {
	if s.focus == paneVars {
		s.varCursor = clamp(s.varCursor+delta, 0, len(s.keys)-1)
		return
	}
	if len(s.visible) == 0 {
		return
	}
	n := clamp(s.cursor+delta, 0, len(s.visible)-1)
	if n != s.cursor {
		s.cursor = n
		s.loadSelected()
	}
}

func clamp(v, lo, hi int) int {
	if v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	return v
}

func (s *profilesScreen) updateFilter(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "enter":
		s.mode = modeNormal
		s.filterInput.Blur()
		return nil
	case "esc":
		s.mode = modeNormal
		s.filterInput.Blur()
		s.filter = ""
		s.applyFilter(s.selected())
		return nil
	}
	var cmd tea.Cmd
	s.filterInput, cmd = s.filterInput.Update(km)
	if v := s.filterInput.Value(); v != s.filter {
		s.filter = v
		s.cursor = 0
		s.applyFilter("")
	}
	return cmd
}

func (s *profilesScreen) updateNewProfile(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		s.mode = modeNormal
		s.nameInput.Blur()
		s.setStatus("", false)
		return nil
	case "enter":
		name := strings.TrimSpace(s.nameInput.Value())
		if err := s.createProfile(name); err != nil {
			s.setStatus(err.Error(), true)
			return nil
		}
		s.mode = modeNormal
		s.nameInput.Blur()
		s.setStatus(fmt.Sprintf("created profile %q", name), false)
		return nil
	}
	var cmd tea.Cmd
	s.nameInput, cmd = s.nameInput.Update(km)
	return cmd
}

// createProfile validates name and saves an empty profile.
func (s *profilesScreen) createProfile(name string) error {
	if !profile.ValidName(name) {
		return fmt.Errorf("invalid profile name %q (letters, digits, '_', '.', '-'; must start with a letter or digit)", name)
	}
	if _, err := s.env.store.Get(name); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}
	if err := s.env.store.Save(profile.Profile{Name: name, Vars: map[string]string{}}); err != nil {
		return err
	}
	// Make sure the new profile is visible.
	if s.filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(s.filter)) {
		s.filter = ""
	}
	s.focus = paneList
	s.reload(name)
	return nil
}

func (s *profilesScreen) startEdit() tea.Cmd {
	if s.selected() == "" {
		s.setStatus("no profile selected; press n to create one", true)
		return nil
	}
	if s.loadErr != nil {
		s.setStatus(fmt.Sprintf("profile %q cannot be read: %v", s.selected(), s.loadErr), true)
		return nil
	}
	s.keyInput.Reset()
	s.valueInput.Reset()
	if s.focus == paneVars {
		if k := s.selectedVar(); k != "" {
			s.keyInput.SetValue(k)
			s.valueInput.SetValue(s.current.Vars[k])
		}
	}
	s.keyInput.CursorEnd()
	s.valueInput.CursorEnd()
	s.mode = modeEditKey
	s.valueInput.Blur()
	return s.keyInput.Focus()
}

func (s *profilesScreen) updateEditVar(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		s.mode = modeNormal
		s.keyInput.Blur()
		s.valueInput.Blur()
		s.setStatus("", false)
		return nil
	case "tab", "shift+tab", "up", "down":
		return s.switchEditField()
	case "enter":
		if s.mode == modeEditKey {
			if err := checkVarName(strings.TrimSpace(s.keyInput.Value())); err != nil {
				s.setStatus(err.Error(), true)
				return nil
			}
			return s.switchEditField()
		}
		key := strings.TrimSpace(s.keyInput.Value())
		if err := s.saveVar(key, s.valueInput.Value()); err != nil {
			s.setStatus(err.Error(), true)
			return nil
		}
		s.mode = modeNormal
		s.keyInput.Blur()
		s.valueInput.Blur()
		msg := fmt.Sprintf("set %s in profile %q", key, s.current.Name)
		if s.current.Name == s.env.activeName {
			msg += "; it is active: activate it again to apply"
		}
		s.setStatus(msg, false)
		return nil
	}
	s.setStatus("", false)
	var cmd tea.Cmd
	if s.mode == modeEditKey {
		s.keyInput, cmd = s.keyInput.Update(km)
	} else {
		s.valueInput, cmd = s.valueInput.Update(km)
	}
	return cmd
}

func (s *profilesScreen) switchEditField() tea.Cmd {
	if s.mode == modeEditKey {
		s.mode = modeEditValue
		s.keyInput.Blur()
		return s.valueInput.Focus()
	}
	s.mode = modeEditKey
	s.valueInput.Blur()
	return s.keyInput.Focus()
}

// checkVarName applies the same rules as "envmgr set".
func checkVarName(key string) error {
	if !profile.ValidVar(key) {
		return fmt.Errorf("invalid variable name %q (letters, digits and '_'; must not start with a digit)", key)
	}
	if shell.Reserved(key) {
		return fmt.Errorf("variable %q uses the reserved ENVMGR_ prefix", key)
	}
	return nil
}

func (s *profilesScreen) saveVar(key, value string) error {
	if err := checkVarName(key); err != nil {
		return err
	}
	name := s.selected()
	if name == "" {
		return errors.New("no profile selected")
	}
	if err := s.env.store.SetVar(name, key, value); err != nil {
		return err
	}
	s.loadSelected()
	for i, k := range s.keys {
		if k == key {
			s.varCursor = i
		}
	}
	s.focus = paneVars
	return nil
}

func (s *profilesScreen) deleteVar() {
	if s.focus != paneVars {
		s.setStatus("select a variable first (right or l)", true)
		return
	}
	key := s.selectedVar()
	if key == "" {
		s.setStatus("no variable selected", true)
		return
	}
	if err := s.env.store.UnsetVar(s.current.Name, key); err != nil {
		s.setStatus(err.Error(), true)
		return
	}
	name := s.current.Name
	s.loadSelected()
	if len(s.keys) == 0 {
		s.focus = paneList
	}
	s.setStatus(fmt.Sprintf("removed %s from profile %q", key, name), false)
}

func (s *profilesScreen) updateConfirmDelete(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "y", "Y":
		name := s.selected()
		s.mode = modeNormal
		if err := s.env.store.Delete(name); err != nil {
			s.setStatus(err.Error(), true)
			return nil
		}
		s.focus = paneList
		s.reload("")
		s.setStatus(fmt.Sprintf("deleted profile %q", name), false)
	case "n", "N", "esc", "q":
		s.mode = modeNormal
		s.setStatus("", false)
	}
	return nil
}

func (s *profilesScreen) activate() tea.Cmd {
	name := s.selected()
	if name == "" {
		s.setStatus("no profile selected; press n to create one", true)
		return nil
	}
	p, err := s.env.store.Get(name)
	if err != nil {
		s.setStatus(fmt.Sprintf("profile %q: %v", name, err), true)
		return nil
	}
	code, err := shell.Use(s.env.kind, s.env.lookup, p)
	if err != nil {
		s.setStatus(fmt.Sprintf("profile %q: %v", name, err), true)
		return nil
	}
	if s.env.stdoutTTY {
		s.showNotice("Activate "+name, s.env.command+" use "+name+" --shell "+string(s.env.kind))
		return nil
	}
	return exitCmd(Result{
		Action:  ActionActivate,
		Profile: name,
		Code:    code,
		Message: fmt.Sprintf("envmgr: activated profile %q (%d variables)", name, len(p.Vars)),
	})
}

func (s *profilesScreen) deactivate() tea.Cmd {
	st := shell.ReadState(s.env.lookup)
	if !st.Active() {
		s.setStatus("no active profile", true)
		return nil
	}
	code, err := shell.Deactivate(s.env.kind, s.env.lookup)
	if err != nil {
		s.setStatus(err.Error(), true)
		return nil
	}
	if s.env.stdoutTTY {
		s.showNotice("Deactivate", s.env.command+" deactivate --shell "+string(s.env.kind))
		return nil
	}
	msg := "envmgr: deactivated"
	if st.Profile != "" {
		msg = fmt.Sprintf("envmgr: deactivated profile %q", st.Profile)
	}
	return exitCmd(Result{Action: ActionDeactivate, Profile: st.Profile, Code: code, Message: msg})
}

// showNotice explains why nothing can be activated from an uncaptured stdout
// and offers the exact one-liner.
func (s *profilesScreen) showNotice(title, cmdline string) {
	one := WrapCommand(s.env.kind, cmdline)
	s.notice = notice{
		title: title,
		lines: []string{
			"stdout is a terminal, so no shell would evaluate the code; nothing was changed.",
			"",
			"Run this in your shell:",
			"  " + one,
			"",
			"Or start the TUI so it can activate on exit:",
			"  " + s.wrapTUI(),
		},
		oneLine: one,
	}
	s.mode = modeNotice
	s.setStatus("", false)
}

func (s *profilesScreen) updateNotice(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "c", "y":
		if s.env.copy == nil {
			s.setStatus("copying is not available here; select the command with the mouse", true)
			return nil
		}
		err := s.env.copy(s.notice.oneLine)
		switch {
		case errors.Is(err, ErrCopiedViaTerminal):
			s.mode = modeNormal
			s.setStatus(err.Error()+": "+s.notice.oneLine, false)
		case err != nil:
			s.setStatus("copy failed: "+err.Error(), true)
		default:
			s.mode = modeNormal
			s.setStatus("copied: "+s.notice.oneLine, false)
		}
	case "esc", "enter", "q", "n":
		s.mode = modeNormal
		s.setStatus("", false)
	}
	return nil
}

// ShortHelp implements Screen.
func (s *profilesScreen) ShortHelp() []Hint {
	switch s.mode {
	case modeFilter:
		return []Hint{{"enter", "keep filter"}, {"esc", "clear filter"}}
	case modeNewProfile:
		return []Hint{{"enter", "create"}, {"esc", "cancel"}}
	case modeEditKey, modeEditValue:
		return []Hint{{"enter", "next/save"}, {"tab", "switch field"}, {"esc", "cancel"}}
	case modeConfirmDelete:
		return []Hint{{"y", "delete"}, {"n", "cancel"}}
	case modeNotice:
		return []Hint{{"c", "copy command"}, {"esc", "close"}}
	}
	reveal := "reveal"
	if s.reveal {
		reveal = "mask"
	}
	return []Hint{
		{"enter", "activate"}, {"d", "deactivate"}, {"j/k", "move"}, {"h/l", "pane"},
		{"n", "new"}, {"e", "edit var"}, {"x", "del var"}, {"D", "del profile"},
		{"r", reveal}, {"/", "filter"},
	}
}

// FullHelp implements Screen.
func (s *profilesScreen) FullHelp() []Hint {
	return []Hint{
		{"up/down, j/k", "move in the focused pane"},
		{"left/right, h/l", "focus profiles / variables"},
		{"enter, a", "activate the selected profile (exits, prints shell code)"},
		{"d", "deactivate the active profile (exits, prints shell code)"},
		{"n", "new profile"},
		{"e", "edit the selected variable, or add one"},
		{"x", "delete the selected variable"},
		{"D", "delete the selected profile (asks y/n)"},
		{"r", "reveal / mask values"},
		{"/", "filter profiles (enter keeps, esc clears)"},
	}
}

// View implements Screen.
func (s *profilesScreen) View(w, h int) string {
	st := s.env.styles
	var bottom []string
	switch s.mode {
	case modeFilter:
		s.filterInput.Width = w - 3
		bottom = []string{s.filterInput.View()}
	case modeNewProfile:
		s.nameInput.Width = w - 8
		bottom = []string{st.PaneTitle.Render("New profile"), s.nameInput.View()}
	case modeEditKey, modeEditValue:
		s.keyInput.Width = w - 12
		s.valueInput.Width = w - 9
		if s.reveal {
			s.valueInput.EchoMode = textinput.EchoNormal
		} else {
			s.valueInput.EchoMode = textinput.EchoPassword
		}
		bottom = []string{
			st.PaneTitle.Render(fmt.Sprintf("Set variable in %q", s.current.Name)),
			s.keyInput.View(), s.valueInput.View(),
		}
	case modeConfirmDelete:
		bottom = []string{st.Error.Render(fmt.Sprintf("Delete profile %q? (y/n)", s.selected()))}
	case modeNotice:
		return s.noticeView(w, h)
	}
	for i := range bottom {
		bottom[i] = fit(bottom[i], w)
	}
	paneH := h - len(bottom)
	if paneH < 3 {
		// Too short for panes: show only the prompt.
		return block(bottom, w, h)
	}
	panes := s.panesView(w, paneH)
	if len(bottom) == 0 {
		return panes
	}
	return panes + "\n" + block(bottom, w, len(bottom))
}

func (s *profilesScreen) panesView(w, h int) string {
	if w < MinWidth {
		// Narrow: only the focused pane.
		if s.focus == paneVars {
			return s.varsPane(w, h, true)
		}
		return s.listPane(w, h, true)
	}
	lw := w / 3
	if lw > 32 {
		lw = 32
	}
	left := s.listPane(lw, h, s.focus == paneList)
	right := s.varsPane(w-lw, h, s.focus == paneVars)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// framed draws lines in a bordered pane of exactly w x h cells.
func (s *profilesScreen) framed(lines []string, w, h int, focused bool) string {
	st := s.env.styles.Pane
	if focused {
		st = s.env.styles.PaneFocused
	}
	return st.Render(block(lines, w-2, h-2))
}

func (s *profilesScreen) listPane(w, h int, focused bool) string {
	st := s.env.styles
	iw, rows := w-2, h-3
	title := fmt.Sprintf("Profiles (%d)", len(s.names))
	if s.filter != "" {
		title = fmt.Sprintf("Profiles %d/%d /%s", len(s.visible), len(s.names), s.filter)
	}
	lines := []string{st.PaneTitle.Render(fit(title, iw))}
	if len(s.visible) == 0 {
		msg := "no profiles; press n"
		if s.filter != "" {
			msg = "no match"
		}
		lines = append(lines, st.Dim.Render(fit(msg, iw)))
	}
	start, end := window(len(s.visible), s.cursor, rows)
	for i := start; i < end; i++ {
		name := s.visible[i]
		mark := "  "
		if i == s.cursor {
			mark = "> "
		}
		act := " "
		if name == s.env.activeName {
			act = "*"
		}
		text := fit(mark+act+name, iw)
		switch {
		case i == s.cursor:
			text = st.Selected.Render(text)
		case name == s.env.activeName:
			text = st.Active.Render(text)
		}
		lines = append(lines, text)
	}
	return s.framed(lines, w, h, focused)
}

func (s *profilesScreen) varsPane(w, h int, focused bool) string {
	st := s.env.styles
	iw, rows := w-2, h-3
	name := s.selected()
	var lines []string
	switch {
	case name == "":
		lines = []string{st.PaneTitle.Render("Variables"), st.Dim.Render(fit("no profile selected", iw))}
		return s.framed(lines, w, h, focused)
	case s.loadErr != nil:
		lines = []string{st.PaneTitle.Render(fit(name, iw))}
		for _, l := range wrapLines("cannot read profile: "+s.loadErr.Error(), iw) {
			lines = append(lines, st.Error.Render(l))
		}
		return s.framed(lines, w, h, focused)
	}
	title := fmt.Sprintf("%s (%d variables)", name, len(s.keys))
	if name == s.env.activeName {
		title += " active"
	}
	if s.reveal {
		title += " [revealed]"
	}
	lines = []string{st.PaneTitle.Render(fit(title, iw))}
	if len(s.keys) == 0 {
		lines = append(lines, st.Dim.Render(fit("no variables; press e to add one", iw)))
	}
	kw := 0
	for _, k := range s.keys {
		if len(k) > kw {
			kw = len(k)
		}
	}
	if limit := iw / 2; kw > limit {
		kw = limit
	}
	start, end := window(len(s.keys), s.varCursor, rows)
	for i := start; i < end; i++ {
		k := s.keys[i]
		v := maskedValue
		if s.reveal {
			v = printable(s.current.Vars[k])
		}
		mark := "  "
		sel := focused && i == s.varCursor
		if sel {
			mark = "> "
		}
		keyText := pad(fit(k, kw), kw)
		line := mark + keyText + "  "
		rest := iw - lipgloss.Width(line)
		if sel {
			line = st.Selected.Render(line) + fit(v, rest)
		} else {
			line = mark + st.Key.Render(keyText) + "  " + st.Dim.Render(fit(v, rest))
		}
		lines = append(lines, line)
	}
	return s.framed(lines, w, h, focused)
}

func (s *profilesScreen) noticeView(w, h int) string {
	st := s.env.styles
	iw := w - 4 // border + padding
	if iw < 10 {
		iw = w
	}
	lines := []string{st.PaneTitle.Render(s.notice.title)}
	for _, l := range s.notice.lines {
		lines = append(lines, wrapLines(l, iw)...)
	}
	lines = append(lines, "", st.Dim.Render("c copy the command   esc close"))
	box := st.Notice.Width(iw + 2).Render(strings.Join(lines, "\n"))
	if lipgloss.Height(box) > h {
		return block(lines, w, h)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}
