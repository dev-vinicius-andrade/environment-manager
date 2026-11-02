package types

import (
	"github.com/dev-vinicius-andrade/environment-manager/types/interfaces"
)

type CommandDefinition struct {
	interfaces.CommandDefinition
}

type CobraHelpOptions struct {
	Title string
}
