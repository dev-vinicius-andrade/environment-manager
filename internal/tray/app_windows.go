//go:build windows

package tray

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/selected"
)

const (
	createNoWindow        = 0x08000000
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createNewConsole      = 0x00000010
	runKeyPath            = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// Running reports whether a tray for home is already running.
func Running(home string) bool {
	name, err := windows.UTF16PtrFromString(MutexName(home))
	if err != nil {
		return false
	}
	const synchronize = 0x00100000
	h, err := windows.OpenMutex(synchronize, false, name)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// Launch starts exe with args detached from the console (no window), so the
// tray outlives the terminal that started it. It returns the new PID.
func Launch(exe string, args []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | detachedProcess | createNewProcessGroup,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

type app struct {
	opts Options
	sel  *selected.Selector
	log  *log.Logger

	mu    sync.Mutex
	model Model
	ready bool

	slots     [MaxProfiles]*systray.MenuItem // profile items (each has a submenu)
	slotEnv   [MaxProfiles]*systray.MenuItem // "Environment"
	slotHosts [MaxProfiles]*systray.MenuItem // "Hosts"
	slotApply [MaxProfiles]*systray.MenuItem // "Apply hosts (asks for administrator)..."
	more      *systray.MenuItem
	empty     *systray.MenuItem
	none      *systray.MenuItem
	openTUI   *systray.MenuItem
	folder    *systray.MenuItem
	autostart *systray.MenuItem
	quit      *systray.MenuItem
	stop      chan struct{}

	// shown tracks which optional items are in the menu: hiding an item that
	// is already hidden makes fyne.io/systray log an error.
	shown map[*systray.MenuItem]bool
}

func (a *app) hide(it *systray.MenuItem) {
	if a.shown[it] {
		it.Hide()
		a.shown[it] = false
	}
}

// show marks it visible; any update (SetTitle, Check) has already re-added it.
func (a *app) show(it *systray.MenuItem) {
	if !a.shown[it] {
		it.Show()
		a.shown[it] = true
	}
}

// Run shows the tray icon and blocks until Quit. It must be called from the
// main goroutine. Only one tray per envmgr home runs (named mutex).
func Run(o Options) error {
	if o.Store == nil {
		return errors.New("tray: no profile store")
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	name, err := windows.UTF16PtrFromString(MutexName(o.Store.Dir))
	if err != nil {
		return err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return ErrAlreadyRunning
	}
	if err != nil {
		return fmt.Errorf("tray: single-instance mutex: %w", err)
	}
	defer windows.CloseHandle(h)

	w := o.Log
	if w == nil {
		w = io.Discard
	}
	a := &app{opts: o, sel: selected.For(o.Store), log: log.New(w, "", log.LstdFlags|log.Lmicroseconds), stop: make(chan struct{})}
	a.log.Printf("start pid=%d home=%s exe=%s", os.Getpid(), o.Store.Dir, o.Exe)
	systray.Run(a.onReady, a.onExit)
	a.log.Printf("exit")
	return nil
}

func (a *app) onReady() {
	systray.SetIcon(Icon(false))
	systray.SetTooltip("envmgr")
	a.shown = map[*systray.MenuItem]bool{}
	for i := range a.slots {
		a.slots[i] = systray.AddMenuItemCheckbox("-", "Choose what this profile applies", false)
		a.slotEnv[i] = a.slots[i].AddSubMenuItemCheckbox(TitleEnv, "Following shells activate this profile's variables", false)
		a.slotHosts[i] = a.slots[i].AddSubMenuItemCheckbox(TitleHosts, "Select this profile's hosts entries (the hosts file is written only by Apply hosts)", false)
		a.slotApply[i] = a.slots[i].AddSubMenuItem(TitleApplyHosts, "Write this profile's hosts entries into the hosts file (Windows asks for administrator rights)")
		a.shown[a.slots[i]] = true
		a.hide(a.slots[i])
	}
	a.more = systray.AddMenuItem("-", "")
	a.more.Disable()
	a.shown[a.more] = true
	a.hide(a.more)
	a.empty = systray.AddMenuItem(TitleEmpty, "")
	a.empty.Disable()
	a.shown[a.empty] = true
	a.hide(a.empty)
	a.none = systray.AddMenuItemCheckbox(TitleNone, "Following shells deactivate their profile", false)
	systray.AddSeparator()
	a.openTUI = systray.AddMenuItem(TitleOpenTUI, "Open envmgr's terminal UI")
	a.folder = systray.AddMenuItem(TitleOpenFolder, "Open the folder that holds the profiles")
	a.autostart = systray.AddMenuItemCheckbox(TitleAutostart, "Start the tray when you sign in", false)
	a.quit = systray.AddMenuItem(TitleQuit, "Close the tray icon")

	a.mu.Lock()
	a.ready = true
	a.mu.Unlock()
	a.refresh(true)

	for i := range a.slots {
		go a.listen(a.slotEnv[i], Target{Kind: TargetEnv, Slot: i})
		go a.listen(a.slotHosts[i], Target{Kind: TargetHosts, Slot: i})
		go a.listen(a.slotApply[i], Target{Kind: TargetApplyHosts, Slot: i})
	}
	go a.listen(a.none, Target{Kind: TargetNone})
	go a.listen(a.openTUI, Target{Kind: TargetOpenTUI})
	go a.listen(a.folder, Target{Kind: TargetOpenFolder})
	go a.listen(a.autostart, Target{Kind: TargetAutostart})
	go a.listen(a.quit, Target{Kind: TargetQuit})
	go a.poll()
}

func (a *app) onExit() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
}

func (a *app) listen(item *systray.MenuItem, t Target) {
	for {
		select {
		case <-a.stop:
			return
		case _, ok := <-item.ClickedCh:
			if !ok {
				return
			}
			a.click(t)
		}
	}
}

func (a *app) poll() {
	tk := time.NewTicker(a.opts.Poll)
	defer tk.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-tk.C:
			a.refresh(false)
		}
	}
}

// readState reads the store, the selection and the autostart flag. Read
// errors keep the previous values (a profile file being written, ...).
func (a *app) readState(prev Model) State {
	st := State{Autostart: autostartEnabled()}
	names, err := a.opts.Store.List()
	if err != nil {
		a.log.Printf("list profiles: %v", err)
		for _, s := range prev.Slots {
			if s.Visible {
				names = append(names, s.Profile)
			}
		}
	}
	st.Profiles = names
	sel, err := a.sel.Get()
	if err != nil {
		a.log.Printf("read selection: %v", err)
	}
	st.Selected, st.SelectedEnv, st.SelectedHosts = sel.Profile, sel.Env, sel.Hosts
	if ap, err := hostsapply.ReadState(a.opts.Store.Dir); err == nil {
		st.HostsApplied = ap.Profile
	} else {
		a.log.Printf("read hosts-applied state: %v", err)
	}
	return st
}

func (a *app) refresh(force bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ready {
		return
	}
	next := Build(a.readState(a.model))
	var changes []Change
	if force {
		changes = AllChanges()
	} else {
		changes = Diff(a.model, next)
	}
	if len(changes) == 0 {
		return
	}
	a.model = next
	a.apply(next, changes)
	a.log.Printf("model %s (%d changes)", next.Summary(), len(changes))
}

// apply pushes changes to the systray items. A hidden item is never updated
// (any update shows it again in fyne.io/systray on Windows).
func (a *app) apply(m Model, changes []Change) {
	for _, c := range changes {
		switch c.What {
		case "slot":
			s, it := m.Slots[c.Slot], a.slots[c.Slot]
			if !s.Visible {
				a.hide(it)
				continue
			}
			it.SetTitle(s.Profile)
			setChecked(it, s.Checked)
			setChecked(a.slotEnv[c.Slot], s.Env)
			setChecked(a.slotHosts[c.Slot], s.Hosts)
			a.show(it)
		case "more":
			if m.More == "" {
				a.hide(a.more)
			} else {
				a.more.SetTitle(m.More)
				a.show(a.more)
			}
		case "empty":
			if m.Empty {
				a.show(a.empty)
			} else {
				a.hide(a.empty)
			}
		case "none":
			setChecked(a.none, m.NoneChecked)
		case "autostart":
			setChecked(a.autostart, m.Autostart)
		case "tooltip":
			systray.SetTooltip(m.Tooltip)
		case "icon":
			systray.SetIcon(Icon(m.HasSelection))
		}
	}
}

func setChecked(it *systray.MenuItem, on bool) {
	if on {
		it.Check()
	} else {
		it.Uncheck()
	}
}

func (a *app) click(t Target) {
	a.mu.Lock()
	act := React(a.model, t)
	a.mu.Unlock()
	a.log.Printf("click %+v -> %+v", t, act)
	var err error
	switch act.Kind {
	case ActSelect:
		err = a.sel.SetScoped(act.Profile, selected.Scopes{Env: act.Env, Hosts: act.Hosts})
	case ActApplyHosts:
		// Never written by the tray itself: an explicit UAC prompt starts an
		// elevated `envmgr hosts apply` window (nothing happens if the user
		// declines it).
		err = runElevated(a.opts.Exe, ElevatedApplyArgs(a.opts.Store.Dir, a.opts.HostsFile, act.Profile))
	case ActClear:
		err = a.sel.Clear()
	case ActOpenTUI:
		err = openTUI(a.opts.Exe)
	case ActOpenFolder:
		err = openFolder(filepath.Join(a.opts.Store.Dir, "profiles"))
	case ActSetAutostart:
		err = setAutostart(act.Enable, a.opts.Exe)
	case ActQuit:
		systray.Quit()
		return
	}
	if err != nil {
		a.log.Printf("action failed: %v", err)
	}
	a.refresh(false)
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(AutostartValue)
	return err == nil
}

// setAutostart adds or removes the HKCU Run value (no admin rights needed).
func setAutostart(enable bool, exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		return k.SetStringValue(AutostartValue, AutostartCommand(exe))
	}
	if err := k.DeleteValue(AutostartValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// openTUI opens a terminal running `envmgr tui`: a Windows Terminal tab when wt
// is available (and ENVMGR_HOME is not overridden: wt may start the tab from an
// already running Windows Terminal that does not have this process's
// environment), otherwise a new PowerShell console window.
func openTUI(exe string) error {
	if os.Getenv("ENVMGR_HOME") == "" {
		if wt, err := exec.LookPath("wt.exe"); err == nil {
			cmd := exec.Command(wt, WTArgs(exe)...)
			if err := cmd.Start(); err == nil {
				return cmd.Process.Release()
			}
		}
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		return err
	}
	cmd := exec.Command(ps, "-NoLogo", "-NoExit", "-Command", PowerShellTUICommand(exe))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func openFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runElevated starts exe with args through ShellExecute's "runas" verb: Windows
// shows its UAC consent prompt and starts nothing when the user declines.
// envmgr stores no credentials and never elevates silently.
func runElevated(exe string, args []string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(WindowsCommandLine(args))
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
}
