package use

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
	dir, err := os.MkdirTemp("", "envmgr-use-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(hosts.EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const hostsOriginal = "127.0.0.1 localhost\r\n# keep me\r\n"

func setupHosts(t *testing.T) (*profile.Store, string) {
	t.Helper()
	st := setup(t)
	hf := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hf, []byte(hostsOriginal), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hosts.EnvFile, hf)
	if err := st.SetVar("dev", "MY_URL", "http://dev"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHost("dev", "10.0.0.5", "api.dev.local"); err != nil {
		t.Fatal(err)
	}
	return st, hf
}

func fileContent(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUseScopeMatrix(t *testing.T) {
	envCode := func(t *testing.T) string {
		out, _, err := execute("use", "dev", "--shell", "bash")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("default is env only", func(t *testing.T) {
		_, hf := setupHosts(t)
		out := envCode(t)
		if !strings.Contains(out, "export MY_URL='http://dev'") || fileContent(t, hf) != hostsOriginal {
			t.Fatalf("out=%q file=%q", out, fileContent(t, hf))
		}
	})

	t.Run("--hosts does both", func(t *testing.T) {
		st, hf := setupHosts(t)
		want := envCode(t)
		out, errOut, err := execute("use", "dev", "--shell", "bash", "--hosts")
		if err != nil || out != want {
			t.Fatalf("err=%v out=%q want %q", err, out, want)
		}
		got := fileContent(t, hf)
		if !strings.HasPrefix(got, hostsOriginal) || !strings.Contains(got, "# group: profile:dev\r\n10.0.0.5 api.dev.local\r\n") {
			t.Fatalf("file %q", got)
		}
		if !strings.Contains(errOut, "updated") || !strings.Contains(errOut, "activated") {
			t.Fatalf("stderr %q", errOut)
		}
		if s, _ := hostsapply.ReadState(st.Dir); s.Profile != "dev" {
			t.Fatalf("state %+v", s)
		}
	})

	t.Run("--only all does both", func(t *testing.T) {
		_, hf := setupHosts(t)
		out, _, err := execute("use", "dev", "--shell", "bash", "--only", "all")
		if err != nil || out == "" || !strings.Contains(fileContent(t, hf), "profile:dev") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("--only hosts prints no code", func(t *testing.T) {
		_, hf := setupHosts(t)
		out, errOut, err := execute("use", "dev", "--shell", "bash", "--only", "hosts")
		if err != nil || out != "" || !strings.Contains(fileContent(t, hf), "profile:dev") || strings.Contains(errOut, "activated profile") {
			t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
		}
	})

	t.Run("--only env leaves hosts alone", func(t *testing.T) {
		_, hf := setupHosts(t)
		out, _, err := execute("use", "dev", "--shell", "bash", "--only", "env")
		if err != nil || out == "" || fileContent(t, hf) != hostsOriginal {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("failing hosts write prints nothing and fails", func(t *testing.T) {
		setupHosts(t)
		t.Setenv(hosts.EnvFile, filepath.Join(t.TempDir(), "missing-hosts"))
		out, _, err := execute("use", "dev", "--shell", "bash", "--hosts")
		if err == nil || out != "" || !strings.Contains(err.Error(), "--best-effort") {
			t.Fatalf("err=%v out=%q", err, out)
		}
		out, _, err = execute("use", "dev", "--shell", "bash", "--only", "hosts")
		if err == nil || out != "" {
			t.Fatalf("only hosts: err=%v out=%q", err, out)
		}
	})

	t.Run("--best-effort prints the env code", func(t *testing.T) {
		setupHosts(t)
		want := envCode(t)
		t.Setenv(hosts.EnvFile, filepath.Join(t.TempDir(), "missing-hosts"))
		out, errOut, err := execute("use", "dev", "--shell", "bash", "--hosts", "--best-effort")
		if err != nil || out != want || !strings.Contains(errOut, "NOT applied") {
			t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
		}
	})

	t.Run("render error writes no hosts", func(t *testing.T) {
		st, hf := setupHosts(t)
		if err := st.SetVar("dev", "MY_URL", "a\nb"); err != nil {
			t.Fatal(err)
		}
		out, _, err := execute("use", "dev", "--shell", "cmd", "--hosts")
		if err == nil || out != "" || fileContent(t, hf) != hostsOriginal {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("--dry-run writes nothing and prints no code", func(t *testing.T) {
		_, hf := setupHosts(t)
		out, errOut, err := execute("use", "dev", "--shell", "bash", "--hosts", "--dry-run")
		if err != nil || out != "" || fileContent(t, hf) != hostsOriginal || !strings.Contains(errOut, "+10.0.0.5 api.dev.local") {
			t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
		}
		if _, _, err := execute("use", "dev", "--dry-run"); err == nil {
			t.Fatal("--dry-run without hosts scope accepted")
		}
	})

	t.Run("--file and flag conflicts", func(t *testing.T) {
		_, hf := setupHosts(t)
		other := filepath.Join(t.TempDir(), "other")
		if err := os.WriteFile(other, []byte("# other\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := execute("use", "dev", "--only", "hosts", "--file", other); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fileContent(t, other), "profile:dev") || fileContent(t, hf) != hostsOriginal {
			t.Fatal("--file ignored")
		}
		for _, args := range [][]string{{"--hosts", "--only", "env"}, {"--only", "both"}} {
			if out, _, err := execute(append([]string{"use", "dev", "--shell", "bash"}, args...)...); err == nil || out != "" {
				t.Fatalf("%v accepted", args)
			}
		}
	})

	t.Run("profile with hosts only", func(t *testing.T) {
		st, hf := setupHosts(t)
		if err := st.SetHost("net", "10.0.0.9", "net.local"); err != nil {
			t.Fatal(err)
		}
		out, _, err := execute("use", "net", "--shell", "bash", "--hosts")
		if err != nil || !strings.Contains(out, "ENVMGR_PROFILE='net'") || !strings.Contains(fileContent(t, hf), "10.0.0.9 net.local") {
			t.Fatalf("err=%v out=%q", err, out)
		}
		// Applying another profile replaces it.
		if _, _, err := execute("use", "dev", "--only", "hosts"); err != nil {
			t.Fatal(err)
		}
		if got := fileContent(t, hf); strings.Contains(got, "net.local") || !strings.Contains(got, "api.dev.local") || !strings.HasPrefix(got, hostsOriginal) {
			t.Fatalf("replace: %q", got)
		}
	})
}
