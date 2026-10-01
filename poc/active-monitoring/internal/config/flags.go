package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// FlagSet is the set of command-line flags the module accepts. Every flag maps
// to a variable that is also an environment variable, and a flag WINS over the
// environment. The two exist for different audiences: a flag is what a person
// types once, and an environment variable is what a run records so a week-long
// run can be reproduced from its own configuration.
//
// There is no flag that has no variable. A value that exists only as a flag
// would be a value the run cannot reproduce and the report cannot name.
var FlagSet = map[string]string{
	"data-root":         "SP_POC_DATA_ROOT",
	"mode":              "SP_POC_MODE",
	"source-kind":       "SP_POC_SOURCE_KIND",
	"source-arg":        "SP_POC_SOURCE_ARG",
	"ai-addr":           "SP_POC_AI_ADDR",
	"ai-model":          "SP_POC_AI_MODEL",
	"sidecar-script":    "SP_POC_SIDECAR_SCRIPT",
	"ai-fps":            "SP_POC_AI_FPS",
	"ai-deadline":       "SP_POC_AI_DEADLINE_MS",
	"ai-budget":         "SP_POC_AI_BUDGET_MS",
	"ai-backpressure":   "SP_POC_AI_BACKPRESSURE_MS",
	"ai-input-width":    "SP_POC_AI_INPUT_WIDTH",
	"score-threshold":   "SP_POC_SCORE_THRESHOLD",
	"classes":           "SP_POC_CLASSES",
	"zones":             "SP_POC_ZONES",
	"cooldown":          "SP_POC_COOLDOWN_MS",
	"track-max-age":     "SP_POC_TRACK_MAX_AGE_MS",
	"track-iou":         "SP_POC_TRACK_IOU",
	"line-tolerance":    "SP_POC_LINE_TOLERANCE",
	"clip-before":       "SP_POC_CLIP_BEFORE_MS",
	"clip-after":        "SP_POC_CLIP_AFTER_MS",
	"max-disk":          "SP_POC_MAX_DISK_BYTES",
	"rss-limit":         "SP_POC_RSS_LIMIT_BYTES",
	"db-limit":          "SP_POC_DB_LIMIT_BYTES",
	"cpu-limit":         "SP_POC_CPU_LIMIT_PERCENT",
	"disk-headroom":     "SP_POC_DISK_HEADROOM",
	"disk-budget-days":  "SP_POC_DISK_BUDGET_DAYS",
	"heartbeat":         "SP_POC_HEARTBEAT_INTERVAL",
	"gap-threshold":     "SP_POC_GAP_THRESHOLD",
	"soak-target":       "SP_POC_SOAK_TARGET",
	"segment-seconds":   "SP_POC_SEGMENT_SECONDS",
	"clock-source":      "SP_POC_CLOCK_SOURCE",
	"sim-url":           "SP_POC_SIM_URL",
	"xprotect-base-url": "SP_POC_XPROTECT_BASE_URL",
	"camera-guid":       "SP_POC_CAMERA_GUID",
	"camera-integrity":  "SP_POC_CAMERA_INTEGRITY",
	"device-path":       "SP_POC_DEVICE_PATH",
	"event-type-guid":   "SP_POC_EVENT_TYPE_GUID",
	"push-max-attempts": "SP_POC_PUSH_MAX_ATTEMPTS",
	"push-timeout":      "SP_POC_PUSH_TIMEOUT",
	"ui-addr":           "SP_POC_UI_ADDR",
	"label":             "SP_POC_LABEL",
}

// LoadWithFlags reads the environment and then applies the command line on top.
// It is the single place the runtime configuration is read: there is no second
// read anywhere in the module.
//
// A flag wins over the environment, and the reason is the one the plan gives:
// a run must be reproducible from the configuration it records, and a person who
// types a flag is making a deliberate statement about this run.
func LoadWithFlags(args []string, lookup Lookup) (Config, error) {
	overlay, err := parseFlags(args, lookup)
	if err != nil {
		return Config{}, err
	}
	cfg, err := Load(overlay)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadEnvAndFlags is LoadWithFlags against the process environment and
// os.Args.
func LoadEnvAndFlags() (Config, error) { return LoadWithFlags(os.Args[1:], os.LookupEnv) }

// parseFlags builds a Lookup that answers from the environment and is overridden
// by whatever the command line said.
func parseFlags(args []string, lookup Lookup) (Lookup, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	over := map[string]string{}
	var unknown []string
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			// A bare argument is left for the caller: this module has no
			// positional arguments, and pretending otherwise would let a typo
			// become a silently ignored argument.
			continue
		}
		name := strings.TrimLeft(a, "-")
		var value string
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name, value = name[:eq], name[eq+1:]
		} else {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("config: --%s needs a value", name)
			}
			i++
			value = args[i]
		}
		env, ok := FlagSet[name]
		if !ok {
			unknown = append(unknown, "--"+name)
			continue
		}
		over[env] = value
	}
	if len(unknown) > 0 {
		parts := make([]string, 0, len(unknown))
		for _, u := range unknown {
			if near := closest(u, FlagNames()); near != "" {
				parts = append(parts, fmt.Sprintf("%s (did you mean %s?)", u, near))
			} else {
				parts = append(parts, u)
			}
		}
		return nil, fmt.Errorf("config: %s %s not a flag of this program. A flag that does nothing on a measurement tool is worse than a refusal: run with --help for the list",
			strings.Join(parts, ", "), plural(len(unknown), "is", "are"))
	}
	return func(name string) (string, bool) {
		if v, ok := over[name]; ok {
			return v, true
		}
		return lookup(name)
	}, nil
}

// closest returns the flag name within a small edit distance of the input, so a
// refusal can say what the operator probably meant. It is a courtesy, not a
// repair: the flag is still refused.
func closest(input string, names []string) string {
	stripped := strings.TrimLeft(input, "-")
	best, bestDist := "", 1<<30
	for _, n := range names {
		d := editDistance(stripped, strings.TrimLeft(n, "-"))
		if d < bestDist {
			best, bestDist = n, d
		}
	}
	if bestDist <= 2 {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = minInt(minInt(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// FlagNames returns the flag names, sorted, for --help.
func FlagNames() []string {
	out := make([]string, 0, len(FlagSet))
	for k := range FlagSet {
		out = append(out, "--"+k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// WriteUsage prints the flags and their variables.
func WriteUsage(w io.Writer) {
	fmt.Fprintln(w, "Configuration comes from the environment and from flags, and a flag wins.")
	fmt.Fprintln(w, "Both exist because a person types a flag once and a week-long run records")
	fmt.Fprintln(w, "variables. There is no flag without a variable: a value that existed only")
	fmt.Fprintln(w, "as a flag would be a run nobody could reproduce.")
	fmt.Fprintln(w)
	for _, name := range FlagNames() {
		key := strings.TrimLeft(name, "-")
		fmt.Fprintf(w, "  %-22s %s\n", name, FlagSet[key])
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Every variable is required. A missing one fails by name, with the")
	fmt.Fprintln(w, "consequence of not having it. There is no default that changes behaviour.")
}

// DurationFlag is a small helper for callers that want Go's flag package: the
// value arrives as a string and is parsed here so the failure message names the
// variable rather than a flag index.
func DurationFlag(set *flag.FlagSet, name, env, usage string, target *time.Duration) {
	set.Var(newDurationValue(target), name, fmt.Sprintf("%s (%s)", usage, env))
}

type durationValue struct{ v *time.Duration }

func newDurationValue(v *time.Duration) *durationValue { return &durationValue{v} }

func (d durationValue) String() string {
	if d.v == nil {
		return "0s"
	}
	return d.v.String()
}

func (d durationValue) Set(s string) error {
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a duration: %w", s, err)
	}
	*d.v = parsed
	return nil
}

var _ = strconv.Itoa
