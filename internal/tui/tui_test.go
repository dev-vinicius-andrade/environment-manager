package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// harness drives an App without a terminal: messages go through Update and
// returned commands are executed synchronously, like the Bubble Tea loop.
type harness struct {
	t      *testing.T
	app    *App
	store  *profile.Store
	quit   bool
	copied []string
}

type setupOpts struct {
	env       map[string]string
	kind      shell.Kind
	stdoutTTY bool
	profiles  map[string]map[string]string
}

func newHarness(t *testing.T, o setupOpts) *harness {
	t.Helper()
	store := &profile.Store{Dir: t.TempDir()}
	for name, vars := range o.profiles {
		if err := store.Save(profile.Profile{Name: name, Vars: vars}); err != nil {
			t.Fatal(err)
		}
	}
	if o.env == nil {
		o.env = map[string]string{}
	}
	if o.kind == "" {
		o.kind = shell.Bash
	}
	h := &harness{t: t, store: store}
	h.app = New(Options{
		Store:            store,
		Shell:            o.kind,
		Lookup:           shell.MapLookup(o.env),
		StdoutIsTerminal: o.stdoutTTY,
		Copy: func(s string) error {
			h.copied = append(h.copied, s)
			return nil
		},
	})
	return h
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	if h.quit {
		h.t.Fatalf("message %#v sent after quit", msg)
	}
	_, cmd := h.app.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.QuitMsg:
		h.quit = true
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		h.send(msg)
	}
}

// keys sends named keys ("enter", "down", ...) or single characters.
func (h *harness) keys(ks ...string) {
	h.t.Helper()
	for _, k := range ks {
		h.send(keyMsg(k))
	}
}

// typeText sends every rune of s as its own key press.
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		if r == ' ' {
			h.send(keyMsg("space"))
			continue
		}
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (h *harness) screen() *profilesScreen { return h.app.screens[0].(*profilesScreen) }

func (h *harness) view() string { return h.app.View() }

func (h *harness) status() (string, bool) { return h.screen().Status() }

func (h *harness) vars(name string) map[string]string {
	h.t.Helper()
	p, err := h.store.Get(name)
	if err != nil {
		h.t.Fatalf("get %s: %v", name, err)
	}
	return p.Vars
}

func TestNavigation(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{
		"a": {"A1": "1", "A2": "2"}, "b": {}, "c": {},
	}})
	s := h.screen()
	if s.selected() != "a" {
		t.Fatalf("initial selection %q", s.selected())
	}
	steps := []struct {
		key  string
		want string
	}{{"down", "b"}, {"j", "c"}, {"down", "c"}, {"k", "b"}, {"up", "a"}, {"up", "a"}}
	for _, st := range steps {
		h.keys(st.key)
		if got := s.selected(); got != st.want {
			t.Fatalf("after %s: selected %q want %q", st.key, got, st.want)
		}
	}
	// Right pane: variables of "a".
	h.keys("l")
	if s.focus != paneVars || s.selectedVar() != "A1" {
		t.Fatalf("focus=%v var=%q", s.focus, s.selectedVar())
	}
	h.keys("j")
	if s.selectedVar() != "A2" {
		t.Fatalf("var=%q", s.selectedVar())
	}
	h.keys("j")
	if s.selectedVar() != "A2" {
		t.Fatalf("var moved past end: %q", s.selectedVar())
	}
	h.keys("h", "j")
	if s.focus != paneList || s.selected() != "b" {
		t.Fatalf("focus=%v selected=%q", s.focus, s.selected())
	}
	if h.quit {
		t.Fatal("navigation quit the program")
	}
}

func TestActiveProfileMarked(t *testing.T) {
	h := newHarness(t, setupOpts{
		env:      map[string]string{"ENVMGR_PROFILE": "b"},
		profiles: map[string]map[string]string{"a": {}, "b": {}},
	})
	v := h.view()
	if !strings.Contains(v, " *b") || strings.Contains(v, "*a") {
		t.Fatalf("active marker missing or wrong:\n%s", v)
	}
	if !strings.Contains(v, "active: b") {
		t.Fatalf("header does not show the active profile:\n%s", v)
	}
}

func TestMaskAndReveal(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{
		"dev": {"SECRET": "hunter2", "ESC": "a\x1b[31mb\nc"},
	}})
	v := h.view()
	if strings.Contains(v, "hunter2") || !strings.Contains(v, maskedValue) {
		t.Fatalf("value not masked:\n%s", v)
	}
	h.keys("r")
	v = h.view()
	if !strings.Contains(v, "hunter2") {
		t.Fatalf("value not revealed:\n%s", v)
	}
	if strings.Contains(v, "\x1b") || !strings.Contains(v, `a\x1b[31mb\nc`) {
		t.Fatalf("control characters not escaped:\n%q", v)
	}
	h.keys("r")
	if strings.Contains(h.view(), "hunter2") {
		t.Fatal("value still revealed after second r")
	}
}

func TestCreateProfile(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"a": {}}})
	h.keys("n")
	if !h.screen().Capturing() {
		t.Fatal("n did not open the name prompt")
	}
	// q is text while the prompt is open, not quit.
	h.typeText("q1")
	h.keys("enter")
	if h.quit {
		t.Fatal("typing q in the prompt quit")
	}
	names, _ := h.store.List()
	if !reflect.DeepEqual(names, []string{"a", "q1"}) {
		t.Fatalf("names = %v", names)
	}
	if h.screen().selected() != "q1" || h.screen().Capturing() {
		t.Fatalf("selected=%q capturing=%v", h.screen().selected(), h.screen().Capturing())
	}
}

func TestCreateProfileRejectsInvalidAndDuplicate(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"a": {}}})
	for _, name := range []string{"bad name", "-x", "a"} {
		h.keys("n")
		h.typeText(name)
		h.keys("enter")
		msg, isErr := h.status()
		if !isErr || !h.screen().Capturing() {
			t.Fatalf("%q accepted: status=%q", name, msg)
		}
		h.keys("esc")
	}
	names, _ := h.store.List()
	if !reflect.DeepEqual(names, []string{"a"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestAddEditDeleteVariable(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"dev": {}}})
	// Add.
	h.keys("e")
	h.typeText("API_URL")
	h.keys("enter")
	h.typeText("http://x/'y'")
	h.keys("enter")
	if got := h.vars("dev"); !reflect.DeepEqual(got, map[string]string{"API_URL": "http://x/'y'"}) {
		t.Fatalf("after add: %v", got)
	}
	if h.screen().Capturing() {
		t.Fatal("still in edit mode after save")
	}
	// Edit the selected variable: key and value are prefilled.
	s := h.screen()
	if s.focus != paneVars || s.selectedVar() != "API_URL" {
		t.Fatalf("focus=%v var=%q", s.focus, s.selectedVar())
	}
	h.keys("e")
	if s.keyInput.Value() != "API_URL" || s.valueInput.Value() != "http://x/'y'" {
		t.Fatalf("prefill key=%q value=%q", s.keyInput.Value(), s.valueInput.Value())
	}
	h.keys("enter", "ctrl+u")
	h.typeText("v2")
	h.keys("enter")
	if got := h.vars("dev"); got["API_URL"] != "v2" || len(got) != 1 {
		t.Fatalf("after edit: %v", got)
	}
	// Add a second one, then delete the selected one.
	h.keys("h", "e")
	h.typeText("B")
	h.keys("tab")
	h.typeText("2")
	h.keys("enter")
	if got := h.vars("dev"); len(got) != 2 || got["B"] != "2" {
		t.Fatalf("after second add: %v", got)
	}
	if s.selectedVar() != "B" {
		t.Fatalf("selected var %q", s.selectedVar())
	}
	h.keys("x")
	if got := h.vars("dev"); !reflect.DeepEqual(got, map[string]string{"API_URL": "v2"}) {
		t.Fatalf("after delete: %v", got)
	}
	// x with the list focused does nothing.
	h.keys("h", "x")
	if _, isErr := h.status(); !isErr || len(h.vars("dev")) != 1 {
		t.Fatalf("x on list pane deleted a variable: %v", h.vars("dev"))
	}
	if h.quit {
		t.Fatal("program quit")
	}
}

func TestEditVariableRejectsReservedAndInvalidNames(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"dev": {"A": "1"}}})
	for _, k := range []string{"ENVMGR_PROFILE", "envmgr_x", "1BAD", "A-B", ""} {
		h.keys("e")
		h.typeText(k)
		h.keys("enter")
		msg, isErr := h.status()
		if !isErr || h.screen().mode != modeEditKey {
			t.Fatalf("%q accepted (status %q, mode %v)", k, msg, h.screen().mode)
		}
		// Forcing the value field and saving is rejected too.
		h.keys("tab")
		h.typeText("v")
		h.keys("enter")
		if _, isErr := h.status(); !isErr || !h.screen().Capturing() {
			t.Fatalf("%q saved through the value field", k)
		}
		h.keys("esc")
	}
	if got := h.vars("dev"); !reflect.DeepEqual(got, map[string]string{"A": "1"}) {
		t.Fatalf("store changed: %v", got)
	}
}

func TestDeleteProfileConfirmation(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"a": {}, "b": {}}})
	h.keys("D")
	if !strings.Contains(h.view(), `Delete profile "a"? (y/n)`) {
		t.Fatalf("no confirmation shown:\n%s", h.view())
	}
	h.keys("x", "enter") // ignored while confirming
	h.keys("n")
	if names, _ := h.store.List(); len(names) != 2 {
		t.Fatalf("deleted without y: %v", names)
	}
	h.keys("D", "esc")
	if h.quit {
		t.Fatal("esc in confirmation quit the program")
	}
	h.keys("D", "y")
	names, _ := h.store.List()
	if !reflect.DeepEqual(names, []string{"b"}) {
		t.Fatalf("names = %v", names)
	}
	if h.screen().selected() != "b" {
		t.Fatalf("selected %q", h.screen().selected())
	}
}

func TestActivateReturnsUseCode(t *testing.T) {
	for _, kind := range []shell.Kind{shell.Bash, shell.PowerShell} {
		env := map[string]string{"MY_URL": "orig"}
		vars := map[string]string{"MY_URL": "http://x/'y'", "TOKEN": "té"}
		h := newHarness(t, setupOpts{kind: kind, env: env, profiles: map[string]map[string]string{"a": {}, "dev": vars}})
		h.keys("down", "enter")
		if !h.quit {
			t.Fatalf("%s: enter did not exit", kind)
		}
		want, err := shell.Use(kind, shell.MapLookup(env), profile.Profile{Name: "dev", Vars: vars})
		if err != nil {
			t.Fatal(err)
		}
		res := h.app.Result()
		if res.Code != want || res.Action != ActionActivate || res.Profile != "dev" {
			t.Fatalf("%s: result %+v\nwant code %q", kind, res, want)
		}
		if h.app.View() != "" {
			t.Fatalf("%s: view not cleared on exit", kind)
		}
	}
	// "a" key activates too.
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"dev": {"X": "1"}}})
	h.keys("a")
	if !h.quit || h.app.Result().Action != ActionActivate {
		t.Fatalf("a did not activate: %+v", h.app.Result())
	}
}

func TestActivateErrorStaysOpen(t *testing.T) {
	// cmd cannot represent a newline: the error is shown, nothing is emitted.
	h := newHarness(t, setupOpts{kind: shell.Cmd, profiles: map[string]map[string]string{"dev": {"X": "a\nb"}}})
	h.keys("enter")
	if h.quit || h.app.Result().Code != "" {
		t.Fatal("exited with code despite a render error")
	}
	if _, isErr := h.status(); !isErr {
		t.Fatal("no error status")
	}
}

func TestDeactivateReturnsCode(t *testing.T) {
	env := map[string]string{"ENVMGR_PROFILE": "dev", "ENVMGR_VARS": "MY_URL", "ENVMGR_PREV_MY_URL": "orig", "MY_URL": "new"}
	for _, kind := range []shell.Kind{shell.Bash, shell.PowerShell} {
		h := newHarness(t, setupOpts{kind: kind, env: env, profiles: map[string]map[string]string{"dev": {"MY_URL": "new"}}})
		h.keys("d")
		want, _ := shell.Deactivate(kind, shell.MapLookup(env))
		res := h.app.Result()
		if !h.quit || res.Code != want || res.Action != ActionDeactivate || want == "" {
			t.Fatalf("%s: quit=%v result=%+v want %q", kind, h.quit, res, want)
		}
	}
	// Nothing active: stays open with a message.
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"dev": {}}})
	h.keys("d")
	if h.quit {
		t.Fatal("deactivate with nothing active exited")
	}
}

func TestQuitProducesNoOutput(t *testing.T) {
	for _, seq := range [][]string{{"q"}, {"esc"}, {"ctrl+c"}, {"e", "ctrl+c"}, {"r", "down", "q"}} {
		h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"dev": {"X": "1"}}})
		h.keys(seq...)
		if !h.quit {
			t.Fatalf("%v did not quit", seq)
		}
		if res := h.app.Result(); res != (Result{}) {
			t.Fatalf("%v produced %+v", seq, res)
		}
	}
}

func TestStdoutTerminalShowsNoticeInsteadOfCode(t *testing.T) {
	h := newHarness(t, setupOpts{stdoutTTY: true, profiles: map[string]map[string]string{"dev": {"X": "1"}}})
	h.app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.keys("enter")
	if h.quit || h.app.Result().Code != "" {
		t.Fatal("exited with code although stdout is a terminal")
	}
	one := `eval "$(envmgr use dev --shell bash)"`
	if v := h.view(); !strings.Contains(v, one) || !strings.Contains(v, `eval "$(envmgr tui --shell bash)"`) {
		t.Fatalf("notice does not show the one-liners:\n%s", v)
	}
	h.keys("c")
	if !reflect.DeepEqual(h.copied, []string{one}) {
		t.Fatalf("copied %v", h.copied)
	}
	if h.screen().Capturing() {
		t.Fatal("notice still open after copy")
	}
	// The default status line tells how to wrap the command.
	if msg, _ := h.status(); !strings.Contains(msg, "copied") {
		t.Fatalf("status %q", msg)
	}
	h.keys("down")
	if msg, _ := h.status(); !strings.Contains(msg, `eval "$(envmgr tui --shell bash)"`) {
		t.Fatalf("status hint %q", msg)
	}
}

func TestPowerShellNoticeOneLiner(t *testing.T) {
	h := newHarness(t, setupOpts{kind: shell.PowerShell, stdoutTTY: true, profiles: map[string]map[string]string{"dev": {}}})
	h.app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.keys("enter", "c")
	want := "envmgr use dev --shell powershell | Invoke-Expression"
	if !reflect.DeepEqual(h.copied, []string{want}) {
		t.Fatalf("copied %v", h.copied)
	}
}

func TestFilter(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"alpha": {}, "beta": {}, "gamma": {}, "delta": {}}})
	h.keys("/")
	h.typeText("ta")
	s := h.screen()
	if !reflect.DeepEqual(s.visible, []string{"beta", "delta"}) {
		t.Fatalf("visible %v", s.visible)
	}
	// q is filter text, not quit.
	h.typeText("q")
	if h.quit || len(s.visible) != 0 {
		t.Fatalf("quit=%v visible=%v", h.quit, s.visible)
	}
	h.keys("backspace", "enter")
	if s.Capturing() || s.filter != "ta" || s.selected() != "beta" {
		t.Fatalf("capturing=%v filter=%q selected=%q", s.Capturing(), s.filter, s.selected())
	}
	h.keys("down")
	if s.selected() != "delta" {
		t.Fatalf("selected %q", s.selected())
	}
	// Activation uses the filtered selection.
	h.keys("/", "esc")
	if s.filter != "" || len(s.visible) != 4 || s.selected() != "delta" {
		t.Fatalf("esc did not clear: filter=%q visible=%v selected=%q", s.filter, s.visible, s.selected())
	}
}

func TestHelpOverlay(t *testing.T) {
	h := newHarness(t, setupOpts{profiles: map[string]map[string]string{"dev": {}}})
	for _, w := range []int{MinWidth, 80, 200} {
		h.app.Update(tea.WindowSizeMsg{Width: w, Height: MinHeight})
		lines := strings.Split(h.view(), "\n")
		if bar := lines[len(lines)-1]; !strings.Contains(bar, "? help") || !strings.Contains(bar, "q quit") || !strings.Contains(bar, "enter activate") {
			t.Fatalf("width %d: help bar %q", w, bar)
		}
	}
	h.app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.keys("?")
	v := h.view()
	if !strings.Contains(v, "delete the selected profile") || !strings.Contains(v, "stdout") {
		t.Fatalf("help overlay incomplete:\n%s", v)
	}
	h.keys("q") // closes the overlay, does not quit
	if h.quit || h.app.showHelp {
		t.Fatalf("quit=%v showHelp=%v", h.quit, h.app.showHelp)
	}
}

func TestViewFitsAnySize(t *testing.T) {
	profiles := map[string]map[string]string{
		"a-very-long-profile-name-that-does-not-fit": {"A_VERY_LONG_VARIABLE_NAME_INDEED": strings.Repeat("v", 300), "B": "x"},
	}
	for _, n := range strings.Split("b c d e f g h i j k l m n o p q r s t", " ") {
		profiles[n] = map[string]string{}
	}
	sizes := [][2]int{{120, 40}, {80, 24}, {60, 15}, {59, 14}, {40, 10}, {30, 8}, {20, 5}, {10, 3}}
	modes := [][]string{nil, {"r"}, {"l", "j"}, {"/"}, {"n"}, {"e"}, {"D"}, {"?"}, {"G"}}
	for _, sz := range sizes {
		for _, m := range modes {
			h := newHarness(t, setupOpts{stdoutTTY: true, profiles: profiles})
			h.app.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			h.keys(m...)
			checkFits(t, h.view(), sz[0], sz[1], m)
		}
		// The activation notice too.
		h := newHarness(t, setupOpts{stdoutTTY: true, profiles: profiles})
		h.app.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		h.keys("enter")
		checkFits(t, h.view(), sz[0], sz[1], "notice")
	}
}

func checkFits(t *testing.T, v string, w, h int, ctx any) {
	t.Helper()
	lines := strings.Split(v, "\n")
	if len(lines) != h {
		t.Errorf("%dx%d %v: %d lines\n%s", w, h, ctx, len(lines), v)
		return
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%dx%d %v: line %d is %d wide: %q", w, h, ctx, i, lw, l)
		}
	}
}

func TestWrapCommand(t *testing.T) {
	cases := map[shell.Kind]string{
		shell.Bash:       `eval "$(envmgr use dev --shell bash)"`,
		shell.Zsh:        `eval "$(envmgr use dev --shell zsh)"`,
		shell.Fish:       `envmgr use dev --shell fish | source`,
		shell.PowerShell: `envmgr use dev --shell powershell | Invoke-Expression`,
		shell.Cmd:        `envmgr use dev --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"`,
	}
	for k, want := range cases {
		if got := WrapCommand(k, "envmgr use dev --shell "+string(k)); got != want {
			t.Errorf("%s: %q want %q", k, got, want)
		}
	}
}

func TestPrintable(t *testing.T) {
	rlo, eAcute := string(rune(0x202e)), string(rune(0xe9))
	if got := printable("a\tb\x00c" + rlo + "d" + eAcute); got != `a\tb\x00c`+`\`+"u202ed"+eAcute {
		t.Fatalf("got %q", got)
	}
}
