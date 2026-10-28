// Command security-hafnia is the Milestone / Hafnia hackathon demo binary.
//
// It connects Milestone XProtect and Hafnia VLM for one shared test scenario:
// XProtect motion/analytics event → clip evidence → Hafnia VLM answer →
// Milestone bookmark on the camera timeline.
//
// Synthetic mode (default) runs entirely against fixtures and an httptest
// Hafnia server: no live Milestone, no network egress. All synthetic data is
// marked Simulated() = true and printed with a SIMULATED prefix; it is never
// presented as automatic detection.
//
// Live mode polls a configured Milestone, uses real video, and writes real
// bookmarks. All Milestone routes are Unverified, so live mode is opt-in and
// failures are named refusals.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/fixture"
	"github.com/Sergey-Bar/vision/internal/hafnia"
	"github.com/Sergey-Bar/vision/internal/integration"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// testPatternSource generates synthetic test-pattern clip bytes for an event.
type testPatternSource struct{}

func (s *testPatternSource) EvidenceFor(_ context.Context, event integration.Event) ([]byte, string, error) {
	data := []byte("SYNTHETIC-TEST-PATTERN-CLIP-" + event.EventID())
	return data, "video/mp4", nil
}

// stubBookmarkWriter records bookmark placements without reaching a vendor.
type stubBookmarkWriter struct {
	placed []milestone.Bookmark
}

func (s *stubBookmarkWriter) WriteBookmark(_ context.Context, b milestone.Bookmark) (milestone.BookmarkRef, error) {
	s.placed = append(s.placed, b)
	return milestone.BookmarkRef{ID: "bmk-stub-" + b.IdempotencyKey}, nil
}

// hafniaDoer rewrites the pinned hafnia host to the test server, mirroring the
// hafnia test transport so the client's own allow-list and redirect policy are
// not bypassed.
type hafniaDoer struct {
	base string
	do   func(*http.Request) (*http.Response, error)
}

func (d *hafniaDoer) Do(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(d.base, "http://")
	return d.do(req)
}

const (
	validAnswer = `{"model":"hafnia-8b","choices":[{"message":{"content":"{\"summary\":\"a person crossed the restricted zone\",\"verdict\":\"ALERT CONFIRMED\",\"confidence\":0.9}"}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "security-hafnia: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	synthetic := flag.Bool("synthetic", true, "run the synthetic demo (no live Milestone)")
	video := flag.String("video", "", "path to a real video clip (live mode)")
	milestoneBaseURL := flag.String("milestone-base-url", "", "Milestone API base URL (live mode)")
	milestoneToken := flag.String("milestone-token", "", "Milestone bearer token (live mode)")
	hafniaRegion := flag.String("hafnia-region", "eu", "Hafnia region (eu|us)")
	hafniaAPIKeyEnv := flag.String("hafnia-api-key-env", "SP_HAFNIA_API_KEY", "env var holding the Hafnia API key")
	promptTemplate := flag.String("prompt-template", "PROMPT VERBATIM FROM THE PROVIDER'S DOCUMENTATION", "Hafnia prompt template")
	outputVideo := flag.String("output-video", "", "path to write the demo video clip")
	if err := flag.CommandLine.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()

	var (
		adapter   *milestone.Adapter
		evSource  integration.EvidenceSource
		writer    milestone.BookmarkWriter
		simulated bool
	)

	if *synthetic {
		adapter, evSource, writer, simulated = syntheticSetup()
	} else {
		var err error
		adapter, evSource, writer, err = liveSetup(*milestoneBaseURL, *milestoneToken, *video)
		if err != nil {
			return err
		}
		simulated = false
	}

	hafniaClient, hafniaServer, err := hafniaSetup(*hafniaRegion, *hafniaAPIKeyEnv, *promptTemplate)
	if err != nil {
		return err
	}
	defer hafniaServer.Close()

	bridge := &integration.Bridge{
		Adapter:        adapter,
		Hafnia:         hafniaClient,
		TenantID:       "tenant-demo",
		SiteID:         "site-demo",
		ProfileID:      integration.GeneralMonitoringProfile,
		EvidenceSource: evSource,
		BookmarkWriter: writer,
		PreRoll:        10 * time.Second,
		PostRoll:       5 * time.Second,
	}

	results, err := bridge.Run(ctx)
	if err != nil {
		return fmt.Errorf("bridge: %w", err)
	}

	// Write the demo video clip if requested.
	if *outputVideo != "" && evSource != nil {
		if event, ok := firstEvent(results); ok {
			data, _, err := evSource.EvidenceFor(ctx, event)
			if err == nil && data != nil {
				if err := os.WriteFile(*outputVideo, data, 0o644); err != nil {
					return fmt.Errorf("write demo video: %w", err)
				}
			}
		}
	}

	// Print transcript.
	printTranscript(results, simulated)
	return nil
}

func syntheticSetup() (*milestone.Adapter, integration.EvidenceSource, milestone.BookmarkWriter, bool) {
	occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := integration.SimulatedRawRecord(
		"evt-motion-001", "xprotect-demo", "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		occurred, integration.EventTypeMotion, "Motion detected at entrance",
	)

	envelope, err := json.Marshal(map[string]any{
		"Id":        rec.SourceEventID,
		"Type":      rec.EventType,
		"Source":    rec.SourceDeviceRef,
		"Timestamp": rec.ObservedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		panic(err)
	}

	scenario := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "synthetic-motion",
		Source:        "synthetic",
		SiteID:        "site-demo",
		Epoch:         occurred,
		Events: []fixture.Event{
			{
				Kind:     milestone.APIPrefix + "/events",
				Method:   "GET",
				OffsetMS: 0,
				Envelope: envelope,
			},
		},
	}

	replay := milestone.NewReplayTransport([]fixture.Scenario{scenario})
	adapter := milestone.New(replay, "site-demo", "integration-hafnia")

	return adapter, &testPatternSource{}, &stubBookmarkWriter{}, true
}

func liveSetup(baseURL, token, videoPath string) (*milestone.Adapter, integration.EvidenceSource, milestone.BookmarkWriter, error) {
	transport, err := milestone.NewHTTPTransport(baseURL, token, 15*time.Second)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("milestone transport: %w", err)
	}
	adapter := milestone.New(transport, "", "integration-hafnia")

	var evSource integration.EvidenceSource = &fileEvidenceSource{path: videoPath}
	writer, ok := transport.(milestone.BookmarkWriter)
	if !ok {
		return nil, nil, nil, errors.New("milestone transport does not implement BookmarkWriter")
	}
	return adapter, evSource, writer, nil
}

type fileEvidenceSource struct{ path string }

func (s *fileEvidenceSource) EvidenceFor(_ context.Context, _ integration.Event) ([]byte, string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, "", err
	}
	return data, "video/mp4", nil
}

func hafniaSetup(region, apiKeyEnv, promptTemplate string) (*hafnia.Client, *httptest.Server, error) {
	r, err := hafnia.ParseRegion(region)
	if err != nil {
		return nil, nil, fmt.Errorf("hafnia region: %w", err)
	}
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		apiKey = "demo-key"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/assets" && req.Method == http.MethodPost:
			body, _ := io.ReadAll(req.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"id":"asset-demo","mediaType":"video/mp4","sizeBytes":%d}`, len(body))
		case req.URL.Path == "/chat/completions" && req.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, validAnswer)
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/assets/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	doer := &hafniaDoer{base: srv.URL, do: srv.Client().Do}
	cfg := hafnia.Config{
		Region:              r,
		APIKey:              apiKey,
		PromptTemplate:      promptTemplate,
		PromptVersion:       "v1",
		OutputSchemaVersion: 1,
		APIModelName:        "hafnia-8b",
		Timeout:             30 * time.Second,
		Transport:           doer,
	}
	client, err := hafnia.New(cfg)
	if err != nil {
		srv.Close()
		return nil, nil, fmt.Errorf("hafnia client: %w", err)
	}
	return client, srv, nil
}

func firstEvent(results []integration.BridgeResult) (integration.Event, bool) {
	for _, r := range results {
		if r.Event != nil {
			return r.Event, true
		}
	}
	return nil, false
}

func printTranscript(results []integration.BridgeResult, simulated bool) {
	prefix := ""
	if simulated {
		prefix = "SIMULATED "
	}
	fmt.Printf("%sMilestone / Hafnia integration transcript\n", prefix)
	fmt.Printf("%s==========================================\n", prefix)
	for i, r := range results {
		fmt.Printf("\n%s[%d] event=%s type=%s camera=%s occurred=%s\n",
			prefix, i, r.Event.EventID(), r.Event.EventType(), r.Event.LogicalCamera(),
			r.Event.OccurredAt().UTC().Format(time.RFC3339))
		if r.Event.Simulated() {
			fmt.Printf("%s   SIMULATED: synthetic/demo data, not automatic detection\n", prefix)
		}
		if r.Err != nil {
			fmt.Printf("%s   error: %v\n", prefix, r.Err)
			continue
		}
		res := r.Result
		if res.Degraded {
			fmt.Printf("%s   hafnia: DEGRADED reason=%s detail=%s\n", prefix,
				res.DegradedReason, res.DegradedDetail)
		} else {
			fmt.Printf("%s   hafnia: model=%s structured=%v insufficient=%v\n", prefix,
				res.Model, res.Structured, res.InsufficientEvidence)
		}
		switch {
		case r.BookmarkRefusal != "":
			fmt.Printf("%s   bookmark: REFUSED %s\n", prefix, r.BookmarkRefusal)
		case r.BookmarkRef != nil:
			fmt.Printf("%s   bookmark: placed id=%s\n", prefix, r.BookmarkRef.ID)
		default:
			fmt.Printf("%s   bookmark: not placed (no writer)\n", prefix)
		}
	}
	fmt.Printf("\n%sEnd of transcript.\n", prefix)
}
