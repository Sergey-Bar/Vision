// Command securityctl is the operator and installer CLI.
//
// One binary, one command tree, two audiences: an engineer running rules lint
// during development, and an engineer running upgrade at a site with no
// network. They share a binary because they share a trust model — anything this
// tool can do, the tool can be made to do by feeding it a hostile file.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := newRootCommand()
	env := &command.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(int(root.Execute(ctx, env, os.Args[1:])))
}

func newRootCommand() *command.Command {
	root := &command.Command{
		Name:    "securityctl",
		Summary: "Physical Security Intelligence Layer — operator and installer CLI",
		Usage: `securityctl — Physical Security Intelligence Layer.

Usage:
  securityctl <command> [subcommand] [flags]

Every state-changing command is audited. Nothing here takes a tenant id from
standard input or from a flag: the tenant comes from the authenticated session
or from the site configuration.`,
	}
	root.Register(
		preflightCommand(),
		doctorCommand(),
		installCommand(),
		upgradeCommand(),
		rollbackCommand(),
		backupCommand(),
		restoreCommand(),
		verifyReleaseCommand(),
		migrateCommand(),
		rulesCommand(),
		incidentCommand(),
		auditCommand(),
		identityCommand(),
	)
	return root
}

func notImplemented(what string) func(context.Context, *command.Env, []string) error {
	return func(context.Context, *command.Env, []string) error {
		return command.Refuse("%s is not implemented in this build", what)
	}
}

// printUsageError marks a malformed invocation, which the command tree renders
// with the command's own usage text and a distinct exit code.
func printUsageError(format string, args ...any) error {
	return &command.UsageError{Err: fmt.Errorf(format, args...)}
}
