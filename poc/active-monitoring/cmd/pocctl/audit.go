package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
)

// The proof commands: `pocctl prove`, `pocctl audit`.
//
// They exist because "we can show you the evidence" and "somebody can check our
// evidence without trusting us" are different claims, and the second one is the
// one that survives a sceptic.
//
// Three of them, and each answers a question a sceptic actually asks:
//
//	prove <detection>   recompute this decision from stored facts, zero provider calls
//	audit ledger        recompute the hash chain and say where it breaks
//	audit seal          take or verify an ed25519 signature over the chain head
//	audit decisions     print the stored decision inputs for a run
//	audit seals         verify every seal file against a trusted key
//
// The one rule they all obey: a number that was not measured is printed as NOT RUN
// with the reason, never as a zero.

// cmdProve is W3-4, and it is the command a judge runs.
func cmdProve(args []string) error {
	flags, err := flagSet("prove", map[string]string{"data-root": os.Getenv("SP_POC_DATA_ROOT"), "run": ""}, args)
	if err != nil {
		return err
	}
	root := flags["data-root"]
	if root == "" {
		return errNotConfigured
	}
	st, err := store.Open(root)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	detection := ""
	switch {
	case len(args) > 0 && !strings.HasPrefix(args[0], "-"):
		detection = args[0]
	case flags["run"] != "":
		// `--run` picks the newest detection in that run, because "prove the run"
		// is what somebody actually asks and making them copy an id out of a
		// report is how a proof command goes unused.
		recent, err := st.Recent(ctx(), flags["run"], 1)
		if err != nil {
			return err
		}
		if len(recent) == 0 {
			return fmt.Errorf("prove: run %s has no detections, so there is nothing to prove. A proof of nothing "+
				"is not a proof", flags["run"])
		}
		detection = recent[0].ID
	default:
		return errors.New("prove: name a detection id, or pass --run <id> for the newest detection in that run")
	}

	ev, err := evidence.Open(filepath.Join(root, "evidence"))
	if err != nil {
		return err
	}
	rep, err := soak.Replay(ctx(), soak.ReplayInput{
		Store: st, Evidence: ev, Detection: detection,
		// The counter is the measurement, not the guarantee: the replay function
		// has no observer parameter and therefore no way to call a model.
		Calls: providerCounter(),
	})
	if err != nil {
		return err
	}
	fmt.Print(soak.FormatReplay(rep))
	if rep.Verdict == "FAIL" {
		return fmt.Errorf("prove: the replay did not reproduce this decision")
	}
	return nil
}

// providerCounter returns a counter for the provider's client if one is
// configured, and nil otherwise.
//
// nil is not a silent pass: soak.Replay records "no counter was supplied, so the
// property is UNMEASURED rather than true" as a FAILED beat. That is the honest
// outcome when nobody configured a key, and it is what stops `prove` printing a
// reassuring zero for a run where the counter was never wired.
func providerCounter() func() int64 {
	provider := os.Getenv("SP_POC_VLM_PROVIDER")
	if strings.EqualFold(strings.TrimSpace(provider), "none") || provider == "" {
		return nil
	}
	c, err := vlm.New(vlm.Config{
		Region:       vlm.Region(strings.ToLower(strings.TrimSpace(os.Getenv("SP_POC_VLM_REGION")))),
		APIKey:       os.Getenv("SP_POC_VLM_API_KEY"),
		APIModelName: os.Getenv("SP_POC_VLM_MODEL"),
	})
	if err != nil {
		return nil
	}
	return c.Calls
}

// cmdAudit is the family. It prints its own sub-help rather than refusing an
// unknown one, because an audit tool that says "not a subcommand" when the answer
// is one word away is a tool nobody asks twice.
func cmdAudit(args []string) error {
	if len(args) == 0 {
		auditUsage()
		return nil
	}
	switch args[0] {
	case "ledger":
		return auditLedger(args[1:])
	case "seal":
		return auditSeal(args[1:])
	case "seals":
		return auditSeals(args[1:])
	case "decisions":
		return auditDecisions(args[1:])
	case "observations":
		return auditObservations(args[1:])
	case "-h", "--help", "help":
		auditUsage()
		return nil
	default:
		auditUsage()
		return fmt.Errorf("audit: %q is not an audit subcommand", args[0])
	}
}

func auditUsage() {
	fmt.Print(`pocctl audit — check this run's own claims

  audit ledger [--run <id>]       recompute the hash chain and name the first broken row
  audit seal [--run <id>]         sign the chain head with a key, or verify the existing seal
  audit seals --trusted-key <f>   verify every seal file against a named public key
  audit decisions [--run <id>]    the stored decision inputs: score, threshold, measured region
  audit observations [--run <id>] every model's answer, with its attribution and reason code

A chain that verifies is not the same claim as a chain somebody attested to, and
this prints which of the two you are looking at.
`)
}

func openStoreFor(flags map[string]string) (*store.Store, error) {
	root := flags["data-root"]
	if root == "" {
		return nil, errNotConfigured
	}
	return store.Open(root)
}

// resolveRun picks a run: the named one, or the newest in the store.
//
// "The newest run" is a default an operator can be surprised by, so it is PRINTED
// whenever it is used. A tool that silently proves the wrong run is worse than
// one that asks.
func resolveRun(st *store.Store, named string) (string, error) {
	if named != "" {
		return named, nil
	}
	run, err := st.LatestRun(ctx())
	if err != nil {
		return "", err
	}
	if run == nil || run.ID == "" {
		return "", errors.New("this data root has no runs, so there is nothing to audit")
	}
	fmt.Printf("# the newest run in this data root is %s; name another with --run\n\n", run.ID)
	return run.ID, nil
}

// auditLedger recomputes the chain. W3-1's observable half.
func auditLedger(args []string) error {
	flags, err := flagSet("audit ledger", map[string]string{"data-root": os.Getenv("SP_POC_DATA_ROOT"), "run": "", "detection": ""}, args)
	if err != nil {
		return err
	}
	st, err := openStoreFor(flags)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if flags["detection"] != "" {
		rows, err := st.LedgerFor(ctx(), flags["detection"])
		if err != nil {
			return err
		}
		printLedgerRows(rows)
		rep, err := st.VerifyLedger(ctx(), 0, 0)
		if err != nil {
			return err
		}
		fmt.Printf("\n  chain over the whole ledger: %s\n", chainLine(rep))
		return nil
	}
	runID, err := resolveRun(st, flags["run"])
	if err != nil {
		return err
	}
	rep, err := st.VerifyLedger(ctx(), 0, 0)
	if err != nil {
		return err
	}
	rows, err := st.Ledger(ctx(), 0, 0)
	if err != nil {
		return err
	}
	fmt.Printf("# ledger for run %s\n\n", runID)
	printLedgerRows(rows)
	fmt.Printf("\n  %s\n", chainLine(rep))
	if !rep.Valid {
		return fmt.Errorf("audit ledger: the chain does not verify")
	}
	return nil
}

func printLedgerRows(rows []store.LedgerRow) {
	if len(rows) == 0 {
		fmt.Println("  the ledger is empty. An empty chain is not an intact one; it is the absence of evidence.")
		return
	}
	fmt.Printf("  %5s  %-22s  %-26s  %s\n", "seq", "kind", "subject", "hash")
	for _, r := range rows {
		fmt.Printf("  %5d  %-22s  %-26s  %s\n", r.Seq, r.Kind, trunc(r.Subject, 26), r.Hash[:16])
	}
}

func chainLine(rep store.ChainReport) string {
	if rep.Valid {
		return fmt.Sprintf("PASS — %s", rep.Reason)
	}
	return fmt.Sprintf("FAIL — %s", rep.Reason)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// auditSeal takes or verifies one run's seal. W3-3.
func auditSeal(args []string) error {
	flags, err := flagSet("audit seal", map[string]string{
		"data-root": os.Getenv("SP_POC_DATA_ROOT"), "run": "",
		"key": "", "public-key": "", "verify": boolSpec, "print-key": boolSpec,
	}, args)
	if err != nil {
		return err
	}
	if flags["print-key"] == "true" {
		if flags["public-key"] == "" {
			return errors.New("audit seal --print-key needs --public-key <file>, and the file is the PUBLIC half; the " +
				"private half never leaves the machine that signed")
		}
		raw, err := os.ReadFile(flags["public-key"])
		if err != nil {
			return err
		}
		pub, err := store.ParseTrustedKey(raw)
		if err != nil {
			return err
		}
		fmt.Printf("  key id  %s\n", store.KeyID(pub))
		fmt.Printf("  sha256  %s\n", store.KeyFingerprint(pub))
		fmt.Printf("  read it out loud and compare it with the seal's key_id. Two fingerprints that match is the whole check.\n")
		return nil
	}

	st, err := openStoreFor(flags)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	runID, err := resolveRun(st, flags["run"])
	if err != nil {
		return err
	}
	path := st.SealPath(runID)

	if flags["verify"] == "true" {
		seal, err := store.ReadSeal(path)
		if err != nil {
			return err
		}
		var trusted ed25519.PublicKey
		if flags["public-key"] != "" {
			raw, err := os.ReadFile(flags["public-key"])
			if err != nil {
				return err
			}
			trusted, err = store.ParseTrustedKey(raw)
			if err != nil {
				return err
			}
		}
		rep, err := st.VerifySeal(ctx(), seal, trusted)
		if err != nil {
			return err
		}
		fmt.Printf("  seal     %s\n", path)
		fmt.Printf("  key id   %s\n", seal.KeyID)
		fmt.Printf("  rows     %d\n", seal.Rows)
		fmt.Printf("  head     %s\n", seal.Head)
		fmt.Printf("  sealed   %s\n", seal.SealedAt)
		fmt.Printf("\n  %s\n", store.SealSummary(rep, seal))
		if !rep.SignatureOK || !rep.ChainOK || !rep.Trusted {
			return fmt.Errorf("audit seal --verify: the seal does not verify")
		}
		return nil
	}

	if flags["key"] == "" {
		return errors.New("audit seal needs --key <private.pem> to sign, or --verify to check an existing seal. A seal " +
			"is taken with a PRIVATE key and checked with a PUBLIC one; a tool that let you sign with the public half " +
			"would prove nothing")
	}
	raw, err := os.ReadFile(flags["key"])
	if err != nil {
		return err
	}
	priv, err := store.ParsePrivateKey(raw)
	if err != nil {
		return err
	}
	contract := os.Getenv("SP_POC_CONTRACT_VERSION")
	seal, err := st.SealLedger(ctx(), runID, priv, contract, time.Now())
	if err != nil {
		return err
	}
	if err := store.WriteSeal(path, seal); err != nil {
		return err
	}
	fmt.Printf("  seal     %s\n", path)
	fmt.Printf("  run      %s\n", seal.RunID)
	fmt.Printf("  rows     %d\n", seal.Rows)
	fmt.Printf("  head     %s\n", seal.Head)
	fmt.Printf("  key id   %s\n", seal.KeyID)
	fmt.Printf("  sealed   %s\n\n", seal.SealedAt)
	fmt.Println("  the chain verified BEFORE it was signed. A seal over a broken chain would convert a detectable")
	fmt.Println("  defect into an attested one, which is worse than no seal at all.")
	return nil
}

// auditSeals verifies every seal file beside the database. W3-3's sweep.
func auditSeals(args []string) error {
	flags, err := flagSet("audit seals", map[string]string{
		"data-root": os.Getenv("SP_POC_DATA_ROOT"), "trusted-key": "",
	}, args)
	if err != nil {
		return err
	}
	st, err := openStoreFor(flags)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	var trusted ed25519.PublicKey
	if flags["trusted-key"] != "" {
		raw, err := os.ReadFile(flags["trusted-key"])
		if err != nil {
			return err
		}
		trusted, err = store.ParseTrustedKey(raw)
		if err != nil {
			return err
		}
		fmt.Printf("  trusted key id %s\n\n", store.KeyID(trusted))
	} else {
		// NOT RUN rather than "all fine". Verifying a signature against a key the
		// verifier chose themselves proves that the signature is well-formed, and
		// that is a weaker claim with a very similar shape.
		fmt.Println("  no --trusted-key was named, so this checks that each signature is well-formed and covers the")
		fmt.Println("  chain. It does NOT establish WHO signed. That is a different claim and needs the key.")
	}

	dir := filepath.Dir(st.SealPath("x"))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("  no seal directory exists, so no run has been sealed. NOT RUN, not PASS.")
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".seal.json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("  no seals exist. NOT RUN, not PASS.")
		return nil
	}
	bad := 0
	for _, n := range names {
		seal, err := store.ReadSeal(filepath.Join(dir, n))
		if err != nil {
			fmt.Printf("  [%s] %s  %v\n", "FAIL", n, err)
			bad++
			continue
		}
		rep, err := st.VerifySeal(ctx(), seal, trusted)
		if err != nil {
			fmt.Printf("  [%s] %s  %v\n", "FAIL", n, err)
			bad++
			continue
		}
		mark := "PASS"
		if !rep.SignatureOK || !rep.ChainOK {
			mark = "FAIL"
			bad++
		}
		fmt.Printf("  [%s] %-28s run=%s rows=%d  %s\n", mark, n, seal.RunID, seal.Rows,
			trunc(rep.Detail, 90))
	}
	fmt.Printf("\n  %d seal(s), %d not verifying\n", len(names), bad)
	if bad > 0 {
		return fmt.Errorf("audit seals: %d seal(s) do not verify", bad)
	}
	return nil
}

// auditDecisions prints the stored decision inputs. W3-4's data.
func auditDecisions(args []string) error {
	flags, err := flagSet("audit decisions", map[string]string{"data-root": os.Getenv("SP_POC_DATA_ROOT"), "run": "", "detection": ""}, args)
	if err != nil {
		return err
	}
	st, err := openStoreFor(flags)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if flags["detection"] != "" {
		d, err := st.DecisionFor(ctx(), flags["detection"])
		if err != nil {
			return err
		}
		printDecision(*d)
		return nil
	}
	runID, err := resolveRun(st, flags["run"])
	if err != nil {
		return err
	}
	rows, err := st.Decisions(ctx(), runID)
	if err != nil {
		return err
	}
	fmt.Printf("# stored decision inputs for run %s\n\n", runID)
	if len(rows) == 0 {
		fmt.Println("  no decision inputs were recorded for this run.")
		fmt.Println("  That means the run predates this build, or the trigger recorded nothing. It is NOT a zero,")
		fmt.Println("  and it is NOT a claim that the run made no decisions.")
		return nil
	}
	for _, d := range rows {
		printDecision(d)
	}
	return nil
}

func printDecision(d store.Decision) {
	fmt.Printf("  %s\n", d.DetectionID)
	fmt.Printf("    %s\n", soak.DecisionLine(d))
	if d.ReferenceSHA != "" {
		fmt.Printf("    reference  %s\n", d.ReferenceSHA)
	}
	if d.FrameSeq != 0 {
		fmt.Printf("    frame      %d\n", d.FrameSeq)
	}
}

// auditObservations prints every answer with its attribution and its reason code.
func auditObservations(args []string) error {
	flags, err := flagSet("audit observations", map[string]string{"data-root": os.Getenv("SP_POC_DATA_ROOT"), "run": "", "detection": ""}, args)
	if err != nil {
		return err
	}
	st, err := openStoreFor(flags)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if flags["detection"] != "" {
		o, err := st.ObservationFor(ctx(), flags["detection"])
		if err != nil {
			return err
		}
		printObservation(*o)
		return nil
	}
	runID, err := resolveRun(st, flags["run"])
	if err != nil {
		return err
	}
	rows, err := st.Observations(ctx(), runID)
	if err != nil {
		return err
	}
	fmt.Printf("# model answers for run %s\n\n", runID)
	if len(rows) == 0 {
		fmt.Println("  no model answers were recorded. That is a fact about this run and not a claim that the model")
		fmt.Println("  found nothing: SP_POC_VLM_PROVIDER=none means nobody was asked.")
		return nil
	}
	for _, o := range rows {
		printObservation(o)
	}
	tally, err := st.ObservationTally(ctx(), runID)
	if err != nil {
		return err
	}
	fmt.Printf("\n  %d answer(s): %d confirmed, %d false_positive, %d needs_review, %d no verdict\n",
		tally.Total, tally.Confirmed, tally.FalsePositive, tally.NeedsReview, tally.None)
	fmt.Printf("  %d were answered and %d were not. The second number is not a failure; it is the count of events nobody\n", tally.Asked, tally.Absent)
	fmt.Println("was asked about, and it is the number that stops an unanswered run reading as a clean one.")
	return nil
}

func printObservation(o store.Observation) {
	fmt.Printf("  %s\n", o.DetectionID)
	fmt.Printf("    ask        %s\n", o.AIState)
	fmt.Printf("    verdict    %s  (confidence %.2f, insufficient_evidence=%t)\n", o.Verdict, o.Confidence, o.InsufficientEvidence)
	if o.Summary != "" {
		fmt.Printf("    model said %q\n", trunc(o.Summary, 120))
	}
	fmt.Printf("    prompt     %s@%s\n", o.PromptID, o.PromptVersion)
	fmt.Printf("    contract   %s\n", o.ContractVersion)
	// The model identity is printed with the word CLAIM beside it, every time. It
	// is what the API said, and a reader who does not know that will read it as
	// something we established.
	fmt.Printf("    model      %s  (CLAIM by the provider; a hosted model has no weights to hash)\n", orNone(o.ModelIdentity, "not reported"))
	fmt.Printf("    temperature %.4f\n", o.Temperature)
	fmt.Printf("    output     %s\n", orNone(o.OutputHash, "none recorded"))
	if o.InputHashes != "" {
		fmt.Printf("    inputs     %s\n", o.InputHashes)
	}
	if o.LatencyMS > 0 {
		fmt.Printf("    latency    %dms  (prompt %d tokens, answer %d)\n", o.LatencyMS, o.PromptTokens, o.CompletionTokens)
	}
	if o.ReasonCode != "" && o.ReasonCode != "none" {
		fmt.Printf("    reason     %s  %s\n", o.ReasonCode, trunc(o.Detail, 100))
	}
}

func orNone(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
