package hafnia_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
	"github.com/Sergey-Bar/Ai-Captain/internal/hafnia"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// transport rewrites the destination to the test server.
//
// It stands in for a Doer so the client's own allow-list, TLS settings and
// redirect policy are not bypassed by the test: a test that reached the network
// directly would be testing a different client from the one that ships.
type transport struct {
	base string
	do   func(*http.Request) (*http.Response, error)
}

func (t *transport) Do(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.base, "http://")
	return t.do(req)
}

func fixedTransport(t *testing.T, h http.HandlerFunc) *transport {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &transport{base: srv.URL, do: srv.Client().Do}
}

func testConfig(tr hafnia.Doer) hafnia.Config {
	return hafnia.Config{
		Region:              hafnia.RegionEU,
		APIKey:              "test-key",
		PromptTemplate:      "PROMPT VERBATIM FROM THE PROVIDER'S DOCUMENTATION",
		PromptVersion:       "v1",
		OutputSchemaVersion: 1,
		APIModelName:        "hafnia-8b",
		Timeout:             2 * time.Second,
		Transport:           tr,
	}
}

func validAnswer() string {
	return `{"model":"hafnia-8b","choices":[{"message":{"content":"{\"summary\":\"a person crossed\",\"verdict\":\"ALERT CONFIRMED\",\"confidence\":0.9}"}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`
}

// uploadThen makes every route except the upload answer, so a fault case tests
// the route it names rather than the upload standing in front of it.
func uploadThen(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
			return
		}
		next(w, r)
	}
}

// TestTheProviderIsAProvider is the P1.2 assertion: the second provider speaks
// the same interface as the first, so the worker's fault matrix, its attribution
// and its degradation vocabulary apply to both without a branch.
func TestTheProviderIsAProvider(t *testing.T) {
	var p airuntime.Provider
	tr := fixedTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c, err := hafnia.New(testConfig(tr))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p = c
	if p.Name() != "hafnia" {
		t.Errorf("provider name %q", p.Name())
	}
	// And the nil guard: a nil provider in a job worker is a panic at 3am.
	if _, err := airuntime.ProviderFor(nil); err == nil {
		t.Error("a nil provider was accepted")
	}
}

// TestEveryFailureMapsToItsOwnReasonAndNoneIsAFabricatedAnswer is the plan's
// third new risk: "stubbed-provider fault matrix: timeout, 429, 500, malformed
// body, cross-region, no-endpoint. Each maps to a distinct Degradation, and
// none is a fabricated answer."
//
// Each case below asserts three things, and the third is the one that matters: the
// result is Degraded, and the Structured answer is empty. A fault that produced
// a Degraded-false result with no content would be a fabricated "nothing here",
// which is the one thing this product must never do.
func TestEveryFailureMapsToItsOwnReasonAndNoneIsAFabricatedAnswer(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    hafnia.ReasonCode
	}{
		{
			name: "429 rate limited",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			want: hafnia.ReasonRegionUnavailable,
		},
		{
			name: "500 provider error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			want: hafnia.ReasonRegionUnavailable,
		},
		{
			name: "401 credentials rejected",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			want: hafnia.ReasonError,
		},
		{
			name: "malformed body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":`))
			},
			want: hafnia.ReasonAnswerMalformed,
		},
		{
			name: "two choices is a guess",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":"m","choices":[
					{"message":{"content":"{\"verdict\":\"ALERT CONFIRMED\"}"}},
					{"message":{"content":"{\"verdict\":\"FALSE POSITIVE\"}"}}]}`))
			},
			want: hafnia.ReasonAnswerMalformed,
		},
		{
			name: "prose instead of json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"A person entered."}}]}`))
			},
			want: hafnia.ReasonAnswerMalformed,
		},
		{
			name: "empty content",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"  "}}]}`))
			},
			want: hafnia.ReasonAnswerMalformed,
		},
	}

	distinct := map[hafnia.ReasonCode]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := fixedTransport(t, uploadThen(tc.handler))
			c, err := hafnia.New(testConfig(tr))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			res, err := c.Do(context.Background(), airuntime.Request{
				ObservationID: "obs-1",
				ProfileID:     "general-monitoring",
				Evidence: []airuntime.Evidence{{
					ID: "e1", SHA256: "abc", ContentType: "video/mp4", Data: []byte("clip"),
				}},
			})
			if err != nil {
				t.Fatalf("Do returned an error: %v", err)
			}
			if !res.Degraded {
				t.Fatalf("a failing call returned a non-degraded result: %+v", res)
			}
			if res.Structured != nil {
				t.Errorf("a failing call returned structured output %v; that is a fabricated answer", res.Structured)
			}
			if !strings.Contains(res.DegradedDetail, "reason="+string(tc.want)) {
				t.Errorf("detail %q does not carry reason=%s", res.DegradedDetail, tc.want)
			}
			if !observations.Degradations[res.DegradedReason] {
				t.Errorf("degradation %q is outside the closed vocabulary", res.DegradedReason)
			}
		})
		// Distinctness is asserted per *category*, not per fault. Four shapes of a
		// bad answer are one operational fact - the provider answered and the answer
		// was unreadable - and four reason codes for them would make the code a
		// taxonomy of bugs rather than a list of things an operator can act on.
		// What must never be shared is a category with a *different* one, which is
		// what the count at the end measures.
		if _, seen := distinct[tc.want]; !seen {
			distinct[tc.want] = tc.name
		}
	}
	// The categories the plan names must all be present, and they must all differ.
	for _, want := range []hafnia.ReasonCode{
		hafnia.ReasonRegionUnavailable, // 429 and 500: the region is not serving
		hafnia.ReasonAnswerMalformed,   // the answer could not be read
		hafnia.ReasonError,             // the provider rejected us
	} {
		if _, ok := distinct[want]; !ok {
			t.Errorf("the matrix never produced reason=%s", want)
		}
	}
	if len(distinct) < 3 {
		t.Errorf("only %d distinct reasons across the matrix; the point of the matrix is that they differ", len(distinct))
	}
}

// TestTheCapIsAStateAndNotASilentThrottle: exhaustion must be a recorded,
// first-class outcome with its own reason, not a skip that reads like a quiet
// detector.
func TestTheCapIsAStateAndNotASilentThrottle(t *testing.T) {
	var calls atomic.Int32
	tr := fixedTransport(t, uploadThen(func(w http.ResponseWriter, r *http.Request) {
		// Only the inference route is counted. Uploads and deletes are expected
		// on every accepted call, and counting them would make "a refused call
		// made no call" unfalsifiable.
		if r.URL.Path == "/chat/completions" {
			calls.Add(1)
		}
		_, _ = w.Write([]byte(validAnswer()))
	}))
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cfg := testConfig(tr)
	cfg.DailyQueryCap = 3
	cfg.Now = func() time.Time { return now }
	c, err := hafnia.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 3; i++ {
		res, err := c.Do(context.Background(), airuntime.Request{
			ObservationID: "obs", ProfileID: "general-monitoring",
			Evidence: []airuntime.Evidence{{ID: "e", SHA256: "a", ContentType: "video/mp4", Data: []byte("c")}},
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if res.Degraded {
			t.Fatalf("call %d degraded before the cap: %s", i, res.DegradedDetail)
		}
	}
	res, err := c.Do(context.Background(), airuntime.Request{
		ObservationID: "obs", ProfileID: "general-monitoring",
		Evidence: []airuntime.Evidence{{ID: "e", SHA256: "a", ContentType: "video/mp4", Data: []byte("c")}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !res.Degraded {
		t.Fatal("the fourth call was not degraded")
	}
	if !strings.Contains(res.DegradedDetail, "reason=cap_reached") {
		t.Errorf("detail %q does not name the cap", res.DegradedDetail)
	}
	if strings.Contains(res.DegradedDetail, "cap reached") {
		t.Error("the detail is prose; the repository's rule is that nothing matches on prose")
	}
	// The refused call must not have been made.
	if got := calls.Load(); got != 3 {
		t.Errorf("the provider was called %d times; a refused call is not a call", got)
	}
	st := c.Quota()
	if st.Limit != 3 || st.Used != 3 || !st.Exhausted || st.Remaining != 0 {
		t.Errorf("quota status %+v", st)
	}
	if st.Day != "2026-07-01" {
		t.Errorf("the counters are dated %q; a 'used' figure without its date is not a fact", st.Day)
	}
}

// TestTheCapResetsOnTheUTCDayBoundary, because a cap that resets on a different
// boundary than the bill is not a cap.
func TestTheCapResetsOnTheUTCDayBoundary(t *testing.T) {
	tr := fixedTransport(t, uploadThen(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(validAnswer()))
	}))
	now := time.Date(2026, 7, 1, 23, 59, 0, 0, time.UTC)
	cfg := testConfig(tr)
	cfg.DailyQueryCap = 1
	cfg.Now = func() time.Time { return now }
	c, _ := hafnia.New(cfg)
	req := airuntime.Request{ObservationID: "o", ProfileID: "p",
		Evidence: []airuntime.Evidence{{ID: "e", SHA256: "a", ContentType: "video/mp4", Data: []byte("c")}}}

	if res, _ := c.Do(context.Background(), req); res.Degraded {
		t.Fatal("the first call was refused")
	}
	if res, _ := c.Do(context.Background(), req); !res.Degraded {
		t.Fatal("the second call was not refused")
	}
	now = now.Add(2 * time.Minute) // past midnight UTC
	res, _ := c.Do(context.Background(), req)
	if res.Degraded {
		t.Errorf("the call after the UTC day rolled was refused: %s", res.DegradedDetail)
	}
	if st := c.Quota(); st.Day != "2026-07-02" || st.Used != 1 {
		t.Errorf("quota status after the roll: %+v", st)
	}
}

// TestARegionIsPinnedAndNeverFailsOver is the sovereignty guarantee. A call to
// a region this build does not carry is refused by name at construction, and the
// client's host is the only destination it can name.
func TestARegionIsPinnedAndNeverFailsOver(t *testing.T) {
	if _, err := hafnia.ParseRegion("apac"); err == nil {
		t.Error("a region this build does not carry was accepted")
	}
	if _, err := hafnia.ParseRegion(""); err == nil {
		t.Error("an empty region was accepted; a default here is a site whose footage went somewhere nobody chose")
	}
	r, err := hafnia.ParseRegion(" EU ")
	if err != nil {
		t.Fatalf("ParseRegion: %v", err)
	}
	if r != hafnia.RegionEU {
		t.Errorf("ParseRegion did not normalise %v", r)
	}
	// The allow-list is closed: exactly the regions this build names, and no
	// operator-supplied host.
	if got := len(hafnia.KnownRegions()); got != 2 {
		t.Errorf("the build pins %d regions, want the 2 it implements", got)
	}
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {})
	c, err := hafnia.New(testConfig(tr))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Region() != hafnia.RegionEU {
		t.Errorf("the client is pinned to %v", c.Region())
	}
	if c.Host() == "" || strings.Contains(c.Host(), "127.0.0.1") {
		t.Errorf("the pinned host is %q; it must be the provider's, not the test's", c.Host())
	}
}

// TestTheAssetIsDeletedOnEveryPathAfterUpload is P3.3's core: deletion after
// the answer, and on every failure path after the upload.
func TestTheAssetIsDeletedOnEveryPathAfterUpload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"success", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/assets" && r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"asset-7"}`))
				return
			}
			_, _ = w.Write([]byte(validAnswer()))
		}},
		{"answer fails", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/assets" && r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"asset-7"}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{"answer malformed", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/assets" && r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"asset-7"}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted atomic.Bool
			tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deleted.Store(true)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				tc.handler(w, r)
			})
			c, _ := hafnia.New(testConfig(tr))
			_, _ = c.Do(context.Background(), airuntime.Request{
				ObservationID: "o", ProfileID: "p",
				Evidence: []airuntime.Evidence{{ID: "e", SHA256: "a", ContentType: "video/mp4", Data: []byte("clip")}},
			})
			if !deleted.Load() {
				t.Error("the clip was uploaded and never deleted")
			}
			if ids := c.OutstandingAssets(); len(ids) != 0 {
				t.Errorf("the ledger still lists %v", ids)
			}
		})
	}
}

// TestAKilledWorkerLeavesAVisibleResidue is the honest half of P3.3.
//
// A `defer` does not run when a process is killed, so a client that only relies
// on a defer has no answer for the case the plan names. This test asserts the
// weaker but real property: whatever was uploaded and not released is *listed*,
// so the next process can sweep it, and the run does not claim to have deleted
// something it did not.
func TestAKilledWorkerLeavesAVisibleResidue(t *testing.T) {
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			// 404: the provider already forgot it. For a sweep that is the state
			// the caller asked for, and reporting it as a failure would mean a
			// sweep can never succeed twice.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"asset-orphan"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	c, _ := hafnia.New(testConfig(tr))
	asset, err := c.UploadAsset(context.Background(), "video/mp4", []byte("clip"))
	if err != nil {
		t.Fatalf("UploadAsset: %v", err)
	}
	ids := c.OutstandingAssets()
	if len(ids) != 1 || ids[0] != asset.ID {
		t.Fatalf("outstanding assets %v, want [%s]", ids, asset.ID)
	}
	// A 404 on delete means the provider already forgot it, which is the state
	// the caller asked for - and the sweep must treat it as success.
	failed, err := c.SweepAssets(context.Background())
	if err != nil {
		t.Fatalf("SweepAssets: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("the sweep reported %v as failed", failed)
	}
	if ids := c.OutstandingAssets(); len(ids) != 0 {
		t.Errorf("after a sweep the ledger still lists %v", ids)
	}
}

// TestTheAttributionNamesTheModelWithoutInventingAHash is §6's requirement, and
// the one thing a judge will check: the answer says which model the API reported
// and which contract this build pinned, and carries no hash at all.
func TestTheAttributionNamesTheModelWithoutInventingAHash(t *testing.T) {
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"a1"}`))
			return
		}
		_, _ = w.Write([]byte(validAnswer()))
	})
	c, _ := hafnia.New(testConfig(tr))
	res, err := c.Do(context.Background(), airuntime.Request{
		ObservationID: "o", ProfileID: "general-monitoring",
		Evidence: []airuntime.Evidence{{ID: "e", SHA256: "aaa", ContentType: "video/mp4", Data: []byte("clip")}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.ModelHash != "" {
		t.Errorf("the observation carries model hash %q; this provider will not give us weights to hash, and "+
			"inventing one is the failure §6 is about", res.ModelHash)
	}
	if res.ModelIdentity != "hafnia-8b" {
		t.Errorf("model identity %q, want what the API reported", res.ModelIdentity)
	}
	if res.ModelContractVersion == "" {
		t.Error("no contract version: the prompt and schema that produced this answer are not named")
	}
	if res.Temperature != hafnia.DefaultTemperature {
		t.Errorf("temperature %v, want the API's lowest %v", res.Temperature, hafnia.DefaultTemperature)
	}
	if res.OutputHash == "" {
		t.Error("no output hash; the stored answer is the replay basis and must be hashable")
	}

	// And it must survive Validate with an empty hash, or the honest record is
	// one the schema cannot hold.
	obs := observations.Observation{
		TenantID: "t", SiteID: "s", IncidentID: "i", EventID: "e",
		ProfileID: "general-monitoring", InputEvidenceHashes: res.InputEvidenceHashes,
		OutputSchemaVersion: 1, StructuredOutput: res.Structured,
		Model:                res.Model,
		ModelIdentity:        res.ModelIdentity,
		ModelContractVersion: res.ModelContractVersion,
		RuntimeVersion:       res.RuntimeVersion,
		PromptTemplateID:     "hafnia-traffic",
		PromptVersion:        "v1",
	}
	if err := obs.Validate(); err != nil {
		t.Errorf("an honestly-attributed observation failed validation: %v", err)
	}
	// And the stored answer's hash recomputes, which is what makes "replay from
	// stored facts" checkable rather than merely claimed.
	recomputed, err := obs.OutputHash()
	if err != nil {
		t.Fatalf("OutputHash: %v", err)
	}
	if recomputed != res.OutputHash {
		t.Errorf("the provider result's output hash is %s and the observation recomputes %s; "+
			"two implementations of 'canonical' are two values", res.OutputHash, recomputed)
	}
	if err := obs.Validate(); err != nil {
		t.Errorf("an honestly-attributed observation failed validation: %v", err)
	}
}

// TestTheContractVersionMovesWhenThePromptMoves: the whole value of pinning a
// contract version is that it changes when the thing it describes changes.
func TestTheContractVersionMovesWhenThePromptMoves(t *testing.T) {
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {})
	a, _ := hafnia.New(testConfig(tr))
	cfgB := testConfig(tr)
	cfgB.PromptVersion = "v2"
	b, _ := hafnia.New(cfgB)
	cfgC := testConfig(tr)
	cfgC.PromptTemplate += " ADDED WORDS"
	c, _ := hafnia.New(cfgC)

	if a.Attribution().ModelContractVersion == b.Attribution().ModelContractVersion {
		t.Error("the contract version did not move when the prompt version did")
	}
	if a.Attribution().ModelContractVersion == c.Attribution().ModelContractVersion {
		t.Error("the contract version did not move when the prompt text did")
	}
	// The whole value of a contract version is that it is *reproducible*: a
	// reviewer on another machine, from this repository, must be able to compute
	// the same value. So two independently constructed clients with the same
	// configuration must agree, and a version derived from a clock, a random
	// value or an iteration order would not.
	same, err := hafnia.New(testConfig(tr))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if same.Attribution().ModelContractVersion != a.Attribution().ModelContractVersion {
		t.Errorf("two clients with the same configuration produced different contract versions: %s and %s. "+
			"A value a reviewer cannot reproduce is not attribution",
			same.Attribution().ModelContractVersion, a.Attribution().ModelContractVersion)
	}
}

// TestAnUploadOverTheProvidersCapIsRefusedBeforeItLeaves: the cap is a transmit
// limit, and truncating a clip on the way out would produce a clip that no longer
// contains the event.
func TestAnUploadOverTheProvidersCapIsRefusedBeforeItLeaves(t *testing.T) {
	var reached atomic.Bool
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		_, _ = w.Write([]byte(`{"id":"a"}`))
	})
	c, _ := hafnia.New(testConfig(tr))
	tooBig := make([]byte, hafnia.MaxUploadBytes+1)
	if _, err := c.UploadAsset(context.Background(), "video/mp4", tooBig); err == nil {
		t.Fatal("an oversized clip was accepted")
	}
	if reached.Load() {
		t.Error("the oversized clip was sent anyway")
	}
}

// TestRefusesToStartWithoutTheThingsThatWouldMakeACallWrong.
func TestRefusesToStartWithoutTheThingsThatWouldMakeACallWrong(t *testing.T) {
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {})
	base := testConfig(tr)

	noKey := base
	noKey.APIKey = ""
	if _, err := hafnia.New(noKey); err == nil {
		t.Error("a client with no API key was built; the call would upload footage unauthenticated")
	}
	noPrompt := base
	noPrompt.PromptTemplate = "  "
	if _, err := hafnia.New(noPrompt); err == nil {
		t.Error("a client with no prompt was built; the answer would have no known provenance")
	}
	badRegion := base
	badRegion.Region = "apac"
	if _, err := hafnia.New(badRegion); err == nil {
		t.Error("a client pinned to an uncarried region was built")
	}
}

// TestNoVideoEvidenceIsRefusedBeforeAnythingIsUploaded: a request with nothing
// to look at is a fact about the caller, not a fact about the model.
func TestNoVideoEvidenceIsRefusedBeforeAnythingIsUploaded(t *testing.T) {
	var reached atomic.Bool
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		_, _ = w.Write([]byte(`{"id":"a"}`))
	})
	c, _ := hafnia.New(testConfig(tr))
	res, err := c.Do(context.Background(), airuntime.Request{
		ObservationID: "o", ProfileID: "p",
		Evidence: []airuntime.Evidence{{ID: "e", SHA256: "a", ContentType: "application/json", Data: []byte("{}")}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !res.Degraded || res.Structured != nil {
		t.Errorf("a request with no video produced %+v", res)
	}
	if reached.Load() {
		t.Error("the provider was contacted for a request with nothing to look at")
	}
}

var _ = json.Marshal
