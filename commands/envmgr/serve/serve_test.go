package serve

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7777": true,
		"[::1]:7777":     true,
		"localhost:7777": true,
		":7777":          false,
		"0.0.0.0:7777":   false,
		"[::]:7777":      false,
		"10.0.0.5:7777":  false,
		"example.com:1":  false,
		"garbage":        false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestShowTokenStable(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	show := func() string {
		root := &cobra.Command{Use: "envmgr"}
		CreateCommand(&context.EnvContext{}, root)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"serve", "--show-token"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	a, b := show(), show()
	if a != b || !strings.Contains(a, "token: ") || !strings.Contains(a, "fingerprint: ") {
		t.Fatalf("show-token output %q then %q", a, b)
	}
}
