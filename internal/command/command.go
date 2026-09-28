// Package command is a small subcommand tree for the CLIs.
//
// Deliberately not a dependency. These binaries ship to air-gapped sites where
// every vendored module is a supply-chain decision somebody has to sign off on,
// and the whole requirement here is: nested subcommands, per-command flags,
// usage text, and a typed exit code. A dependency that supplies that and nothing
// else is not worth a permanent audit burden.
package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Command is one node of the tree. A command with no Run is a pure grouping
// node and prints its own help.
type Command struct {
	Name     string
	Summary  string
	Usage    string
	Flags    func(fs *flag.FlagSet)
	Run      func(ctx context.Context, env *Env, args []string) error
	Children []*Command

	parent *Command
}

// Env is what a command is given to work with. Streams are injected so that
// output can be captured in a test and so that nothing writes to the real
// process streams by accident.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExitCode is the process exit status. The values are stable: scripts and
// service managers branch on them, so adding a new one is a contract change.
type ExitCode int

const (
	ExitOK      ExitCode = 0
	ExitError   ExitCode = 1
	ExitUsage   ExitCode = 2
	ExitRefused ExitCode = 3
)

// RefusedError marks a command that ran correctly and declined: a policy gate
// refusing an action, as opposed to a command that broke.
//
// The distinction matters operationally. "preflight failed" and "the database is
// unreachable" are different incidents, and an installer that cannot tell them
// apart retries the wrong thing.
type RefusedError struct {
	Reason string
}

func (e *RefusedError) Error() string { return e.Reason }

// Refuse builds a RefusedError.
func Refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// UsageError marks a malformed invocation.
type UsageError struct {
	Cmd *Command
	Err error
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// Register adds a child command.
func (c *Command) Register(children ...*Command) *Command {
	for _, child := range children {
		child.parent = c
		c.Children = append(c.Children, child)
	}
	return c
}

// Path is the dotted command path, e.g. "rules lint".
func (c *Command) Path() string {
	if c.parent == nil {
		return c.Name
	}
	return c.parent.Path() + " " + c.Name
}

// Find resolves a command path such as []string{"rules", "lint"}.
func (c *Command) Find(path []string) (*Command, error) {
	cur := c
	for i, name := range path {
		var next *Command
		for _, child := range cur.Children {
			if child.Name == name {
				next = child
				break
			}
		}
		if next == nil {
			return nil, &UsageError{
				Cmd: cur,
				Err: fmt.Errorf("unknown subcommand %q for %q", name, cur.Path()),
			}
		}
		cur = next
		_ = i
	}
	return cur, nil
}

// Execute parses args, dispatches, and returns a process exit code.
func (c *Command) Execute(ctx context.Context, env *Env, args []string) ExitCode {
	path, rest, err := c.split(args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s: %v\n\n", c.Name, err)
		c.writeHelp(env.Stderr)
		return ExitUsage
	}

	target := c
	if len(path) > 0 {
		target, err = c.Find(path)
		if err != nil {
			fmt.Fprintf(env.Stderr, "%s: %v\n\n", c.Name, err)
			c.writeHelp(env.Stderr)
			return ExitUsage
		}
	}

	// A leftover bare token where the resolved command is a group means a
	// misspelled subcommand. Without this check `securityctl rules lintt`
	// prints the rules help and exits zero, and an operator concludes their
	// bundle linted clean.
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && len(target.Children) > 0 {
		fmt.Fprintf(env.Stderr, "%s: unknown subcommand %q\n\n", target.Path(), rest[0])
		target.writeHelp(env.Stderr)
		return ExitUsage
	}

	// A grouping command takes no flags and no positionals. Anything after it is
	// either a misspelled subcommand or a flag the engineer expected it to
	// accept, and both must be reported rather than answered with help and a
	// zero exit code.
	if target.Run == nil {
		if len(rest) > 0 {
			word := rest[0]
			kind := "argument"
			if strings.HasPrefix(word, "-") {
				kind = "flag"
			}
			fmt.Fprintf(env.Stderr, "%s: %s takes no %s; %q is not a subcommand of it\n\n",
				target.Path(), target.Path(), kind, word)
			target.writeHelp(env.Stderr)
			return ExitUsage
		}
		target.writeHelp(env.Stdout)
		return ExitOK
	}

	fs := flag.NewFlagSet(target.Path(), flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	if target.Flags != nil {
		target.Flags(fs)
	}
	fs.Usage = func() { target.writeHelp(env.Stderr) }
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}

	if err := target.Run(ctx, env, fs.Args()); err != nil {
		return report(env, target, err)
	}
	return ExitOK
}

func report(env *Env, c *Command, err error) ExitCode {
	var refused *RefusedError
	if errors.As(err, &refused) {
		fmt.Fprintf(env.Stderr, "%s: refused: %s\n", c.Path(), refused.Reason)
		return ExitRefused
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		fmt.Fprintf(env.Stderr, "%s: %v\n\n", c.Path(), usage.Err)
		c.writeHelp(env.Stderr)
		return ExitUsage
	}
	fmt.Fprintf(env.Stderr, "%s: %v\n", c.Path(), err)
	return ExitError
}

// split walks the argument list, consuming leading non-flag tokens that match
// registered subcommands and stopping at the first token that does not.
//
// A flag before the first subcommand is a usage error rather than a guess. An
// engineer who typed `securityctl --json rules lint` meant something specific,
// and silently treating `--json` as a positional would produce a confusing
// failure three commands later.
func (c *Command) split(args []string) (path []string, rest []string, err error) {
	cur := c
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if len(path) == 0 {
				return nil, nil, fmt.Errorf("flags must follow the subcommand, got %q first", a)
			}
			break
		}
		var next *Command
		for _, child := range cur.Children {
			if child.Name == a {
				next = child
				break
			}
		}
		if next == nil {
			break
		}
		path = append(path, a)
		cur = next
	}
	return path, args[i:], nil
}

func (c *Command) writeHelp(w io.Writer) {
	if c.Usage != "" {
		fmt.Fprintf(w, "%s\n\n", strings.TrimSpace(c.Usage))
	} else if c.Summary != "" {
		fmt.Fprintf(w, "%s\n\n", strings.TrimSpace(c.Summary))
	}
	if len(c.Children) == 0 {
		return
	}
	fmt.Fprintf(w, "Subcommands:\n")
	names := make([]*Command, len(c.Children))
	copy(names, c.Children)
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	width := 0
	for _, child := range names {
		if len(child.Name) > width {
			width = len(child.Name)
		}
	}
	for _, child := range names {
		fmt.Fprintf(w, "  %-*s  %s\n", width, child.Name, child.Summary)
	}
}
