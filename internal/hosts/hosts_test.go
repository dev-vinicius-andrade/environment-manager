package hosts

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Never touch the real hosts file: any default resolution in this package's
// tests lands on a path inside a temp dir that does not exist.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "envmgr-hosts-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const lfFixture = "# system hosts\n127.0.0.1\tlocalhost\n::1 localhost ip6-localhost   # trailing comment\n\n  # indented comment  \n10.1.1.1 printer.lan\n"

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

func writeTemp(t *testing.T, content string) *File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &File{Path: p, LockWait: 200 * time.Millisecond, Now: func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}}
}

func read(t *testing.T, f *File) string {
	t.Helper()
	b, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func add(ip string, names []string, group string) func(*Doc) (bool, error) {
	return func(d *Doc) (bool, error) { return d.Add(ip, names, group, false) }
}

func mustUpdate(t *testing.T, f *File, fn func(*Doc) (bool, error)) Change {
	t.Helper()
	ch, err := f.Update(false, fn)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestManagedBlockPreservesUnmanagedBytes(t *testing.T) {
	for name, fixture := range map[string]string{
		"LF":                lfFixture,
		"CRLF":              crlf(lfFixture),
		"LF no final EOL":   strings.TrimSuffix(lfFixture, "\n"),
		"CRLF no final EOL": strings.TrimSuffix(crlf(lfFixture), "\r\n"),
		"BOM":               "\xEF\xBB\xBF" + lfFixture,
	} {
		t.Run(name, func(t *testing.T) {
			f := writeTemp(t, fixture)
			eol := "\n"
			if strings.Contains(fixture, "\r\n") {
				eol = "\r\n"
			}
			endsEOL := strings.HasSuffix(fixture, "\n")
			ch := mustUpdate(t, f, add("10.0.0.5", []string{"API.dev.local", "db.dev.local"}, ""))
			if !ch.Changed || ch.Backup == "" {
				t.Fatalf("change %+v", ch)
			}
			got := read(t, f)
			prefix := fixture
			if !endsEOL {
				prefix += eol
			}
			if !strings.HasPrefix(got, prefix) {
				t.Fatalf("unmanaged bytes changed:\n%q\n%q", got, fixture)
			}
			block := got[len(prefix):]
			want := strings.Join([]string{BeginMarker, headerComment, "# group: default", "10.0.0.5 api.dev.local db.dev.local", EndMarker}, eol)
			if endsEOL {
				want += eol
			}
			if block != want {
				t.Fatalf("block\n%q\nwant\n%q", block, want)
			}
			if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\r") || (eol == "\n" && strings.Contains(got, "\r")) {
				t.Fatalf("mixed line endings: %q", got)
			}
			// Further edits keep everything outside the block, including what
			// follows the block when it is not at the end.
			sep := ""
			if !endsEOL {
				sep = eol
			}
			_ = os.WriteFile(f.Path, []byte(got+sep+"# after the block"+eol+"10.9.9.9 after.lan"), 0o644)
			mustUpdate(t, f, add("10.0.0.6", []string{"web.dev.local"}, "dev"))
			mustUpdate(t, f, func(d *Doc) (bool, error) { return d.SetGroupEnabled("dev", false) })
			mustUpdate(t, f, func(d *Doc) (bool, error) { return d.Remove("api.dev.local", "") })
			got2 := read(t, f)
			if !strings.HasPrefix(got2, prefix+BeginMarker+eol) || !strings.HasSuffix(got2, EndMarker+eol+"# after the block"+eol+"10.9.9.9 after.lan") {
				t.Fatalf("after edits:\n%q", got2)
			}
			doc, err := f.Read()
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Groups) != 2 || doc.Groups[0].Name != "default" || doc.Groups[1].Name != "dev" || doc.Groups[1].Enabled {
				t.Fatalf("groups %+v", doc.Groups)
			}
			if !strings.Contains(got2, "# group: dev (disabled)"+eol+"# 10.0.0.6 web.dev.local"+eol) {
				t.Fatalf("disabled group not commented:\n%q", got2)
			}
		})
	}
}

func TestEmptyFileGetsBlock(t *testing.T) {
	f := writeTemp(t, "")
	mustUpdate(t, f, add("10.0.0.1", []string{"a.local"}, ""))
	got := read(t, f)
	eol := defaultEOL()
	if !strings.HasPrefix(got, BeginMarker+eol) || !strings.HasSuffix(got, EndMarker+eol) {
		t.Fatalf("%q", got)
	}
}

func TestAddIdempotentAndMoves(t *testing.T) {
	f := writeTemp(t, lfFixture)
	mustUpdate(t, f, add("10.0.0.1", []string{"a.local", "b.local"}, "dev"))
	before := read(t, f)
	if ch := mustUpdate(t, f, add("10.0.0.1", []string{"B.LOCAL", "a.local"}, "dev")); ch.Changed {
		t.Fatalf("second add changed the file: %+v", ch)
	}
	if read(t, f) != before {
		t.Fatal("idempotent add rewrote the file")
	}
	bk, _ := f.Backups()
	if len(bk) != 1 {
		t.Fatalf("no-op wrote a backup: %v", bk)
	}
	// Moving a name to a new address in the same group.
	mustUpdate(t, f, add("10.0.0.2", []string{"b.local"}, "dev"))
	doc, _ := f.Read()
	g := doc.Group("dev")
	if len(g.Entries) != 2 || strings.Join(g.Entries[0].Hostnames, ",") != "a.local" || g.Entries[1].IP != "10.0.0.2" {
		t.Fatalf("entries %+v", g.Entries)
	}
	// Merge into an existing address.
	mustUpdate(t, f, add("10.0.0.2", []string{"c.local"}, "dev"))
	doc, _ = f.Read()
	if got := strings.Join(doc.Group("dev").Entries[1].Hostnames, ","); got != "b.local,c.local" {
		t.Fatalf("merge %q", got)
	}
	// IPv6 canonical form.
	mustUpdate(t, f, add("2001:DB8:0:0::1", []string{"v6.local"}, ""))
	if !strings.Contains(read(t, f), "2001:db8::1 v6.local") {
		t.Fatal("ipv6 not canonical")
	}
}

func TestRemove(t *testing.T) {
	f := writeTemp(t, lfFixture)
	mustUpdate(t, f, add("10.0.0.1", []string{"a.local", "b.local"}, "dev"))
	mustUpdate(t, f, add("10.0.0.3", []string{"a.local"}, "qa"))
	mustUpdate(t, f, func(d *Doc) (bool, error) { return d.Remove("a.local", "dev") })
	doc, _ := f.Read()
	if g := doc.Group("dev"); len(g.Entries) != 1 || len(g.Entries[0].Hostnames) != 1 || doc.Group("qa") == nil {
		t.Fatalf("after remove: %+v", doc.Groups)
	}
	// Without a group: every group; empty entries and groups disappear.
	mustUpdate(t, f, func(d *Doc) (bool, error) { return d.Remove("a.local", "") })
	mustUpdate(t, f, func(d *Doc) (bool, error) { return d.Remove("b.local", "") })
	doc, _ = f.Read()
	if len(doc.Groups) != 0 || !doc.HasBlock() {
		t.Fatalf("groups %+v", doc.Groups)
	}
	if !strings.HasPrefix(read(t, f), lfFixture) {
		t.Fatal("unmanaged lines changed")
	}
	if _, err := f.Update(false, func(d *Doc) (bool, error) { return d.Remove("nope.local", "") }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if _, err := f.Update(false, func(d *Doc) (bool, error) { return d.Remove("x.local", "ghost") }); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("remove from missing group: %v", err)
	}
}

func TestGroupEnableDisable(t *testing.T) {
	f := writeTemp(t, lfFixture)
	mustUpdate(t, f, add("10.0.0.1", []string{"a.local"}, "dev"))
	mustUpdate(t, f, func(d *Doc) (bool, error) { return d.SetGroupEnabled("dev", false) })
	if !strings.Contains(read(t, f), "# group: dev (disabled)\n# 10.0.0.1 a.local\n") {
		t.Fatalf("%q", read(t, f))
	}
	if ch := mustUpdate(t, f, func(d *Doc) (bool, error) { return d.SetGroupEnabled("dev", false) }); ch.Changed {
		t.Fatal("disabling twice changed the file")
	}
	// Adding to a disabled group keeps it disabled.
	mustUpdate(t, f, add("10.0.0.2", []string{"b.local"}, "dev"))
	if !strings.Contains(read(t, f), "# 10.0.0.2 b.local\n") {
		t.Fatalf("%q", read(t, f))
	}
	mustUpdate(t, f, func(d *Doc) (bool, error) { return d.SetGroupEnabled("dev", true) })
	got := read(t, f)
	if !strings.Contains(got, "# group: dev\n10.0.0.1 a.local\n10.0.0.2 b.local\n") {
		t.Fatalf("%q", got)
	}
	if _, err := f.Update(false, func(d *Doc) (bool, error) { return d.SetGroupEnabled("ghost", true) }); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group: %v", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := writeTemp(t, crlf(lfFixture))
	before := read(t, f)
	ch, err := f.Update(true, add("10.0.0.1", []string{"a.local"}, ""))
	if err != nil || !ch.Changed || ch.Backup != "" {
		t.Fatalf("%+v %v", ch, err)
	}
	if !strings.Contains(ch.Diff, "+# BEGIN envmgr\n") || !strings.Contains(ch.Diff, "+10.0.0.1 a.local\n") || !strings.Contains(ch.Diff, "@@ ") || !strings.Contains(ch.Diff, " 10.1.1.1 printer.lan\n") {
		t.Fatalf("diff:\n%s", ch.Diff)
	}
	if read(t, f) != before {
		t.Fatal("dry run wrote the file")
	}
	ents, _ := os.ReadDir(filepath.Dir(f.Path))
	if len(ents) != 1 {
		t.Fatalf("dry run left files: %v", ents)
	}
	// Restore dry run too.
	mustUpdate(t, f, add("10.0.0.1", []string{"a.local"}, ""))
	after := read(t, f)
	ch, err = f.Restore("", true)
	if err != nil || !ch.Changed || !strings.Contains(ch.Diff, "-10.0.0.1 a.local") {
		t.Fatalf("restore dry run %+v %v", ch, err)
	}
	if read(t, f) != after {
		t.Fatal("restore dry run wrote the file")
	}
}

func TestBackupRotationAndRestore(t *testing.T) {
	f := writeTemp(t, lfFixture)
	for i := 0; i < 12; i++ {
		mustUpdate(t, f, add("10.0.0.1", []string{"h" + string(rune('a'+i)) + ".local"}, ""))
	}
	list, err := f.Backups()
	if err != nil || len(list) != DefaultKeep {
		t.Fatalf("backups %d %v", len(list), err)
	}
	for _, b := range list {
		if !strings.HasPrefix(filepath.Base(b), "hosts"+BackupInfix) {
			t.Fatalf("backup name %s", b)
		}
	}
	// The newest backup holds the state before the last write.
	latest, _ := os.ReadFile(list[len(list)-1])
	if !strings.Contains(string(latest), "hk.local") || strings.Contains(string(latest), "hl.local") {
		t.Fatalf("latest backup content %q", latest)
	}
	cur := read(t, f)
	ch, err := f.Restore("", false)
	if err != nil || !ch.Changed || ch.Backup == "" {
		t.Fatalf("restore %+v %v", ch, err)
	}
	if read(t, f) != string(latest) {
		t.Fatal("restore content")
	}
	// The restore backed up what it replaced, so it can be undone by name.
	if _, err := f.Restore(filepath.Base(ch.Backup), false); err != nil {
		t.Fatal(err)
	}
	if read(t, f) != cur {
		t.Fatal("undo restore")
	}
	if list, _ := f.Backups(); len(list) != DefaultKeep {
		t.Fatalf("rotation after restore: %d", len(list))
	}
	for _, bad := range []string{"../hosts", "hosts", filepath.Join(t.TempDir(), "hosts"+BackupInfix+"x"), "nope"} {
		if _, err := f.Restore(bad, false); err == nil {
			t.Fatalf("restore %q accepted", bad)
		}
	}
	// Manual backup.
	bk, err := f.Backup()
	if err != nil || !strings.Contains(bk, BackupInfix) {
		t.Fatalf("backup %q %v", bk, err)
	}
	if b, _ := os.ReadFile(bk); string(b) != cur {
		t.Fatal("manual backup content")
	}
}

func TestValidation(t *testing.T) {
	long := strings.Repeat("a.", 126) + "ab" // 254 chars
	label64 := strings.Repeat("a", 64)
	many := make([]string, 33)
	for i := range many {
		many[i] = "h" + strings.Repeat("x", i) + ".local"
	}
	cases := []struct {
		name  string
		ip    string
		hosts []string
		group string
		force bool
		ok    bool
	}{
		{"ok v4", "10.0.0.1", []string{"a.local"}, "", false, true},
		{"ok v6", "::1", []string{"a.local"}, "g1", false, true},
		{"newline in host", "10.0.0.1", []string{"a.local\n10.6.6.6 evil"}, "", false, false},
		{"CR in host", "10.0.0.1", []string{"a.local\r"}, "", false, false},
		{"hash in host", "10.0.0.1", []string{"a#b"}, "", false, false},
		{"space in host", "10.0.0.1", []string{"a b"}, "", false, false},
		{"empty host", "10.0.0.1", []string{""}, "", false, false},
		{"no hosts", "10.0.0.1", nil, "", false, false},
		{"leading hyphen", "10.0.0.1", []string{"-a.local"}, "", false, false},
		{"trailing dot", "10.0.0.1", []string{"a.local."}, "", false, false},
		{"underscore", "10.0.0.1", []string{"a_b.local"}, "", false, false},
		{"label too long", "10.0.0.1", []string{label64 + ".local"}, "", false, false},
		{"name too long", "10.0.0.1", []string{long}, "", false, false},
		{"too many names", "10.0.0.1", many, "", false, false},
		{"bad ip", "10.0.0.256", []string{"a.local"}, "", false, false},
		{"ip with newline", "10.0.0.1\n", []string{"a.local"}, "", false, false},
		{"ip leading zero", "010.0.0.1", []string{"a.local"}, "", false, false},
		{"hostname as ip", "example.com", []string{"a.local"}, "", false, false},
		{"zone id", "fe80::1%eth0", []string{"a.local"}, "", false, false},
		{"bad group", "10.0.0.1", []string{"a.local"}, "a b", false, false},
		{"group newline", "10.0.0.1", []string{"a.local"}, "g\n10.6.6.6 x", false, false},
		{"localhost", "10.0.0.1", []string{"localhost"}, "", false, false},
		{"LOCALHOST", "10.0.0.1", []string{"LocalHost"}, "", false, false},
		{"broadcasthost", "10.0.0.1", []string{"broadcasthost"}, "", false, false},
		{"ip6-localhost", "10.0.0.1", []string{"ip6-localhost"}, "", false, false},
		{"localhost forced", "10.0.0.1", []string{"localhost"}, "", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, _ := Parse([]byte(lfFixture))
			_, err := d.Add(c.ip, c.hosts, c.group, c.force)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok=%v", err, c.ok)
			}
			if err != nil {
				return
			}
			out := string(d.Render())
			if strings.Count(out, "\n") != strings.Count(lfFixture, "\n")+5 {
				t.Fatalf("unexpected line count:\n%s", out)
			}
		})
	}
	if _, err := ParseIP("fe80::1%eth0"); err == nil || !strings.Contains(err.Error(), "zone") {
		t.Fatalf("zone error %v", err)
	}
}

func TestParseRejectsMalformedBlocks(t *testing.T) {
	for name, content := range map[string]string{
		"begin without end":          "a\n" + BeginMarker + "\n10.0.0.1 a.local\n",
		"end without begin":          "a\n" + EndMarker + "\n",
		"two blocks":                 BeginMarker + "\n" + EndMarker + "\n" + BeginMarker + "\n" + EndMarker + "\n",
		"foreign line":               BeginMarker + "\n# group: dev\nhello world\n" + EndMarker + "\n",
		"foreign comment":            BeginMarker + "\n# group: dev\n# note\n" + EndMarker + "\n",
		"commented in enabled group": BeginMarker + "\n# group: dev\n# 10.0.0.1 a.local\n" + EndMarker + "\n",
		"bad host in block":          BeginMarker + "\n# group: dev\n10.0.0.1 a_b\n" + EndMarker + "\n",
		"duplicate group":            BeginMarker + "\n# group: dev\n# group: dev\n" + EndMarker + "\n",
		"utf16":                      "\xFF\xFEa\x00",
		"nul":                        "a\x00b\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := writeTemp(t, content)
			if _, err := f.Update(false, add("10.0.0.1", []string{"x.local"}, "")); err == nil {
				t.Fatal("accepted")
			}
			if read(t, f) != content {
				t.Fatal("file changed")
			}
		})
	}
	// A hand-written block in the documented format is accepted.
	d, err := Parse([]byte("x\n" + BeginMarker + "\n10.0.0.9 pre.local\n# group: dev (disabled)\n#   10.0.0.1   a.local  b.local\n" + EndMarker + "\n"))
	if err != nil || len(d.Groups) != 2 || d.Groups[0].Name != DefaultGroup || d.Groups[1].Enabled || len(d.Groups[1].Entries[0].Hostnames) != 2 {
		t.Fatalf("%+v %v", d, err)
	}
	if u := d.Unmanaged(); len(u) != 1 || u[0].Text != "x" || u[0].N != 1 {
		t.Fatalf("unmanaged %+v", u)
	}
}

func TestLock(t *testing.T) {
	f := writeTemp(t, lfFixture)
	lp := f.Path + lockSuffix
	if err := os.WriteFile(lp, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Update(false, add("10.0.0.1", []string{"a.local"}, "")); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked: %v", err)
	}
	if read(t, f) != lfFixture {
		t.Fatal("wrote while locked")
	}
	// Dry runs and reads never need the lock.
	if _, err := f.Update(true, add("10.0.0.1", []string{"a.local"}, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(); err != nil {
		t.Fatal(err)
	}
	// A stale lock (holder died) is taken over.
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(lp, old, old)
	mustUpdate(t, f, add("10.0.0.1", []string{"a.local"}, ""))
	if _, err := os.Stat(lp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestPermissionDenied(t *testing.T) {
	f := writeTemp(t, lfFixture)
	dir := filepath.Dir(f.Path)
	if err := os.Chmod(f.Path, 0o444); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(f.Path, 0o644)
	})
	_, err := f.Update(false, add("10.0.0.1", []string{"a.local"}, ""))
	if err == nil {
		t.Skip("this OS/user ignores read-only permissions (running as root?)")
	}
	var pe *PermissionError
	if !errors.As(err, &pe) {
		t.Fatalf("not a permission error: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "elevated shell") || !strings.Contains(msg, "sudo") || !strings.Contains(msg, "never elevates") {
		t.Fatalf("message: %s", msg)
	}
	_ = os.Chmod(f.Path, 0o644)
	if read(t, f) != lfFixture {
		t.Fatal("file changed")
	}
}

func TestDefaultPath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := DefaultPath("windows", env(map[string]string{"SystemRoot": `D:\Win\`})); got != `D:\Win\System32\drivers\etc\hosts` {
		t.Fatal(got)
	}
	if got := DefaultPath("windows", env(nil)); got != `C:\Windows\System32\drivers\etc\hosts` {
		t.Fatal(got)
	}
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if got := DefaultPath(goos, env(nil)); got != "/etc/hosts" {
			t.Fatal(got)
		}
	}
	t.Setenv(EnvFile, "/tmp/x-hosts")
	if p, src := ResolvePath(""); p != "/tmp/x-hosts" || src != EnvFile {
		t.Fatal(p, src)
	}
	if p, src := ResolvePath("flag-hosts"); p != "flag-hosts" || src != "--file" {
		t.Fatal(p, src)
	}
}

func TestMissingFile(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "absent"))
	if _, err := f.Update(false, add("10.0.0.1", []string{"a.local"}, "")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(f.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing file was created")
	}
}

func TestDiffLargeFileSmallHunk(t *testing.T) {
	var b bytes.Buffer
	for i := 0; i < 50000; i++ {
		b.WriteString("0.0.0.0 ads")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(".example\n")
	}
	d, err := Parse(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add("10.0.0.1", []string{"a.local"}, "", false); err != nil {
		t.Fatal(err)
	}
	diff := Diff("hosts", b.Bytes(), d.Render())
	if strings.Count(diff, "\n") > 20 || !strings.Contains(diff, "+10.0.0.1 a.local") {
		t.Fatalf("diff:\n%s", diff)
	}
}
