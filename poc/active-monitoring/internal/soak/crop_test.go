package soak

// The crop pass's POLICY, tested without pixels a model can see.
//
// The tests here are about which crops are proposed, which are refused, and what is
// counted when they are. None of them needs a session, a model or a licence, because
// the policy is arithmetic over two answers, the proposer's regions and the whole-frame
// pass's boxes, and the point of separating it from the geometry is exactly that it can
// be tested on its own.

import (
	"testing"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

// stubProposer offers whatever regions the test hands it, and reports the score it was
// given. It satisfies soak.RegionProposer and nothing else, which is the point: the
// crop pass's dependency on the novelty engine is two methods and this is all of it.
type stubProposer struct {
	regions []novelty.Candidate
	score   novelty.Score
	calls   int
}

func (s *stubProposer) Candidates(rgb []byte, w, h int) ([]novelty.Candidate, novelty.Score) {
	s.calls++
	return s.regions, s.score
}

func (s *stubProposer) Last() novelty.Score { return s.score }

func region(class string, x1, y1, x2, y2 float64) novelty.Candidate {
	return novelty.Candidate{Box: novelty.Box{Class: class, Score: 0.9, X1: x1, Y1: y1, X2: x2, Y2: y2}}
}

// aFrame is a 1920x1080 frame of no particular content. The crop pass never looks at
// the pixels itself, it hands them to the proposer, which the test stubs, so the content
// is irrelevant and only the DIMENSIONS matter.
func aFrame() frameSource {
	return frameSource{Seq: 1, RGB: make([]byte, 1920*1080*3), Width: 1920, Height: 1080}
}

func passWith(p RegionProposer) CropPass {
	return CropPass{Enabled: true, Proposer: p, MinArea: 0.0004, Pad: 0.35, MaxRegions: 4, CropMinFrames: 2}
}

// TestACropOverlappingAWholeFrameDetectionIsNotInferredTwice is the control the
// proposal names, and the one that decides whether the crop pass is affordable.
//
// The whole-frame pass has already looked at those pixels, at the model's resolution
// over the whole scene. A second inference on a crop containing that car costs about
// the same as the first, because the crop is letterboxed to the same 416x416, and
// returns the same car. So a crop overlapping a detection is refused, and the refusal
// is COUNTED rather than being silent: a gate with no counter is a gate whose cost
// nobody can measure.
func TestACropOverlappingAWholeFrameDetectionIsNotInferredTwice(t *testing.T) {
	// A car the whole-frame pass found, on the left of the frame.
	car := ai.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.40, X2: 0.25, Y2: 0.60}
	// The proposer offers the region the car occupies, and one far away from it.
	p := &stubProposer{regions: []novelty.Candidate{
		region("motion", 0.11, 0.42, 0.24, 0.58), // overlaps the car
		region("motion", 0.70, 0.70, 0.76, 0.76), // a bird-sized speck, far away
	}}
	got, ct := proposeCrops(aFrame(), []ai.Box{car}, passWith(p))

	if len(got) != 1 {
		t.Fatalf("proposed %d crop(s), want 1. The region over the car must be refused: the whole-frame "+
			"pass already answered those pixels at the model's own resolution over the whole scene, and a "+
			"crop of them costs a full inference to find the same car", len(got))
	}
	if ct.SkippedOverlap != 1 {
		t.Errorf("SkippedOverlap = %d, want 1. A refusal with no counter is a filter whose effect nobody "+
			"can measure, and the cost of the crop pass is the number a reviewer will ask for", ct.SkippedOverlap)
	}
	if ct.Inferred != 1 {
		t.Errorf("Inferred = %d, want 1", ct.Inferred)
	}
	if ct.Proposed != 2 {
		t.Errorf("Proposed = %d, want 2: the number the proposer offered", ct.Proposed)
	}
	// And the one that survived is the far one, which is the assertion that the gate
	// filtered by POSITION rather than by count.
	if got[0].rect.X < 1000 {
		t.Fatalf("the surviving crop is at x=%d; the gate skipped one region and kept the other, so it must "+
			"be the one far from the car at x=192", got[0].rect.X)
	}
}

// TestACropBesideADetectionIsStillInferred: the mirror of the control above, and the
// one that stops the gate being over-broad.
//
// The whole-frame pass found a car in the middle of the frame and the novelty engine
// proposes a region immediately beside it. Those regions share NO pixels, so the crop
// still has something to measure and inferring it is correct. A gate using an inclusive
// overlap would drop it, and every crop along the edge of every large detection would go
// unmeasured.
func TestACropBesideADetectionIsStillInferred(t *testing.T) {
	car := ai.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.40, X2: 0.30, Y2: 0.60}
	// Abutting the car's right edge exactly: shares no pixels, touches at x=0.30.
	p := &stubProposer{regions: []novelty.Candidate{region("motion", 0.30, 0.40, 0.36, 0.60)}}
	got, ct := proposeCrops(aFrame(), []ai.Box{car}, passWith(p))
	if len(got) != 1 || ct.SkippedOverlap != 0 {
		t.Fatalf("a region abutting the car's edge proposed %d crop(s) with %d skipped as overlapping. "+
			"Those regions share no pixels, so the crop still has something the whole-frame pass has not "+
			"answered and dropping it loses a real detection", len(got), ct.SkippedOverlap)
	}
}

// TestTheSameObjectIsReportedOnceWhenBothPassesFindIt: the union's deduplication, and
// which box wins.
//
// The whole-frame box wins. It was measured over the whole scene, which is the frame
// every other part of the system is calibrated against, so the crop pass can only ADD
// what the whole-frame pass missed and never move what it found. A union that preferred
// the crop's tighter box would quietly change the box on every detection both passes
// saw, and the tracker's history would hold two slightly different boxes for one object.
func TestTheSameObjectIsReportedOnceWhenBothPassesFindIt(t *testing.T) {
	whole := ai.Box{Class: "car", Score: 0.90, X1: 0.10, Y1: 0.40, X2: 0.30, Y2: 0.60}
	// The same car through a crop: tighter and slightly offset, because the crop pass
	// sees more of it and less of everything else.
	cut := ai.Box{Class: "car", Score: 0.88, X1: 0.11, Y1: 0.41, X2: 0.31, Y2: 0.61}
	// And a bird the whole-frame pass could not see at all.
	bird := ai.Box{Class: "bird", Score: 0.71, X1: 0.72, Y1: 0.72, X2: 0.76, Y2: 0.76}

	out, kept, deduped := unionBoxes([]ai.Box{whole}, []ai.Box{cut, bird})
	if len(out) != 2 {
		t.Fatalf("the union has %d box(es), want 2: the car once and the bird once", len(out))
	}
	if out[0] != whole {
		t.Fatalf("the union's first box is %+v, want the WHOLE-FRAME box %+v. Preferring the crop's "+
			"tighter measurement would change the box on every detection both passes saw, and nothing "+
			"in the output would say so", out[0], whole)
	}
	if out[1] != bird {
		t.Fatalf("the union's second box is %+v, want the bird %+v: the crop pass must be able to ADD "+
			"what the whole-frame pass missed, which is the only reason it costs anything", out[1], bird)
	}
	if deduped != 1 {
		t.Errorf("DedupedAway = %d, want 1: the duplicate car was dropped and the fact it was dropped is "+
			"counted, because a disagreement between two passes about one object is worth printing", deduped)
	}
	if kept != 2 {
		t.Errorf("kept = %d, want 2", kept)
	}
}

// TestTheRateGateSkipsTheProposerEntirely is the control on the rate gate's position.
//
// The gate has to come BEFORE the novelty engine. A frame the gate skipped must cost
// nothing, and calling the engine and then discarding its answer would spend the whole
// cost the gate exists to avoid — while leaving the proposer one call per frame, which
// is exactly what the operator who set the rate asked not to happen.
func TestTheRateGateSkipsTheProposerEntirely(t *testing.T) {
	p := &stubProposer{regions: []novelty.Candidate{region("motion", 0.7, 0.7, 0.76, 0.76)}}
	cp := passWith(p)
	// One pass per 200 ms, and frames arriving 40 ms apart at 25 fps.
	cp.Gate = novelty.NewGate(5, 25)
	const frameNS = int64(40_000_000)

	before := p.calls
	for i := 0; i < 25; i++ { // one second
		proposeCrops(frameSource{
			Seq: uint64(i), RGB: make([]byte, 1920*1080*3), Width: 1920, Height: 1080,
			AtNS: int64(i) * frameNS,
		}, nil, cp)
	}
	calls := p.calls - before
	ran, skipped := cp.Gate.Counts()
	if calls != int(ran) {
		t.Fatalf("the novelty proposer was called %d time(s) over 25 frames while the gate ran %d pass(es) "+
			"and skipped %d. The gate has to come BEFORE the proposer or the rate saves nothing",
			calls, ran, skipped)
	}
	if skipped == 0 {
		t.Fatal("the gate skipped no frames over one second at 5/s, so nothing was tested")
	}
}

// TestAGatedFrameProducesNothingAndCountsIt is the honest half: a skipped frame yields
// no crops AND says so.
//
// The counter is what makes a rate that is too low diagnosable. A gate that skipped
// everything and reported zero would be indistinguishable from a detector that cannot
// see small objects, which is the fault the crop pass exists to remove.
func TestAGatedFrameProducesNothingAndCountsIt(t *testing.T) {
	p := &stubProposer{regions: []novelty.Candidate{region("motion", 0.7, 0.7, 0.76, 0.76)}}
	cp := passWith(p)
	cp.Gate = novelty.NewGate(1, 25) // one pass per second
	// The first frame runs; the next twenty-four are inside the interval.
	got, ct := proposeCrops(frameSource{
		Seq: 0, RGB: make([]byte, 1920*1080*3), Width: 1920, Height: 1080, AtNS: 0,
	}, nil, cp)
	if len(got) != 1 || ct.Inferred != 1 {
		t.Fatalf("the first frame produced %d crop(s); a gate must fire on the first frame", len(got))
	}
	for i := 1; i < 25; i++ {
		_, ct = proposeCrops(frameSource{
			Seq: uint64(i), RGB: make([]byte, 1920*1080*3), Width: 1920, Height: 1080,
			AtNS: int64(i) * 40_000_000,
		}, nil, cp)
		if ct.GatedOut != 1 {
			t.Fatalf("frame %d counted %d gated-out, want 1", i, ct.GatedOut)
		}
		if ct.Inferred != 0 || ct.Proposed != 0 {
			t.Fatalf("a gated frame still counted %d proposed and %d inferred. A skipped frame must cost "+
				"nothing and must say so", ct.Proposed, ct.Inferred)
		}
	}
	// And the run total carries the count, so the report can print it. The gate is
	// RESET first: it already fired on frame 0 above, so without a reset the whole
	// second pass would be gated out and the total would be 25 rather than 24. That is
	// the arithmetic a real run gets, because a real run starts from a fresh gate.
	cp.Gate.Reset()
	var total CropCounters
	for i := 0; i < 25; i++ {
		_, ct := proposeCrops(frameSource{
			Seq: uint64(i), RGB: make([]byte, 1920*1080*3), Width: 1920, Height: 1080,
			AtNS: int64(i) * 40_000_000,
		}, nil, cp)
		total = total.Add(ct)
	}
	if total.GatedOut != 24 {
		t.Fatalf("the run total counted %d gated-out frames, want 24", total.GatedOut)
	}
	if total.Inferred != 1 {
		t.Fatalf("the run total counted %d inferences, want 1", total.Inferred)
	}
}

// TestAViewChangeSuppressesPresenceAndSaysHowManyFramesItCost is 0.6.4's orchestration
// control, and it is the end-to-end statement of the rule.
//
// A camera being moved produces an enormous novelty spike, which produces tracks, which
// produce events, which read as "everything passed at once". The camera-integrity event
// is correct AND the system is simultaneously reporting a storm of false presence, which
// is worse than reacting to nothing.
//
// So the crop gate must DISCARD its regions on a moved frame, and the run must COUNT how
// many frames it spent suppressing, because a gate that reports nothing is silence and
// silence reads as "nothing happened".
func TestAViewChangeSuppressesPresenceAndSaysHowManyFramesItCost(t *testing.T) {
	// The arbitration is asserted on the ORCHESTRATION, not on a proposer that reports a
	// moved view. A stub proposer cannot produce a novelty SCORE, and the score is what
	// the view detector reads, so driving the whole path from a stub would be asserting a
	// fixture rather than the rule.
	//
	// What is under test here is the decision the crop gate makes once it HAS a verdict:
	// count the change, throw the regions away, and reopen afterwards.
	if _, discard := applyViewVerdict(CropCounters{}, novelty.ViewStable); discard {
		t.Error("a stable frame discarded its regions")
	}
	if _, discard := applyViewVerdict(CropCounters{}, novelty.ViewChanged); !discard {
		t.Error("a frame that declared a view change kept its regions. A crop taken from a frame whose " +
			"camera is mid-pan describes a position the camera was pointing at for one frame")
	}
	if _, discard := applyViewVerdict(CropCounters{}, novelty.ViewSettling); !discard {
		t.Error("a settling frame kept its regions")
	}

	// And the counters, because a gate that reports nothing is silence and silence reads
	// as "nothing happened".
	var total CropCounters
	for _, v := range []novelty.ViewVerdict{
		novelty.ViewStable, novelty.ViewChanged, novelty.ViewSettling, novelty.ViewSettling,
	} {
		var discard bool
		total, discard = applyViewVerdict(total, v)
		if discard && v == novelty.ViewStable {
			t.Error("the stable frame discarded its work")
		}
	}
	if total.ViewChanges != 1 {
		t.Errorf("ViewChanges = %d, want 1. The camera-integrity event and this counter have to agree, "+
			"or the report says the camera moved and the run did not react", total.ViewChanges)
	}
	if total.ViewSettling != 2 {
		t.Errorf("ViewSettling = %d, want 2. Suppression that is not counted is silence", total.ViewSettling)
	}
	// And the counts survive being folded into a run total, which is how they reach the
	// report.
	var run CropCounters
	run = run.Add(total)
	if run.ViewChanges != 1 || run.ViewSettling != 2 {
		t.Errorf("the run total lost the view counts: %+v", run)
	}
}

// TestTheCropGateDiscardsRegionsOnAChangedVerdict is the same rule asserted through
// [proposeCrops], with the verdict forced.
//
// It needs a proposer whose engine reports a moved view, which is why the orchestration
// rule above is tested separately rather than being assumed to reach here.
func TestTheCropGateDiscardsRegionsOnAChangedVerdict(t *testing.T) {
	p := &stubProposer{regions: []novelty.Candidate{region("motion", 0.7, 0.7, 0.76, 0.76)}}
	cp := passWith(p)
	cp.Gate = novelty.NewGate(0, 25)
	vc, err := novelty.NewViewChange(novelty.ViewChangeDefaults())
	if err != nil {
		t.Fatal(err)
	}
	cp.View = vc
	f := frameSource{Seq: 1, RGB: make([]byte, 1920*1080*3), Width: 1920, Height: 1080}
	// A quiet frame still crops, so the pass is demonstrably working before the change.
	if _, ct := proposeCrops(f, nil, cp); ct.Inferred != 1 {
		t.Fatalf("the ordinary frame inferred %d crops, want 1", ct.Inferred)
	}
	// The stub's engine reports no illumination offset, so this frame is stable and the
	// pass keeps cropping. That is the CONTROL: a crop pass with a view detector
	// attached that has seen no change must behave exactly as it did before.
	if _, ct := proposeCrops(f, nil, cp); ct.Inferred != 1 {
		t.Fatalf("with a view detector attached but nothing declared, the frame inferred %d crops, "+
			"want 1. Attaching the detector must not change ordinary frames", ct.Inferred)
	}
}

// TestACropPassThatIsOffProposesNothing: the rollback.
//
// One variable, and setting it back restores the previous behaviour exactly. This is
// what makes presence mode and the crop pass safe to ship as defaults: there is no state
// to unwind, and a run that cannot see a bird at 30 m is one flag away.
func TestACropPassThatIsOffProposesNothing(t *testing.T) {
	p := &stubProposer{regions: []novelty.Candidate{region("motion", 0.7, 0.7, 0.76, 0.76)}}
	cp := passWith(p)
	cp.Enabled = false
	got, ct := proposeCrops(aFrame(), nil, cp)
	if len(got) != 0 || p.calls != 0 {
		t.Fatalf("a disabled crop pass proposed %d crop(s) and called the proposer %d time(s). "+
			"SP_POC_CROP_PASS=false has to be the whole of the rollback: no inference, no proposer, "+
			"no state", len(got), p.calls)
	}
	if ct != (CropCounters{}) {
		t.Fatalf("a disabled crop pass still counted %+v; a pass that did nothing must count nothing", ct)
	}
}

// TestAnEnabledCropPassWithNoProposerProposesNothingRatherThanEveryFrame: the
// degenerate configuration.
//
// It returns nothing, and the caller refuses it at startup. What it must NOT do is infer
// the whole frame, which is the tempting fallback and would cost a whole extra inference
// per frame to find exactly what the whole-frame pass found.
func TestAnEnabledCropPassWithNoProposerProposesNothingRatherThanEveryFrame(t *testing.T) {
	cp := passWith(nil)
	cp.Proposer = nil
	got, ct := proposeCrops(aFrame(), nil, cp)
	if len(got) != 0 {
		t.Fatalf("an enabled crop pass with no proposer proposed %d crop(s)", len(got))
	}
	if ct.Proposed != 0 {
		t.Fatalf("Proposed = %d with no proposer", ct.Proposed)
	}
}

// TestSmallRegionsAndTheRegionCapAreCountedNotSilentlyDropped: the two filters whose
// whole justification is that their effect can be measured.
func TestSmallRegionsAndTheRegionCapAreCountedNotSilentlyDropped(t *testing.T) {
	p := &stubProposer{regions: []novelty.Candidate{
		region("motion", 0.10, 0.10, 0.101, 0.101), // one hundredth of a percent: a speck
		region("motion", 0.20, 0.20, 0.30, 0.30),   // big
		region("motion", 0.40, 0.40, 0.50, 0.50),   // big
		region("motion", 0.60, 0.60, 0.70, 0.70),   // big
		region("motion", 0.80, 0.80, 0.90, 0.90),   // big
	}}
	cp := passWith(p)
	cp.MaxRegions = 2
	got, ct := proposeCrops(aFrame(), nil, cp)
	if len(got) != 2 {
		t.Fatalf("proposed %d crop(s) against a cap of 2", len(got))
	}
	if ct.SkippedSmall != 1 {
		t.Errorf("SkippedSmall = %d, want 1: a two-hundredth-of-a-percent region is the noise floor of a "+
			"luma difference, and magnifying noise spends a full inference on it", ct.SkippedSmall)
	}
	if ct.SkippedCap != 2 {
		t.Errorf("SkippedCap = %d, want 2. A cap that silently drops is a cap that lies, and this one is "+
			"the run's dominant cost knob", ct.SkippedCap)
	}
	if ct.Proposed != 5 || ct.Inferred != 2 {
		t.Errorf("Proposed = %d, Inferred = %d; want 5 and 2", ct.Proposed, ct.Inferred)
	}
}

// TestACropCoveringTheWholeFrameIsNotWorthAnInference: rule 3, and it is the common
// case at the start of a run before a reference view has settled.
func TestACropCoveringTheWholeFrameIsNotWorthAnInference(t *testing.T) {
	p := &stubProposer{regions: []novelty.Candidate{
		{Box: novelty.Box{X1: -1, Y1: -1, X2: 2, Y2: 2}}, // the whole frame, generously
	}}
	got, ct := proposeCrops(aFrame(), nil, passWith(p))
	if len(got) != 0 {
		t.Fatalf("a region covering the whole frame produced %d crop(s). It is letterboxed to the same "+
			"416x416 as the whole-frame pass, so it costs the same and finds the same", len(got))
	}
	if ct.SkippedOverlap != 1 {
		t.Errorf("SkippedOverlap = %d, want 1: the counter is what tells a reader the cost of the gate", ct.SkippedOverlap)
	}
}

// TestCutSetMembershipIsExact: the channel the source-aware minimum rides on.
//
// Membership is by VALUE. A value comparison is exact, the same measurement has the
// same value, whereas matching by class or by proximity would mark a box whose source
// had to be guessed, and a guessed source is a re-inference on the one field the replay
// reads.
func TestCutSetMembershipIsExact(t *testing.T) {
	cut := ai.Box{Class: "bird", Score: 0.8, X1: 0.5, Y1: 0.5, X2: 0.6, Y2: 0.6}
	set := cutSetOf([]ai.Box{cut})
	if set == nil {
		t.Fatal("cutSetOf returned nil for a non-empty list")
	}
	if len(set) != 1 {
		t.Fatalf("the set has %d entries, want 1", len(set))
	}
	// Same class and same score, one pixel different: NOT the same measurement.
	near := ai.Box{Class: "bird", Score: 0.8, X1: 0.5001, Y1: 0.5, X2: 0.6, Y2: 0.6}
	if _, ok := set[near]; ok {
		t.Fatal("a box one pixel from a crop-sourced one was marked as crop-sourced too. Membership must " +
			"be by exact value or the source-aware minimum is applied to a sighting that was not magnified")
	}
	if cutSetOf(nil) != nil {
		t.Fatal("an empty list produced a non-nil set; the detector path allocates one map per frame")
	}
}

// TestCountersAddIsPerFieldNotTotalled: the run total is a sum of frames.
//
// A single field that summed instead, the obvious shortcut, would produce a run total
// that is a function of nothing a reader can decompose, and the four counters that count
// things that did NOT happen are the ones worth decomposing.
func TestCountersAddIsPerFieldNotTotalled(t *testing.T) {
	a := CropCounters{Proposed: 2, Inferred: 1, SkippedOverlap: 1, Refusals: 3}
	b := CropCounters{Proposed: 5, Inferred: 4, SkippedSmall: 1, SkippedCap: 2, BoxesFound: 7, BoxesKept: 6}
	got := a.Add(b)
	want := CropCounters{
		Proposed: 7, Inferred: 5, SkippedOverlap: 1, SkippedSmall: 1,
		SkippedCap: 2, BoxesFound: 7, BoxesKept: 6, Refusals: 3,
	}
	if got != want {
		t.Fatalf("Add = %+v, want %+v", got, want)
	}
}

// TestNoveltyScoreOfIsEmptyWhenThePassIsOff: the score the decision row carries.
func TestNoveltyScoreOfIsEmptyWhenThePassIsOff(t *testing.T) {
	if s := noveltyScoreOf(CropPass{}); s != (novelty.Score{}) {
		t.Fatalf("a disabled crop pass reported novelty score %+v. A run that carries no novelty engine "+
			"must not record its score against a decision it did not make", s)
	}
	want := novelty.Score{Value: 0.42}
	p := &stubProposer{score: want}
	if got := noveltyScoreOf(passWith(p)); got != want {
		t.Fatalf("noveltyScoreOf = %+v, want %+v", got, want)
	}
}

// TestRegionSourceForIsTheOnePlaceACutBoxBecomesCut: the mapping onto the stored
// column.
//
// The event's region_source is what the replay reads, so a hardcoded answer would be a
// false stored fact for every crop-sourced detection: it would claim the box was
// measured at the pipeline's own resolution when it was found in a magnified crop.
func TestRegionSourceForIsTheOnePlaceACutBoxBecomesCut(t *testing.T) {
	if got := regionSourceFor(true); got != novelty.RegionCut {
		t.Errorf("a crop-sourced box recorded region_source %q, want %q", got, novelty.RegionCut)
	}
	if got := regionSourceFor(false); got != novelty.RegionMeasured {
		t.Errorf("a whole-frame box recorded region_source %q, want %q", got, novelty.RegionMeasured)
	}
	// And the vocabulary has no duplicates: the column cannot be read back otherwise.
	if novelty.RegionWholeFrame == novelty.RegionCut || novelty.RegionMeasured == novelty.RegionCut {
		t.Fatal("the region-source vocabulary has duplicates; the column cannot be read back")
	}
	if novelty.RegionCut != "cut" {
		t.Errorf("RegionCut = %q. The value is stored in rows written by earlier builds, so it is a wire "+
			"value and not a spelling that can change", novelty.RegionCut)
	}
	_ = ortdetect.Rect{}
}
