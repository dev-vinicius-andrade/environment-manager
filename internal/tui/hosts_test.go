package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// Never touch the real hosts file from these tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "envmgr-tui-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(hosts.EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const tuiHosts = "127.0.0.1 localhost\n# keep me\n"

// newHostsHarness builds an App over a store with profile dev (one variable,
// one hosts entry) and plain (variables only), and a temp hosts file.
func newHostsHarness(t *testing.T, stdoutTTY bool) (*harness, string) {
	t.Helper()
	store := &profile.Store{Dir: t.TempDir()}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.SetVar("dev", "URL", "http://dev"))
	must(store.SetHostEntry("dev", profile.HostEntry{IP: "10.0.0.5", Names: []string{"api.dev.local"}, Comment: "cluster"}))
	must(store.SetVar("plain", "URL", "http://plain"))
	hf := filepath.Join(t.TempDir(), "hosts")
	must(os.WriteFile(hf, []byte(tuiHosts), 0o644))
	h := &harness{t: t, store: store}
	h.app = New(Options{
		Store:            store,
		Shell:            shell.Bash,
		Lookup:           shell.MapLookup(map[string]string{}),
		StdoutIsTerminal: stdoutTTY,
		HostsFile:        hf,
		Copy:             func(s string) error { h.copied = append(h.copied, s); return nil },
	})
	return h, hf
}

func readHosts(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSectionsAndTab(t *testing.T) {
	h, _ := newHostsHarness(t, false)
	s := h.screen()
	if s.selected() != "dev" || s.section != sectionVars {
		t.Fatalf("initial %q %v", s.selected(), s.section)
	}
	v := h.view()
	if !strings.Contains(v, "[Variables (1)]") || !strings.Contains(v, "Hosts (1)") {
		t.Fatalf("section line missing:\n%s", v)
	}
	// tab with the list focused keeps the old meaning (screens): no section change.
	h.keys("tab")
	if s.section != sectionVars || h.quit {
		t.Fatal("tab on the list changed the section")
	}
	h.keys("l", "tab")
	if s.section != sectionHosts {
		t.Fatal("tab on the detail pane did not switch to Hosts")
	}
	v = h.view()
	if !strings.Contains(v, "[Hosts (1)]") || !strings.Contains(v, "10.0.0.5") || !strings.Contains(v, "api.dev.local  # cluster") {
		t.Fatalf("hosts section:\n%s", v)
	}
	h.send(tea.KeyMsg{Type: tea.KeyShiftTab})
	if s.section != sectionVars {
		t.Fatal("shift+tab did not switch back")
	}
	// Existing keys still work: j/k in variables, h back to the list.
	h.keys("h", "j")
	if s.selected() != "plain" {
		t.Fatalf("navigation broken: %q", s.selected())
	}
}

func TestAddEditDeleteHostEntry(t *testing.T) {
	h, _ := newHostsHarness(t, false)
	s := h.screen()
	h.keys("l", "tab") // Hosts section, detail pane focused
	// Add a new entry: focus the list so e starts an empty form.
	h.keys("h", "e")
	if s.mode != modeEditHost || s.hostIP.Value() != "" {
		t.Fatalf("form not empty: mode=%v ip=%q", s.mode, s.hostIP.Value())
	}
	// Invalid IP is refused in place.
	h.typeText("10.0.0.256")
	h.keys("enter")
	if msg, isErr := h.status(); !isErr || s.hostField != hostFieldIP {
		t.Fatalf("bad ip accepted: %q", msg)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.typeText("10.0.0.6")
	h.keys("enter")
	h.typeText("web.dev.local a#b")
	h.keys("enter")
	if _, isErr := h.status(); !isErr || s.hostField != hostFieldNames {
		t.Fatal("bad hostname accepted")
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.typeText("web.dev.local WWW.dev.local")
	h.keys("enter", "enter") // names, then the empty comment saves
	if s.mode != modeNormal {
		t.Fatalf("not saved: %v", h.app.View())
	}
	p, _ := h.store.Get("dev")
	if len(p.Hosts) != 2 || p.Hosts[1].IP != "10.0.0.6" || strings.Join(p.Hosts[1].Names, " ") != "web.dev.local www.dev.local" {
		t.Fatalf("store %+v", p.Hosts)
	}
	if s.section != sectionHosts || s.focus != paneVars || s.hostCursor != 1 {
		t.Fatalf("cursor %v %v %d", s.section, s.focus, s.hostCursor)
	}
	// Edit the selected entry: prefilled, the comment can be replaced.
	h.keys("k", "e")
	if s.hostIP.Value() != "10.0.0.5" || s.hostNames.Value() != "api.dev.local" || s.hostComment.Value() != "cluster" {
		t.Fatalf("prefill %q %q %q", s.hostIP.Value(), s.hostNames.Value(), s.hostComment.Value())
	}
	h.keys("tab", "tab")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.keys("enter")
	if p, _ = h.store.Get("dev"); p.Hosts[len(p.Hosts)-1].Comment != "" || len(p.Hosts) != 2 {
		t.Fatalf("edit %+v", p.Hosts)
	}
	// Critical names are refused on save.
	h.keys("e", "tab")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.typeText("localhost")
	h.keys("enter", "enter")
	if msg, isErr := h.status(); !isErr || !strings.Contains(msg, "critical") {
		t.Fatalf("critical: %q", msg)
	}
	h.keys("esc")
	// Delete the selected entry; variables untouched.
	before := len(s.current.Hosts)
	h.keys("x")
	if p, _ = h.store.Get("dev"); len(p.Hosts) != before-1 || p.Vars["URL"] != "http://dev" {
		t.Fatalf("delete %+v", p)
	}
}

func TestScopeChooser(t *testing.T) {
	t.Run("profile without hosts activates directly", func(t *testing.T) {
		h, hf := newHostsHarness(t, false)
		h.keys("j", "enter")
		if !h.quit || h.app.Result().Profile != "plain" || readHosts(t, hf) != tuiHosts {
			t.Fatalf("quit=%v result=%+v", h.quit, h.app.Result())
		}
	})
	t.Run("environment (e and enter)", func(t *testing.T) {
		for _, key := range []string{"e", "enter"} {
			h, hf := newHostsHarness(t, false)
			h.keys("enter")
			if h.screen().mode != modeScope || h.quit {
				t.Fatal("no scope dialog")
			}
			if !strings.Contains(h.view(), `Activate "dev"`) {
				t.Fatalf("dialog not drawn:\n%s", h.view())
			}
			h.keys(key)
			p, _ := h.store.Get("dev")
			want, _ := shell.Use(shell.Bash, shell.MapLookup(map[string]string{}), p)
			if !h.quit || h.app.Result().Code != want || readHosts(t, hf) != tuiHosts {
				t.Fatalf("%s: result %+v", key, h.app.Result())
			}
		}
	})
	t.Run("hosts only stays open", func(t *testing.T) {
		h, hf := newHostsHarness(t, false)
		h.keys("enter", "h")
		if h.quit || !strings.Contains(readHosts(t, hf), "# group: profile:dev\n10.0.0.5 api.dev.local\n") {
			t.Fatalf("quit=%v file=%q", h.quit, readHosts(t, hf))
		}
		if msg, isErr := h.status(); isErr || !strings.Contains(msg, "applied hosts") {
			t.Fatalf("status %q", msg)
		}
		if st, _ := hostsapply.ReadState(h.store.Dir); st.Profile != "dev" {
			t.Fatalf("state %+v", st)
		}
	})
	t.Run("both", func(t *testing.T) {
		h, hf := newHostsHarness(t, false)
		h.keys("enter", "b")
		r := h.app.Result()
		if !h.quit || !strings.Contains(r.Code, "export URL='http://dev'") || !strings.Contains(readHosts(t, hf), "profile:dev") || !strings.Contains(r.Message, "applied hosts") {
			t.Fatalf("result %+v", r)
		}
	})
	t.Run("hosts failure is shown in the footer, nothing printed", func(t *testing.T) {
		h, _ := newHostsHarness(t, false)
		h.app.env.hostsFile = filepath.Join(t.TempDir(), "missing")
		h.keys("enter", "b")
		msg, isErr := h.status()
		if h.quit || !isErr || !strings.Contains(msg, "not applied") || h.app.Result().Code != "" {
			t.Fatalf("quit=%v status=%q", h.quit, msg)
		}
		h.keys("enter", "h")
		if _, isErr := h.status(); !isErr || h.quit {
			t.Fatal("hosts only failure")
		}
	})
	t.Run("cancel", func(t *testing.T) {
		h, hf := newHostsHarness(t, false)
		h.keys("enter", "esc")
		if h.quit || h.screen().mode != modeNormal || readHosts(t, hf) != tuiHosts {
			t.Fatal("cancel")
		}
	})
	t.Run("stdout is a terminal: hosts applied, env notice", func(t *testing.T) {
		h, hf := newHostsHarness(t, true)
		h.keys("enter", "b")
		if h.quit || h.screen().mode != modeNotice || !strings.Contains(readHosts(t, hf), "profile:dev") {
			t.Fatalf("quit=%v mode=%v", h.quit, h.screen().mode)
		}
	})
}

func TestHostsViewsFit(t *testing.T) {
	sizes := [][2]int{{120, 40}, {80, 24}, {60, 15}, {59, 14}, {40, 10}, {30, 8}, {20, 5}, {10, 3}}
	modes := [][]string{{"l", "tab"}, {"l", "tab", "e"}, {"enter"}, {"l", "tab", "?"}}
	for _, sz := range sizes {
		for _, m := range modes {
			h, _ := newHostsHarness(t, true)
			h.app.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			h.keys(m...)
			checkFits(t, h.view(), sz[0], sz[1], m)
		}
	}
}
