// Package commands holds the cli's commands, one per file. A new command is
// a Command value added to All.
package commands

import (
	"context"

	"github.com/pmaxis/helpic/internal/config"
)

// Command is one job the cli runs.
type Command struct {
	// Name is what's typed after cli, e.g. "send-digest".
	Name string
	// Summary is its line in cli help.
	Summary string
	// Run does the job with the arguments after the name. An error ends the
	// cli with status 1.
	Run func(ctx context.Context, cfg config.Config, args []string) error
}

// All are the commands, in the order cli help lists them.
var All = []Command{}

// Find returns the command with the name.
func Find(name string) (Command, bool) {
	for _, c := range All {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}
