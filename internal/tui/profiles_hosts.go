package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostspec"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// section is the part of the selected profile shown in the right pane.
type section int

const (
	sectionVars section = iota
	sectionHosts
)

// hostCommentMax limits the comment input (same limit as the store).
const hostCommentMax = hostspec.MaxCommentLen

// Fields of the hosts entry form.
const (
	hostFieldIP = iota
	hostFieldNames
	hostFieldComment
	hostFieldCount
)

// ClaimsKey implements KeyClaimer: with the detail pane focused, tab and
// shift+tab switch between the Variables and Hosts sections instead of tabs.
func (s *profilesScreen) ClaimsKey(key string) bool {
	return s.mode == modeNormal && s.focus == paneVars && (key == "tab" || key == "shift+tab")
}

func (s *profilesScreen) toggleSection() {
	if s.section == sectionVars {
		s.section = sectionHosts
	} else {
		s.section = sectionVars
	}
}

// selectedHost returns the highlighted hosts entry, or nil.
func (s *profilesScreen) selectedHost() *profile.HostEntry {
	if s.hostCursor >= 0 && s.hostCursor < len(s.current.Hosts) {
		return &s.current.Hosts[s.hostCursor]
	}
	return nil
}

func (s *profilesScreen) hostInputs() []*textinput.Model {
	return []*textinput.Model{&s.hostIP, &s.hostNames, &s.hostComment}
}

// startEditHost opens the hosts entry form: prefilled from the highlighted
// entry when the detail pane is focused, empty (new entry) otherwise.
func (s *profilesScreen) startEditHost() tea.Cmd {
	if s.selected() == "" {
		s.setStatus("no profile selected; press n to create one", true)
		return nil
	}
	if s.loadErr != nil {
		s.setStatus(fmt.Sprintf("profile %q cannot be read: %v", s.selected(), s.loadErr), true)
		return nil
	}
	for _, in := range s.hostInputs() {
		in.Reset()
		in.Blur()
	}
	s.hostOrig = ""
	if s.focus == paneVars {
		if e := s.selectedHost(); e != nil {
			s.hostOrig = e.IP
			s.hostIP.SetValue(e.IP)
			s.hostNames.SetValue(strings.Join(e.Names, " "))
			s.hostComment.SetValue(e.Comment)
		}
	}
	for _, in := range s.hostInputs() {
		in.CursorEnd()
	}
	s.mode = modeEditHost
	s.hostField = hostFieldIP
	return s.hostIP.Focus()
}

func (s *profilesScreen) focusHostField(f int) tea.Cmd {
	ins := s.hostInputs()
	for _, in := range ins {
		in.Blur()
	}
	s.hostField = (f + hostFieldCount) % hostFieldCount
	return ins[s.hostField].Focus()
}

// checkHostField validates the field being left (same rules as the CLI).
func (s *profilesScreen) checkHostField() error {
	switch s.hostField {
	case hostFieldIP:
		_, err := hostspec.ParseIP(strings.TrimSpace(s.hostIP.Value()))
		return err
	case hostFieldNames:
		_, err := hostspec.NormalizeHostnames(strings.Fields(s.hostNames.Value()))
		return err
	}
	return hostspec.ValidComment(s.hostComment.Value())
}

func (s *profilesScreen) updateEditHost(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		s.mode = modeNormal
		for _, in := range s.hostInputs() {
			in.Blur()
		}
		s.setStatus("", false)
		return nil
	case "tab", "down":
		return s.focusHostField(s.hostField + 1)
	case "shift+tab", "up":
		return s.focusHostField(s.hostField - 1)
	case "enter":
		if err := s.checkHostField(); err != nil {
			s.setStatus(err.Error(), true)
			return nil
		}
		if s.hostField < hostFieldCount-1 {
			return s.focusHostField(s.hostField + 1)
		}
		ip := strings.TrimSpace(s.hostIP.Value())
		if err := s.saveHost(ip, strings.Fields(s.hostNames.Value()), s.hostComment.Value()); err != nil {
			s.setStatus(err.Error(), true)
			return nil
		}
		s.mode = modeNormal
		for _, in := range s.hostInputs() {
			in.Blur()
		}
		msg := fmt.Sprintf("saved hosts entry %s in profile %q", ip, s.current.Name)
		if st, err := hostsapply.ReadState(s.env.store.Dir); err == nil && st.Profile == s.current.Name {
			msg += "; its hosts are applied: apply them again to update the hosts file"
		}
		s.setStatus(msg, false)
		return nil
	}
	s.setStatus("", false)
	var cmd tea.Cmd
	in := s.hostInputs()[s.hostField]
	*in, cmd = in.Update(km)
	return cmd
}

// saveHost replaces the entry being edited (hostOrig) by ip/names/comment,
// with the store's rules (a name mapped elsewhere moves; critical names are
// refused).
func (s *profilesScreen) saveHost(ip string, names []string, comment string) error {
	name := s.selected()
	if name == "" {
		return errors.New("no profile selected")
	}
	p, err := s.env.store.Get(name)
	if err != nil {
		return err
	}
	if s.hostOrig != "" {
		// The edited entry is replaced as a whole (names and comment).
		if err := p.RemoveHostIP(s.hostOrig); err != nil && !errors.Is(err, profile.ErrHostNotFound) {
			return err
		}
	}
	if _, err := p.AddHost(ip, names, comment); err != nil {
		return err
	}
	if err := s.env.store.Save(p); err != nil {
		return err
	}
	s.loadSelected()
	if c, err := hostspec.ParseIP(ip); err == nil {
		for i, e := range s.current.Hosts {
			if e.IP == c {
				s.hostCursor = i
			}
		}
	}
	s.focus = paneVars
	s.section = sectionHosts
	return nil
}

func (s *profilesScreen) deleteHost() {
	if s.focus != paneVars {
		s.setStatus("select a hosts entry first (right or l)", true)
		return
	}
	e := s.selectedHost()
	if e == nil {
		s.setStatus("no hosts entry selected", true)
		return
	}
	ip, name := e.IP, s.current.Name
	p, err := s.env.store.Get(name)
	if err == nil {
		err = p.RemoveHostIP(ip)
	}
	if err == nil {
		err = s.env.store.Save(p)
	}
	if err != nil {
		s.setStatus(err.Error(), true)
		return
	}
	s.loadSelected()
	s.setStatus(fmt.Sprintf("removed hosts entry %s from profile %q", ip, name), false)
}

// updateScope handles the activate dialog of a profile that has hosts
// entries: e/enter environment (as before), h hosts only, b both.
func (s *profilesScreen) updateScope(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "e", "enter":
		s.mode = modeNormal
		return s.activateEnv()
	case "h":
		s.mode = modeNormal
		s.applyHosts()
		return nil
	case "b":
		s.mode = modeNormal
		return s.activateBoth()
	case "esc", "q", "n":
		s.mode = modeNormal
		s.setStatus("", false)
	}
	return nil
}

// applyHosts writes the selected profile's hosts to the hosts file (this
// process does it; no shell code). A failure (typically: not elevated) is
// shown in the status line and the TUI stays open.
func (s *profilesScreen) applyHosts() bool {
	name := s.selected()
	ch, err := hostsapply.Apply(s.env.store, s.env.hostsFile, name, false)
	if err != nil {
		s.setStatus(fmt.Sprintf("hosts of %q not applied: %v", name, err), true)
		return false
	}
	switch {
	case !ch.Changed:
		s.setStatus(fmt.Sprintf("hosts of %q already applied to %s", name, ch.Path), false)
	default:
		s.setStatus(fmt.Sprintf("applied hosts of %q to %s (backup: %s)", name, ch.Path, ch.Backup), false)
	}
	return true
}

// activateBoth applies the hosts, then ends with the environment code. The
// code is rendered first and nothing is printed when the hosts part fails,
// so the shell is never left half applied.
func (s *profilesScreen) activateBoth() tea.Cmd {
	name := s.selected()
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
	if !s.applyHosts() {
		return nil
	}
	hostsMsg := s.status
	if s.env.stdoutTTY {
		s.showNotice("Activate "+name, s.env.command+" use "+name+" --shell "+string(s.env.kind))
		s.setStatus(hostsMsg+"; the environment still needs the command below", false)
		return nil
	}
	return exitCmd(Result{
		Action:  ActionActivate,
		Profile: name,
		Code:    code,
		Message: fmt.Sprintf("envmgr: activated profile %q (%d variables); %s", name, len(p.Vars), hostsMsg),
	})
}

// hostsLines renders the Hosts section rows.
func (s *profilesScreen) hostsLines(iw, rows int, focused bool) []string {
	st := s.env.styles
	hs := s.current.Hosts
	if len(hs) == 0 {
		return []string{st.Dim.Render(fit("no hosts entries; press e to add one", iw))}
	}
	ipw := 0
	for _, e := range hs {
		if len(e.IP) > ipw {
			ipw = len(e.IP)
		}
	}
	if limit := iw / 2; ipw > limit {
		ipw = limit
	}
	var lines []string
	start, end := window(len(hs), s.hostCursor, rows)
	for i := start; i < end; i++ {
		e := hs[i]
		rest := strings.Join(e.Names, " ")
		if e.Comment != "" {
			rest += "  # " + e.Comment
		}
		rest = printable(rest)
		mark := "  "
		sel := focused && i == s.hostCursor
		if sel {
			mark = "> "
		}
		ipText := pad(fit(e.IP, ipw), ipw)
		line := mark + ipText + "  "
		room := iw - lipgloss.Width(line)
		if sel {
			line = st.Selected.Render(line) + fit(rest, room)
		} else {
			line = mark + st.Key.Render(ipText) + "  " + st.Dim.Render(fit(rest, room))
		}
		lines = append(lines, line)
	}
	return lines
}

// sectionTabs is the "Variables  Hosts" line of the detail pane.
func (s *profilesScreen) sectionTabs(iw int) string {
	st := s.env.styles
	v := fmt.Sprintf("Variables (%d)", len(s.keys))
	h := fmt.Sprintf("Hosts (%d)", len(s.current.Hosts))
	if s.section == sectionVars {
		v, h = st.TabActive.Render("["+v+"]"), st.TabInactive.Render(" "+h+" ")
	} else {
		v, h = st.TabInactive.Render(" "+v+" "), st.TabActive.Render("["+h+"]")
	}
	return fit(v+" "+h, iw)
}
