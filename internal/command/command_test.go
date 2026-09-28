package command_test

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

func tree() (*command.Command, *string) {
	captured := new(string)
	flagCount := 0

	leaf := &command.Command{
		Name:    "run",
		Summary: "run a thing",
		Flags: func(fs *flag.FlagSet) {
			fs.String("target", "", "where to run")
			fs.Bool("dry-run", false, "change nothing")
			flagCount++
		},
		Run: func(ctx context.Context, env *command.Env, args []string) error {
			*captured = strings.Join(args, ",")
			_ = ctx
			return nil
		},
	}
	refuse := &command.Command{
		Name:    "refuse",
		Summary: "always declines",
		Run: func(context.Context, *command.Env, []string) error {
			return command.Refuse("not in this build")
		},
	}
	broken := &command.Command{
		Name:    "broken",
		Summary: "always fails",
		Run: func(context.Context, *command.Env, []string) error {
			return errors.New("something broke")
		},
	}
	bad := &command.Command{
		Name:    "bad-args",
		Summary: "rejects its arguments",
		Run: func(context.Context, *command.Env, []string) error {
			return &command.UsageError{Err: errors.New("too many arguments")}
		},
	}
	group := &command.Command{Name: "group", Summary: "a group of things"}
	group.Register(leaf, refuse, broken, bad)

	root := &command.Command{Name: "tool", Summary: "a tool"}
	root.Register(group)
	return root, captured
}

func env() (*command.Env, *strings.Builder, *strings.Builder) {
	var out, errOut strings.Builder
	return &command.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, &out, &errOut
}

func TestDispatchNestedSubcommand(t *testing.T) {
	root, captured := tree()
	e, out, _ := env()

	if code := root.Execute(context.Background(), e, []string{"group", "run", "--target", "x", "arg1", "arg2"}); code != command.ExitOK {
		t.Fatalf("exit code %d, want %d", code, command.ExitOK)
	}
	if *captured != "arg1,arg2" {
		t.Errorf("positional args were %q, want \"arg1,arg2\"", *captured)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("a successful run wrote to stdout: %q", out.String())
	}
}

// Exit codes are a contract: a service manager or a script branches on them. A
// refusal and a failure must be distinguishable, or an installer retries the
// wrong thing.
func TestExitCodesDistinguishRefusalFromFailure(t *testing.T) {
	cases := []struct {
		args []string
		want command.ExitCode
	}{
		{[]string{"group", "run"}, command.ExitOK},
		{[]string{"group", "refuse"}, command.ExitRefused},
		{[]string{"group", "broken"}, command.ExitError},
		{[]string{"group", "bad-args"}, command.ExitUsage},
		{[]string{"group", "nope"}, command.ExitUsage},
		{[]string{"nope"}, command.ExitUsage},
		{[]string{"--target", "x"}, command.ExitUsage},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			root, _ := tree()
			e, _, errOut := env()
			if code := root.Execute(context.Background(), e, tc.args); code != tc.want {
				t.Errorf("exit code %d, want %d (stderr: %s)", code, tc.want, errOut.String())
			}
		})
	}
}

// A flag before the subcommand is almost always a mistake, and silently treating
// it as a positional would produce a confusing failure several commands later.
// A grouping command takes no flags at all, so `group --target x run` is an error
// rather than a help dump with a zero exit code.
func TestFlagsMustFollowTheSubcommand(t *testing.T) {
	root, _ := tree()
	e, _, errOut := env()
	if code := root.Execute(context.Background(), e, []string{"group", "--target", "x", "run"}); code != command.ExitUsage {
		t.Fatalf("exit code %d, want %d", code, command.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "takes no flag") {
		t.Errorf("usage error should say so, got %q", errOut.String())
	}
}

func TestGroupWithoutRunPrintsHelp(t *testing.T) {
	root, _ := tree()
	e, out, _ := env()
	if code := root.Execute(context.Background(), e, []string{"group"}); code != command.ExitOK {
		t.Fatalf("exit code %d, want %d", code, command.ExitOK)
	}
	text := out.String()
	for _, want := range []string{"Subcommands:", "run", "refuse", "broken"} {
		if !strings.Contains(text, want) {
			t.Errorf("help output is missing %q:\n%s", want, text)
		}
	}
}

func TestRootWithoutArgsPrintsHelp(t *testing.T) {
	root, _ := tree()
	e, out, _ := env()
	if code := root.Execute(context.Background(), e, nil); code != command.ExitOK {
		t.Fatalf("exit code %d, want %d", code, command.ExitOK)
	}
	if !strings.Contains(out.String(), "group") {
		t.Errorf("root help is missing its subcommands:\n%s", out.String())
	}
}

// An unknown subcommand must be a usage error, not a silent dispatch to the
// parent. An operator who typed `securityctl rules lintt` must be told, not
// shown a successful no-op.
func TestUnknownSubcommandIsAUsageError(t *testing.T) {
	root, captured := tree()
	e, _, errOut := env()
	if code := root.Execute(context.Background(), e, []string{"group", "runn"}); code != command.ExitUsage {
		t.Fatalf("exit code %d, want %d", code, command.ExitUsage)
	}
	if *captured != "" {
		t.Error("an unknown subcommand still dispatched to a handler")
	}
	if !strings.Contains(errOut.String(), "unknown subcommand") {
		t.Errorf("stderr should name the problem, got %q", errOut.String())
	}
}

func TestFindRejectsUnknownPath(t *testing.T) {
	root, _ := tree()
	if _, err := root.Find([]string{"group", "nope"}); err == nil {
		t.Fatal("Find accepted an unknown subcommand")
	}
	found, err := root.Find([]string{"group", "run"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found.Path() != "tool group run" {
		t.Errorf("Path is %q, want \"tool group run\"", found.Path())
	}
}
