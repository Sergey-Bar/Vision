package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
)

// vlmProbeRequest is the parsed argument list.
//
// It is a struct rather than a bag of locals because the thing this command must
// not do is guess: every value it needs is either required or defaulted in one
// place, here, and nowhere else.
type vlmProbeRequest struct {
	region  vlm.Region
	apiKey  string
	model   string
	timeout time.Duration
	// outDir is where the raw body lands. It is a flag and not a constant because
	// the raw bodies are evidence and the person who ran the probe decides where
	// their evidence lives.
	outDir string
	// printBody caps what reaches the console. The file gets the whole thing
	// regardless; the console is for reading, the file is for the record.
	printBody bool
}

// rawDir is the default home for probe records: contracts/hafnia/raw/.
//
// It sits beside the contract it is evidence FOR, because a raw response filed
// somewhere else is a raw response nobody will find when someone asks whether
// assumption 6 was verified or merely written down.
func rawDir() string { return filepath.Join(repoRoot(), "contracts", "hafnia", "raw") }

// pinnedHostNames lists every host this build can reach, for a refusal.
//
// It reads the region map rather than repeating the hostnames, because a copy of
// the egress allow-list in a command's error message is a list that can drift from
// the allow-list and still look authoritative.
func pinnedHostNames() string {
	parts := make([]string, 0, len(vlm.KnownRegions()))
	for _, r := range vlm.KnownRegions() {
		h, err := r.Host()
		if err != nil {
			continue
		}
		parts = append(parts, string(r)+"="+h)
	}
	return strings.Join(parts, ", ")
}

func cmdVLMProbe(args []string) error {
	flags, err := flagSet("vlm-probe", map[string]string{
		"region": "", "model": "", "timeout": "30s", "out": rawDir(), "print-body": "false",
	}, args)
	if err != nil {
		return err
	}
	region := strings.ToLower(strings.TrimSpace(flags["region"]))
	if region == "" {
		region = strings.ToLower(strings.TrimSpace(os.Getenv("SP_POC_VLM_REGION")))
	}
	if region == "" {
		region = string(vlm.RegionEU)
	}
	if region != string(vlm.RegionEU) && region != string(vlm.RegionUS) {
		return fmt.Errorf("--region %q is not a region this client can reach. The pinned hosts are %s, and pointing the "+
			"probe anywhere else would answer a question about a host no run would ever call",
			region, pinnedHostNames())
	}
	timeout, err := time.ParseDuration(flags["timeout"])
	if err != nil || timeout <= 0 {
		return fmt.Errorf("--timeout %q is not a positive duration", flags["timeout"])
	}

	// The key and the model come from the environment ONLY. There is no flag for
	// either, and that is deliberate: a probe that can be run with a key typed on
	// the command line is a key that ends up in a shell history and a screenshot,
	// and the whole reason to probe is to check the configuration you have.
	apiKey := strings.TrimSpace(os.Getenv("SP_POC_VLM_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("SP_POC_VLM_API_KEY is not set. A probe with no key cannot reach the host, and a probe that echoed " +
			"the configuration back would be indistinguishable from one that reached it")
	}
	model := strings.TrimSpace(os.Getenv("SP_POC_VLM_MODEL"))
	if model == "" {
		return fmt.Errorf("SP_POC_VLM_MODEL is not set. The question is whether the host serves the model this run asks for, " +
			"and a probe with no model name cannot answer it")
	}

	req := vlmProbeRequest{
		region: vlm.Region(region), apiKey: apiKey, model: model, timeout: timeout,
		outDir:    flags["out"],
		printBody: flags["print-body"] == "true",
	}
	return runVLMProbe(req)
}

func runVLMProbe(req vlmProbeRequest) error {
	client, err := vlm.New(vlm.Config{
		Region: req.region, APIKey: req.apiKey, APIModelName: req.model,
		Timeout: req.timeout,
	})
	if err != nil {
		return err
	}
	fmt.Printf("vlm-probe: POST https://%s%s (region %s, model %s)\n", client.Host(), req.region, req.model, vlm.PathCompletions)
	fmt.Println("  one call, no clip, no footage leaves the site")

	rep, probeErr := client.Probe(context.Background())

	// The raw body is written FIRST and whatever happened. A probe run against a
	// host that returned 401 is the run that settles the auth assumption, and
	// discarding its body because it was an error would throw away exactly the
	// evidence the probe was run to collect.
	path, werr := writeRawBody(req, rep)
	if werr != nil {
		return fmt.Errorf("the probe's raw response could not be recorded: %w", werr)
	}

	fmt.Printf("  host answered: %s\n", rep.Host)
	fmt.Printf("  status:        %d\n", rep.Status)
	fmt.Printf("  latency:       %d ms\n", rep.LatencyMS)
	fmt.Printf("  model claimed: %s\n", noClaim(rep.ModelIdentity))
	fmt.Printf("  raw response:  %s\n", relToRepo(path))
	if probeErr != nil {
		fmt.Printf("  FAIL:          %v\n", probeErr)
	} else {
		fmt.Println("  OK: the pinned host answered this region, this path and this auth scheme")
	}
	if req.printBody && rep.Body != "" {
		fmt.Println()
		fmt.Println("  raw body:")
		fmt.Println(indentBlock(rep.Body))
	}
	fmt.Println()
	fmt.Println("  contracts/hafnia/contract.json is NOT changed by this command and must not be")
	fmt.Println("  by you: flipping `status` from asserted_not_verified to verified is a human")
	fmt.Println("  decision made after reading the file this command just wrote.")

	if probeErr != nil {
		return fmt.Errorf("the probe failed: %w", probeErr)
	}
	return nil
}

// noClaim reports an absent claim as an ABSENCE, in words.
//
// It is not `orNone` from audit.go (which takes a fallback), because the two answer
// different questions: a missing model name is not "unknown", it is the API having
// reported none, and "unknown" is a thing a reader could mistake for "this build
// did not look".
func noClaim(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none reported"
	}
	return s
}

func indentBlock(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}

// relToRepo prints a path relative to the module root when it can, so the line an
// operator reads is the path they would type into a file explorer.
func relToRepo(path string) string {
	if rel, err := filepath.Rel(repoRoot(), path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// writeRawBody records one raw response under contracts/hafnia/raw/.
//
// The name carries the region, the UTC timestamp and the status, so the directory
// is a directory of ANSWERS and not a directory of `response (3).json`. A 401 and
// a 200 in the same folder have to be distinguishable at a glance six weeks later.
func writeRawBody(req vlmProbeRequest, rep vlm.ProbeReport) (string, error) {
	if err := os.MkdirAll(req.outDir, 0o755); err != nil {
		return "", err
	}
	status := rep.Status
	name := fmt.Sprintf("%s-%s-%d.txt", time.Now().UTC().Format("20060102T150405Z"), rep.Region, status)
	path := filepath.Join(req.outDir, name)
	// The file carries the same facts the console printed, above the raw bytes, so
	// a file that travels on its own is still self-describing. The separator is
	// marked because the body is JSON and a file of bare JSON invites a reader to
	// treat it as the whole answer when it is one of two halves.
	var b strings.Builder
	fmt.Fprintf(&b, "# pocctl vlm-probe\n")
	fmt.Fprintf(&b, "at:       %s\n", rep.At.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "host:     %s\n", rep.Host)
	fmt.Fprintf(&b, "region:   %s\n", rep.Region)
	fmt.Fprintf(&b, "request:  %s %s\n", rep.Method, rep.Path)
	fmt.Fprintf(&b, "status:   %d\n", rep.Status)
	fmt.Fprintf(&b, "latency:  %dms\n", rep.LatencyMS)
	fmt.Fprintf(&b, "model:    %s\n", noClaim(rep.ModelIdentity))
	fmt.Fprintf(&b, "model_identity_source: the API's own claim; there is no weight hash for a hosted model\n")
	if rep.Err != nil {
		fmt.Fprintf(&b, "error:    %v\n", rep.Err)
	}
	fmt.Fprintf(&b, "\n--- raw response body ---\n%s\n", rep.Body)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
