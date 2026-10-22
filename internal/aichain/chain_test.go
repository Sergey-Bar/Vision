package aichain_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/aichain"
	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// The chain.
//
// The claim the chain makes is the one that matters most in this product: it
// NEVER fabricates an answer. A chain that exhausted its providers returns a
// DEGRADED result naming every provider's reason, and a chain that returned
// an empty answer would be reporting "there is nothing here" about a
// question nobody managed to ask.

// fake is a provider with a scripted answer.
type fake struct {
	name   string
	result airuntime.Result
	err    error
	calls  int
}

// Name identifies the provider.
func (f *fake) Name() string { return f.name }

// Do performs one inference.
func (f *fake) Do(_ context.Context, _ airuntime.Request) (airuntime.Result, error) {
	f.calls++
	if f.err != nil {
		return airuntime.Result{}, f.err
	}
	return f.result, nil
}

// anAnswer is a provider that answered.
func anAnswer() airuntime.Result {
	return airuntime.Result{Structured: map[string]any{"answer": "a person"}, Degraded: false}
}

// aRefusal is a provider that declined.
func aRefusal() airuntime.Result {
	return airuntime.Result{
		Degraded:       true,
		DegradedReason: observations.DegradationModelUnavailable,
		DegradedDetail: "the model is not loaded",
	}
}

// theRequest is one inference.
func theRequest() airuntime.Request {
	return airuntime.Request{ObservationID: "obs-1", ProfileID: "general-monitoring"}
}

func TestTheChainReturnsTheFirstAnswer(t *testing.T) {
	first := &fake{name: "first", result: anAnswer()}
	second := &fake{name: "second", result: anAnswer()}
	c := aichain.NewChain(aichain.Policy{}, []airuntime.Provider{first, second})

	res, err := c.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the chain failed: %v", err)
	}
	if res.Degraded {
		t.Fatalf("a chain with an answer degraded: %s", res.DegradedDetail)
	}
	if second.calls != 0 {
		t.Error("the chain asked the second provider after the first answered")
	}
}

func TestTheChainMovesOnWhenAProviderDeclines(t *testing.T) {
	// A provider that declines is not a failure of the CHAIN. It is one
	// provider saying "not me", and the chain's whole purpose is to have
	// somewhere else to go.
	first := &fake{name: "first", result: aRefusal()}
	second := &fake{name: "second", result: anAnswer()}
	c := aichain.NewChain(aichain.Policy{}, []airuntime.Provider{first, second})

	res, err := c.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the chain failed: %v", err)
	}
	if res.Degraded {
		t.Fatalf("the chain degraded although the second provider answered: %s", res.DegradedDetail)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Errorf("the chain called %d and %d providers, want one each", first.calls, second.calls)
	}
}

func TestTheChainMovesOnWhenAProviderFails(t *testing.T) {
	// A refused connection is a FAILURE and not a decline, and it does not
	// stop the chain either.
	first := &fake{name: "first", err: errors.New("connection refused")}
	second := &fake{name: "second", result: anAnswer()}
	c := aichain.NewChain(aichain.Policy{}, []airuntime.Provider{first, second})

	res, err := c.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the chain failed: %v", err)
	}
	if res.Degraded {
		t.Fatalf("the chain degraded although the second provider answered: %s", res.DegradedDetail)
	}
}

func TestAChainThatExhaustsItselfDegradesAndSaysWhy(t *testing.T) {
	// THE claim. A chain with nothing left reports that it had nothing left,
	// and it names every provider's reason. It does NOT return an empty
	// answer, because an empty answer reads as "there was nothing to see".
	first := &fake{name: "first", result: aRefusal()}
	second := &fake{name: "second", err: errors.New("connection refused")}
	c := aichain.NewChain(aichain.Policy{}, []airuntime.Provider{first, second})

	res, err := c.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the chain failed: %v", err)
	}
	if !res.Degraded {
		t.Fatal("a chain that exhausted itself returned an answer")
	}
	if res.Structured != nil {
		t.Errorf("a chain that exhausted itself returned a structured answer: %v", res.Structured)
	}
	// And it says WHY, for each provider, so that an operator is not left
	// with one reason and no idea which provider it belonged to.
	if !strings.Contains(res.DegradedDetail, "first") {
		t.Errorf("the detail does not name the first provider: %q", res.DegradedDetail)
	}
	if !strings.Contains(res.DegradedDetail, "second") {
		t.Errorf("the detail does not name the second provider: %q", res.DegradedDetail)
	}
	if !strings.Contains(res.DegradedDetail, "connection refused") {
		t.Errorf("the detail does not carry the failure: %q", res.DegradedDetail)
	}
}

func TestAChainWithNoProvidersIsRefused(t *testing.T) {
	c := aichain.NewChain(aichain.Policy{}, nil)
	_, err := c.Do(context.Background(), theRequest())
	if err == nil {
		t.Fatal("a chain with no providers answered")
	}
	if !errors.Is(err, aichain.ErrNoPolicy) {
		t.Errorf("the refusal is not the no-policy refusal: %v", err)
	}
}

func TestTheKindsAreAClosedSet(t *testing.T) {
	// A kind the build does not know is a chain that cannot run, and it is
	// named as one rather than skipped at call time.
	want := []string{"openai_compatible", "anthropic", "local_bundle"}
	if len(aichain.Kinds) != len(want) {
		t.Fatalf("the kinds are %v, want %v", aichain.Kinds, want)
	}
	for i, k := range want {
		if aichain.Kinds[i] != k {
			t.Errorf("kind %d is %q, want %q", i, aichain.Kinds[i], k)
		}
	}
}
