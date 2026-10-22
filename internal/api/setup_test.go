package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Sergey-Bar/vision/internal/api"
)

// The setup path.
//
// The claim the page makes is a promise about the FIRST day: an operator can
// install this, and the product will tell them what is left to do. The tests
// below are about that promise being kept:
//
//   - every step is ANSWERED, not guessed;
//   - an unfinished step says what to do next, because a step that is not
//     done and does not say what to do is a complaint;
//   - the steps are ordered, and `next` is the first unfinished one.

// step is one step of the setup path.
type setupStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Done   bool   `json:"done"`
	Detail string `json:"detail"`
	Action string `json:"action"`
}

// setup is the whole answer.
type setup struct {
	SiteID     string      `json:"siteId"`
	Steps      []setupStep `json:"steps"`
	DoneCount  int         `json:"doneCount"`
	TotalCount int         `json:"totalCount"`
	Ready      bool        `json:"ready"`
	Next       string      `json:"next"`
}

// readSetup asks the product what is left to do.
func readSetup(t *testing.T, s *api.Server) setup {
	t.Helper()
	rec := call(t, s, http.MethodGet, "/v1/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the setup path answered %d: %s", rec.Code, rec.Body.String())
	}
	var out setup
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestTheSetupPathNamesEveryStep(t *testing.T) {
	// The steps are the product's OWN claims, and a step the product
	// cannot answer is a step it does not have.
	s, _, _ := server(t)
	out := readSetup(t, s)
	if len(out.Steps) == 0 {
		t.Fatal("the setup path named no steps")
	}
	want := []string{"database", "schema", "site", "devices", "recorder", "chain"}
	for _, id := range want {
		found := false
		for _, step := range out.Steps {
			if step.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("the setup path does not name the %q step", id)
		}
	}
	if out.TotalCount != len(out.Steps) {
		t.Errorf("the path claims %d step(s) and names %d", out.TotalCount, len(out.Steps))
	}
}

func TestEveryUnfinishedStepSaysWhatToDoNext(t *testing.T) {
	// A step that is not done and does not say what to do is a complaint.
	// The operator is told what is missing and then left there.
	s, _, _ := server(t)
	out := readSetup(t, s)
	for _, step := range out.Steps {
		if step.Done {
			continue
		}
		if step.Action == "" {
			t.Errorf("the %q step is not done and says nothing about what to do", step.ID)
		}
		if step.Detail == "" {
			t.Errorf("the %q step is not done and does not say what the product found", step.ID)
		}
	}
}

func TestTheNextStepIsTheFirstUnfinishedOne(t *testing.T) {
	// The steps are ordered by dependency, so the first unfinished one is
	// the one blocking the rest. Reporting the LAST unfinished one would
	// send the operator to the wrong step.
	s, _, _ := server(t)
	out := readSetup(t, s)
	first := ""
	for _, step := range out.Steps {
		if !step.Done {
			first = step.ID
			break
		}
	}
	if first == "" {
		if out.Next != "" {
			t.Errorf("the path names %q as next and every step is done", out.Next)
		}
		return
	}
	if out.Next != first {
		t.Errorf("the path names %q as next, want %q", out.Next, first)
	}
}

func TestTheReadyVerdictIsAClaimAboutTheProduct(t *testing.T) {
	// "ready" says the product can record. It is not a claim about the
	// console and it is not a claim about the browser.
	s, _, _ := server(t)
	out := readSetup(t, s)
	if out.Ready != (out.DoneCount == out.TotalCount) {
		t.Errorf("the path says ready=%v with %d of %d step(s) done",
			out.Ready, out.DoneCount, out.TotalCount)
	}
}
