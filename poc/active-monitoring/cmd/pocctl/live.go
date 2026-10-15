package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/cams"
)

// cmdLive is the public-relay camera path: the table, the resolver, and the
// environment one camera produces for `watch`.
//
// It exists as its own command rather than as flags on `demo` because the
// table is a thing an operator reads and edits, and because resolving a camera
// is a network act that happens BEFORE a run, not during one. A camera whose
// stream URL expired mid-run is a run that ended with a named transport
// failure; a camera whose URL expired at resolve time is a refusal that names
// which camera and what the resolver said.
//
// The honesty this command is built around: these are real cameras belonging
// to real operators, streamed publicly, and none of them are ours. The
// environment it prints says so in the one variable that reaches every screen
// and every report line — SP_POC_SOURCE_PUBLIC — and the source sentence that
// derives from it is "public relay of a real camera, NOT ours".
// cmdLive dispatches on the first token, the way `pocctl audit` does: the
// subcommand comes first and the handler parses what is left. The ids a camera
// subcommand takes are POSITIONAL, which flagSet refuses by design — an
// unknown flag is a typo on a measurement tool — so the splitter below hands
// flagSet only the flags and returns the ids beside them.
func cmdLive(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		liveUsage()
		return nil
	}
	flags, ids, err := liveArgs(args[1:])
	if err != nil {
		return err
	}
	sub := args[0]

	root := findModuleRoot()
	table := flags["table"]
	if table == "" {
		table = cams.TablePath(root)
	}
	camsList, err := cams.Load(table)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		cmdLiveList(camsList, table)
		return nil
	case "resolve":
		return cmdLiveResolve(camsList, ids, flags)
	case "env":
		return cmdLiveEnv(camsList, ids, flags)
	default:
		return fmt.Errorf("live: %q is not list, resolve or env", sub)
	}
}

// liveArgs splits a camera subcommand's arguments into flags and ids.
//
// It exists because flagSet refuses a positional token, which is the right
// default and the wrong one here: `live resolve times-square-north` names a
// camera, and a parser that cannot tell a camera id from a typo'd flag cannot
// accept either. The flag/value rule is the same one flagSet uses — a boolean
// consumes no value, everything else does — so the two cannot disagree about
// where the ids start.
func liveArgs(args []string) (map[string]string, []string, error) {
	specs := map[string]string{
		"table": "", "ytdlp": "", "js": "", "format": "ps", "all": boolSpec,
	}
	var flagArgs, ids []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			ids = append(ids, a)
			continue
		}
		key := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			// Normalised to the space form flagSet parses, so `--format=sh`
			// and `--format sh` cannot diverge.
			flagArgs = append(flagArgs, "--"+key[:eq], key[eq+1:])
			continue
		}
		if key == "all" {
			flagArgs = append(flagArgs, a)
			continue
		}
		if _, known := specs[key]; !known {
			return nil, nil, fmt.Errorf("live: --%s is not a flag of this subcommand. Known flags: %s",
				key, strings.Join(sortedKeys(specs), ", "))
		}
		if i+1 >= len(args) {
			return nil, nil, fmt.Errorf("live: --%s needs a value", key)
		}
		flagArgs = append(flagArgs, a, args[i+1])
		i++
	}
	flags, err := flagSet("live", specs, flagArgs)
	if err != nil {
		return nil, nil, err
	}
	return flags, ids, nil
}

func liveUsage() error {
	fmt.Print(`pocctl live — the public-relay cameras (real cameras, run by somebody else)

  live list                    the curated table: place, operator, licence basis, verified date
  live resolve [ids...]        resolve each named camera to its HLS playlist and print the stream's host
  live resolve --all           the same for every row
  live env <id>                the SP_POC_SOURCE_* lines one camera produces, for ` + "`watch`" + `

  --table <path>               a different camera table (default: ` + cams.TableFile + ` at the module root)
  --ytdlp <path>               the resolver binary (default: $SP_POC_YTDLP, then yt-dlp on PATH)
  --js node|deno|none          the JavaScript runtime yt-dlp uses (default: auto-detect)
  --format ps|sh               the assignment syntax "live env" prints (default: ps)

The cameras are real and they are not ours. Every run over one is labelled
"public relay of a real camera, NOT ours" on the screen, in the log and in the
report, and SP_POC_SOURCE_LICENCE carries the recorded basis for the footage.
`)
	return nil
}

func cmdLiveList(list []cams.Camera, table string) {
	fmt.Printf("# the curated public-relay cameras (%s)\n\n", table)
	for _, c := range list {
		lic := "no recorded basis - REFUSED"
		if c.HasLicence() {
			lic = c.Licence
			if len(lic) > 100 {
				lic = lic[:100] + "…"
			}
		}
		fmt.Printf("%-22s %s\n", c.ID, c.Name)
		fmt.Printf("%-22s %s\n", "", c.Place)
		fmt.Printf("%-22s operator %s; verified live %s\n", "", c.Operator, c.Verified)
		fmt.Printf("%-22s %s\n", "", c.URL)
		fmt.Printf("%-22s licence: %s\n", "", lic)
		ext := "NOT cleared for external use — a screenshot of a run over this camera is a screenshot of this camera"
		if c.ClearedForExternalUse {
			ext = "cleared for external use"
		}
		fmt.Printf("%-22s %s\n\n", "", ext)
	}
	fmt.Println("None of these cameras is ours. A run over one is labelled a public relay of a real")
	fmt.Println("camera, and the table's licence field records the public basis for WATCHING the feed;")
	fmt.Println("storing clips is the operator's to permit, which is what SP_POC_SOURCE_LICENCE states.")
}

func liveResolver(flags map[string]string) cams.Resolver {
	return cams.Resolver{
		YtDlp:     flags["ytdlp"],
		JSRuntime: flags["js"],
	}
}

func cmdLiveResolve(list []cams.Camera, ids []string, flags map[string]string) error {
	if flags["all"] == "true" {
		ids = cams.IDs(list)
	}
	if len(ids) == 0 {
		return fmt.Errorf("live resolve: no camera named. `live list` prints the ids; --all resolves every row")
	}
	r := liveResolver(flags)
	var failed []string
	for _, id := range ids {
		cam, ok := cams.Find(list, id)
		if !ok {
			return fmt.Errorf("live resolve: %q is not in the table. The ids are: %s", id, strings.Join(cams.IDs(list), ", "))
		}
		stream, err := r.Resolve(context.Background(), cam)
		if err != nil {
			fmt.Printf("  %-22s FAILED\n", cam.ID)
			fmt.Printf("      %s\n", strings.ReplaceAll(err.Error(), "\n", "\n      "))
			failed = append(failed, cam.ID)
			continue
		}
		fmt.Printf("  %-22s %s\n", cam.ID, cams.Redacted(stream))
	}
	if len(failed) == len(ids) {
		return fmt.Errorf("live resolve: none of the %d camera(s) resolved, so this machine has no public relay to read", len(ids))
	}
	if len(failed) > 0 {
		fmt.Printf("\n  %d of %d camera(s) did not resolve this run. The one(s) above answered; the\n", len(failed), len(ids))
		fmt.Println("  failures are named rather than skipped, because a camera that answered on its")
		fmt.Println("  verification date is a camera that may be offline today.")
	}
	return nil
}

func cmdLiveEnv(list []cams.Camera, ids []string, flags map[string]string) error {
	if len(ids) != 1 {
		return fmt.Errorf("live env: name exactly one camera. The ids are: %s", strings.Join(cams.IDs(list), ", "))
	}
	cam, ok := cams.Find(list, ids[0])
	if !ok {
		return fmt.Errorf("live env: %q is not in the table. The ids are: %s", ids[0], strings.Join(cams.IDs(list), ", "))
	}
	stream, err := liveResolver(flags).Resolve(context.Background(), cam)
	if err != nil {
		return err
	}
	spec, err := cams.Spec(cam, stream)
	if err != nil {
		return err
	}
	assign := func(k, v string) string {
		if flags["format"] == "sh" {
			return fmt.Sprintf("export %s=%q", k, v)
		}
		return fmt.Sprintf("$env:%s = '%s'", k, strings.ReplaceAll(v, "'", "''"))
	}
	fmt.Printf("# %s — %s\n", cam.Name, cam.Place)
	fmt.Printf("# operator %s; row verified live %s\n", cam.Operator, cam.Verified)
	fmt.Printf("# %s\n", spec.Label())
	fmt.Println(assign("SP_POC_SOURCE_KIND", "url"))
	fmt.Println(assign("SP_POC_SOURCE_ARG", stream))
	fmt.Println(assign("SP_POC_SOURCE_PUBLIC", "true"))
	fmt.Println(assign("SP_POC_SOURCE_RELAY", "false"))
	fmt.Println(assign("SP_POC_SOURCE_LICENCE", cam.Licence))
	fmt.Println("# The camera is real and it is not ours: the source line on the screen, in the")
	fmt.Println("# log and in the report will read `public relay of a real camera, NOT ours`.")
	return nil
}
