package deactivate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// Never touch the real hosts file from these tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "envmgr-deactivate-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(hosts.EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const original = "127.0.0.1 localhost\n# keep me\n"

// applied prepares a home with profile dev applied to a temp hosts file and
// a shell where dev is active.
func applied(t *testing.T) (*profile.Store, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ENVMGR_HOME", home)
	st := &profile.Store{Dir: home}
	if err := st.SetHost("dev", "10.0.0.5", "api.dev.local"); err != nil {
		t.Fatal(err)
	}
	hf := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hf, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hosts.EnvFile, hf)
	if _, err := hostsapply.Apply(st, hf, "dev", false); err != nil {
		t.Fatal(err)
	}
	clearEnv(t, "ENVMGR_PREV_A", "ENVMGR_PINNED")
	t.Setenv("ENVMGR_PROFILE", "dev")
	t.Setenv("ENVMGR_VARS", "A")
	t.Setenv("A", "dev")
	return st, hf
}

func content(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDeactivateScopeMatrix(t *testing.T) {
	t.Run("default is env only", func(t *testing.T) {
		_, hf := applied(t)
		out, _, err := execute("deactivate", "--shell", "bash")
		if err != nil || !strings.Contains(out, "unset A") || !strings.Contains(content(t, hf), "profile:dev") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})
	t.Run("--hosts does both", func(t *testing.T) {
		st, hf := applied(t)
		out, errOut, err := execute("deactivate", "--shell", "bash", "--hosts")
		if err != nil || !strings.Contains(out, "unset A") || content(t, hf) != original+hosts.BeginMarker+"\n# Managed by envmgr (envmgr hosts). Lines between BEGIN and END are rewritten by envmgr.\n"+hosts.EndMarker+"\n" {
			t.Fatalf("err=%v out=%q file=%q", err, out, content(t, hf))
		}
		if !strings.Contains(errOut, `hosts of profile "dev"`) {
			t.Fatalf("stderr %q", errOut)
		}
		if s, _ := hostsapply.ReadState(st.Dir); s.Profile != "" {
			t.Fatalf("state %+v", s)
		}
	})
	t.Run("--only hosts prints no code", func(t *testing.T) {
		_, hf := applied(t)
		out, _, err := execute("deactivate", "--shell", "bash", "--only", "hosts")
		if err != nil || out != "" || strings.Contains(content(t, hf), "profile:dev") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})
	t.Run("--hosts with nothing active still removes hosts", func(t *testing.T) {
		_, hf := applied(t)
		clearEnv(t, "ENVMGR_PROFILE", "ENVMGR_VARS")
		out, errOut, err := execute("deactivate", "--shell", "bash", "--hosts")
		if err != nil || out != "" || strings.Contains(content(t, hf), "profile:dev") || !strings.Contains(errOut, "no active profile") {
			t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
		}
	})
	t.Run("failing hosts write prints nothing", func(t *testing.T) {
		applied(t)
		t.Setenv(hosts.EnvFile, filepath.Join(t.TempDir(), "missing"))
		out, _, err := execute("deactivate", "--shell", "bash", "--hosts")
		if err == nil || out != "" {
			t.Fatalf("err=%v out=%q", err, out)
		}
		out, errOut, err := execute("deactivate", "--shell", "bash", "--hosts", "--best-effort")
		if err != nil || !strings.Contains(out, "unset A") || !strings.Contains(errOut, "NOT removed") {
			t.Fatalf("best effort: err=%v out=%q stderr=%q", err, out, errOut)
		}
	})
	t.Run("--dry-run", func(t *testing.T) {
		_, hf := applied(t)
		before := content(t, hf)
		out, errOut, err := execute("deactivate", "--shell", "bash", "--only", "hosts", "--dry-run")
		if err != nil || out != "" || content(t, hf) != before || !strings.Contains(errOut, "-10.0.0.5 api.dev.local") {
			t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
		}
	})
}
