package main

import (
	"fmt"
	"os"

	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr"
)

func main() {
	//dotfilesInformation := &context.EnvContext{}
	command := envmgr.CreateCommand()
	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
