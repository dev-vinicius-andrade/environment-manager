package profile

import (
	"bytes"
	"strings"
	"testing"

	store "github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func execute(args ...string) (string, string, error) {
	root := &cobra.Command{Use: "envmgr"}
	CreateCommand(&context.EnvContext{}, root)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestShowAndDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	st := &store.Store{Dir: dir}
	_ = st.SetVar("dev", "TOKEN", "secret-value")
	_ = st.SetVar("dev", "API", "http://dev")

	out, _, err := execute("profile", "show", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if out != "API=********\nTOKEN=********\n" || strings.Contains(out, "secret") {
		t.Fatalf("masked show = %q", out)
	}
	out, _, err = execute("profile", "show", "dev", "--reveal")
	if err != nil || out != "API=http://dev\nTOKEN=secret-value\n" {
		t.Fatalf("reveal: out=%q err=%v", out, err)
	}
	if out, _, err := execute("profile", "show", "nope"); err == nil || out != "" {
		t.Fatalf("show unknown: out=%q err=%v", out, err)
	}

	if _, _, err := execute("profile", "delete", "dev"); err != nil {
		t.Fatal(err)
	}
	if names, _ := st.List(); len(names) != 0 {
		t.Fatalf("profiles left: %v", names)
	}
	if _, _, err := execute("profile", "delete", "dev"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("delete unknown: %v", err)
	}
}

func TestProfileHostsAndShowSections(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	st := &store.Store{Dir: dir}
	_ = st.SetVar("dev", "TOKEN", "secret-value")

	if _, _, err := execute("profile", "hosts", "add", "dev", "10.0.0.5", "API.dev.local", "db.dev.local", "--comment", "dev cluster"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute("profile", "hosts", "add", "dev", "10.0.0.6", "web.dev.local"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{
		{"10.0.0.7", "localhost"},
		{"10.0.0.7", "a#b"},
		{"10.0.0.256", "a.local"},
		{"10.0.0.7", "a.local", "--comment", "x # y"},
	} {
		if _, _, err := execute(append([]string{"profile", "hosts", "add", "dev"}, bad...)...); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	out, _, err := execute("profile", "show", "dev")
	want := "# variables\nTOKEN=********\n# hosts\n10.0.0.5 api.dev.local db.dev.local  # dev cluster\n10.0.0.6 web.dev.local\n"
	if err != nil || out != want {
		t.Fatalf("show:\n%q\nwant\n%q", out, want)
	}
	if out, _, _ := execute("profile", "show", "dev", "--section", "vars", "--reveal"); out != "TOKEN=secret-value\n" {
		t.Fatalf("vars section %q", out)
	}
	if out, _, _ := execute("profile", "show", "dev", "--section", "hosts"); out != "10.0.0.5 api.dev.local db.dev.local  # dev cluster\n10.0.0.6 web.dev.local\n" {
		t.Fatalf("hosts section %q", out)
	}
	if _, _, err := execute("profile", "show", "dev", "--section", "nope"); err == nil {
		t.Fatal("bad section accepted")
	}

	if _, _, err := execute("profile", "hosts", "remove", "dev", "db.dev.local"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute("profile", "hosts", "remove", "dev", "10.0.0.6"); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := execute("profile", "hosts", "remove", "dev", "nope.local"); err != nil || !strings.Contains(errOut, "not in the profile") {
		t.Fatalf("remove missing: %v %q", err, errOut)
	}
	p, _ := st.Get("dev")
	if len(p.Hosts) != 1 || len(p.Hosts[0].Names) != 1 || p.Vars["TOKEN"] != "secret-value" {
		t.Fatalf("%+v", p)
	}
	// A hosts-only profile is created by add.
	if _, _, err := execute("profile", "hosts", "add", "net", "10.1.0.1", "n.local"); err != nil {
		t.Fatal(err)
	}
	if out, errOut, _ := execute("profile", "show", "net"); out != "# hosts\n10.1.0.1 n.local\n" || !strings.Contains(errOut, "no variables") {
		t.Fatalf("hosts only: %q %q", out, errOut)
	}
}
