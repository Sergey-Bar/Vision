// Command poc is a second name for pocctl, so both spellings in the plan work:
// `poc cap` and `pocctl cap` are the same program.
//
// It forwards its arguments and propagates the exit code. A refusal is refused
// here exactly as it is there, because a wrapper that swallowed the exit status
// would turn `pocctl preflight`'s red into `poc preflight`'s green, and that is
// the one thing a wrapper around a gate must never do.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	// The sibling binary, found next to this one rather than on PATH: an
	// operator who just built the module has bin/poc.exe and bin/pocctl.exe
	// side by side and nothing else on PATH.
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "poc: this program cannot find itself, so it cannot find pocctl either")
		os.Exit(1)
	}
	name := "pocctl"
	if runtime.GOOS != "windows" {
		name = "pocctl"
	} else {
		name = "pocctl.exe"
	}
	sibling := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(sibling); err != nil {
		fmt.Fprintf(os.Stderr,
			"poc: %s is not next to %s.\n"+
				"  Build both with 'go build -o bin/ ./cmd/...', or run pocctl directly.\n"+
				"  This wrapper does not exist to be convenient; it exists so the plan's spelling works.\n",
			sibling, self)
		os.Exit(1)
	}
	cmd := exec.Command(sibling, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		// Propagate the exit code exactly. A wrapper that turned a red gate green
		// would be worse than no wrapper at all.
		var ee *exec.ExitError
		if asExit(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "poc: pocctl could not be run: %v\n", err)
		os.Exit(1)
	}
}

func asExit(err error, dst **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*dst = e
	}
	return ok
}
