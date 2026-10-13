package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

func faultVerdict(f store.Fault) (soak.Verdict, string) {
	if f.Expected != "" {
		if f.Observed == f.Expected {
			return soak.VerdictPass, f.Detail
		}
		return soak.VerdictFail, fmt.Sprintf("expected %s, observed %s: %s", f.Expected, f.Observed, f.Detail)
	}
	return soak.VerdictFail, f.Detail
}

func kv(b *strings.Builder, k, v string) {
	fmt.Fprintf(b, "<b>%s</b><div>%s</div>", esc(k), esc(v))
}

func verdictClass(v string) string {
	switch v {
	case "PASS":
		return "PASS"
	case "FAIL":
		return "FAIL"
	}
	return "NOTRUN"
}

func provisionalSuffix(p bool) string {
	if !p {
		return ""
	}
	return " &mdash; PROVISIONAL: the run has not reached its target, so nothing here concludes"
}

func gapText(gaps []store.Gap) string {
	if len(gaps) == 0 {
		return "none"
	}
	var worst store.Gap
	for _, g := range gaps {
		if g.Seconds > worst.Seconds {
			worst = g
		}
	}
	return fmt.Sprintf("%.1fs at %s", worst.Seconds, worst.From.Format(time.RFC3339))
}

func countOr(m map[string]int) string {
	if len(m) == 0 {
		return "no data"
	}
	n := 0
	for _, v := range m {
		n += v
	}
	return itoa(n)
}

func sumNonDone(m map[string]int) int {
	n := 0
	for k, v := range m {
		if k != "done" {
			n += v
		}
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func short(s string) string {
	if s == "" {
		return "none"
	}
	if len(s) > 18 {
		return s[:18] + "…"
	}
	return s
}

func humanBytes(n int64) string {
	if n == 0 {
		return "no samples"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
