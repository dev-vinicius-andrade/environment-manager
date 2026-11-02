// Command envmgrtest is the envmgr binary used by the hook round-trip tests:
// the real root command plus the selection commands (added here only when the
// root does not register them yet). Also used to try the tray by hand.
package main

import (
	"fmt"
	"os"

	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/follow"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/hook"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/selectcmd"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/selected"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/synccmd"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/tray"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func main() {
	root := envmgr.CreateCommand()
	ctx := &context.EnvContext{}
	has := func(name string) bool {
		for _, c := range root.Commands() {
			if c.Name() == name {
				return true
			}
		}
		return false
	}
	for name, create := range map[string]func(*context.EnvContext, *cobra.Command) *cobra.Command{
		"select":   selectcmd.CreateCommand,
		"selected": selected.CreateCommand,
		"sync":     synccmd.CreateCommand,
		"follow":   follow.CreateCommand,
		"hook":     hook.CreateCommand,
		"tray":     tray.CreateCommand,
	} {
		if !has(name) {
			create(ctx, root)
		}
	}
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
