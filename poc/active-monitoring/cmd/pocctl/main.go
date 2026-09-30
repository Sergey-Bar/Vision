// Command pocctl is the POC's operator tool: the capability table, the contract
// probe, preflight, the fault rehearsal, the report, the demo, and the two
// builders that make labelled test material.
//
// Every subcommand prints where its numbers came from. A subcommand that cannot
// measure something prints why, and never prints a zero in its place.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "cap":
		err = cmdCap(os.Args[2:])
	case "probe":
		err = cmdProbe(os.Args[2:])
	case "preflight":
		err = cmdPreflight(os.Args[2:])
	case "rehearse":
		err = cmdRehearse(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "soak":
		err = cmdSoak(os.Args[2:])
	case "local-faults":
		err = soakFaults(os.Args[2:])
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "zone":
		err = cmdZone(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "sim":
		err = cmdSim(os.Args[2:])
	case "fetch-model":
		err = cmdFetchModel(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "pocctl: %q is not a subcommand\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pocctl: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`pocctl — the active-monitoring POC's operator tool

  cap                      the measured capability table (codec, decoders, GPU, decode cost, disk budget)
  probe                    the Milestone contract confidence table
  preflight                the full preflight table; refuses to start by name
  rehearse [all|<fault>]   inject each rehearsable provider fault against xpsim and record what happened
  soak start               preflight + gate D; refuses to launch a week on code that has not been rehearsed
  soak status              the live state of the newest run, read from the store
  soak report              the report, regenerated from the store
  soak faults              the six LOCAL fault classes: worker, ffmpeg, python, db freeze, disk fill, sim down
  report [--run <id>]      re-read the store and print the report
  demo                     the three-minute demonstration against xpsim
  zone --pick <source>     print a still with a coordinate grid, to draw a line on
  run                      run the pipeline in the foreground
  sim                      run the xpsim simulator on loopback
  fetch-model              download the ONNX model into models/

Run 'pocctl <subcommand> --help' for a subcommand's own flags.

Every subcommand prints where its numbers came from. None of them prints a
default that changes a verdict. 'poc' and 'pocctl' are the same program.
`)
}

// flagSet parses `--name value` pairs. specs is the set of known flag names
// mapped to their default, so an unknown flag is refused rather than silently
// accepted: a typo in a flag name on a measurement tool is a flag that did
// nothing, and a run that did not do what the operator asked is worse than a
// refusal.
// flagSet parses `--name value` pairs from ARGS, not from os.Args.
//
// It used to read os.Args[2:] itself, which is wrong for every subcommand that
// has a name of its own: `poc soak status --data-root X` put "status" in front
// of the flags and every invocation was refused with `"status" is not a flag`.
// The caller passes the arguments, because the caller is the only place that
// knows where they start.
// boolSpec is the spec value that marks a flag as BOOLEAN. A boolean flag
// takes no value, so `--force` does not swallow the token after it.
//
// It is a sentinel in the spec map rather than a second map because then every
// subcommand keeps ONE place that says what its flags are, and a flag declared
// in two places is a flag that will be declared wrongly in one of them.
const boolSpec = "bool"

func flagSet(name string, specs map[string]string, args []string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range specs {
		if v == boolSpec {
			out[k] = "false"
			continue
		}
		out[k] = v
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return nil, fmt.Errorf("%s: %q is not a flag. Flags are --name value", name, a)
		}
		key := strings.TrimLeft(a, "-")
		def, known := specs[key]
		if !known {
			return nil, fmt.Errorf("%s: --%s is not a flag of this subcommand. Known flags: %s",
				name, key, strings.Join(sortedKeys(specs), ", "))
		}
		if def == boolSpec {
			// A boolean consumes no value. `--force=false` is honoured because it
			// is unambiguous; a bare `--force` is true.
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				out[key[:eq]] = key[eq+1:]
				continue
			}
			out[key] = "true"
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("%s: --%s needs a value", name, key)
		}
		i++
		out[key] = args[i]
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func logf(format string, args ...any) {
	fmt.Printf("%s  %s\n", time.Now().UTC().Format("15:04:05"), fmt.Sprintf(format, args...))
}

var errNotConfigured = errors.New("pocctl: the environment is not configured. Set the SP_POC_* variables; 'pocctl cap' and 'pocctl sim' work without any")

func ctx() context.Context { return context.Background() }
