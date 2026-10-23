// Command cli runs Helpic's one-off operator jobs against the same database
// and configuration as the server. Each command is a file in ./commands.
//
// Usage:
//
//	cli <command> [flags]
//	cli help              list the commands
//
// In Docker: docker compose exec backend /app/cli <command>.
package main

import (
	"context"
	"fmt"
	"os"
	_ "time/tzdata" // APP_TIMEZONE works on images without zoneinfo

	"github.com/pmaxis/helpic/cmd/cli/commands"
	"github.com/pmaxis/helpic/internal/config"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		return
	}
	cmd, ok := commands.Find(os.Args[1])
	if !ok {
		fmt.Fprintf(os.Stderr, "cli: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cli: configuration:", err)
		os.Exit(1)
	}
	if err := cmd.Run(context.Background(), cfg, os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "cli %s: %v\n", cmd.Name, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: cli <command> [flags]\n\nCommands:")
	for _, c := range commands.All {
		fmt.Fprintf(os.Stderr, "  %-14s %s\n", c.Name, c.Summary)
	}
	fmt.Fprintln(os.Stderr, "\nRun cli <command> -h for its flags.")
}
