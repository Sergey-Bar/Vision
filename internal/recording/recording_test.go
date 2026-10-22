package recording

import (
	"testing"
	"time"
)

// The chain.
//
// The chain is the PROOF: it is what makes a recording on a customer's own
// disk evidence rather than a file. The tests below are the two properties
// that proof rests on.
//
//  1. A link is a function of its three inputs and nothing else.
//  2. A link computed on the way IN equals a link computed on the way OUT.
//
// The second is the one that bites, and it bit: the hash was over a
// nanosecond timestamp while the storage keeps microseconds, so a chain
// recomputed from the rows differed from the chain that was written — for
// every segment whose timestamp happened to end in something other than
// zeros. A proof that fails at random is worse than no proof, because it
// looks like tampering.

func TestTheChainHashIsAFunctionOfItsThreeInputs(t *testing.T) {
	when := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	base := ChainHash("", "aa", when)

	if base == ChainHash("bb", "aa", when) {
		t.Error("the previous hash does not change the link")
	}
	if base == ChainHash("", "cc", when) {
		t.Error("the segment's own digest does not change the link")
	}
	if base == ChainHash("", "aa", when.Add(time.Second)) {
		t.Error("the segment's own start does not change the link")
	}
	// The same inputs, the same link. A hash that is not deterministic is
	// not a chain.
	if base != ChainHash("", "aa", when) {
		t.Error("the same inputs produced a different link")
	}
}

func TestTheChainHashSurvivesTheStorageRoundTrip(t *testing.T) {
	// The storage keeps MICROSECONDS. The hash must be computed over the
	// timestamp the storage will keep, or the chain written on the way in
	// and the chain recomputed on the way out are different chains.
	//
	// The nanosecond component here is the awkward one -- 789 nanoseconds
	// past the microsecond -- and it is the case that failed.
	when := time.Date(2026, 10, 22, 12, 0, 0, 123456789, time.UTC)
	stored := when.Truncate(time.Microsecond)

	if when.Equal(stored) {
		t.Fatal("the test's timestamp does not have a sub-microsecond component")
	}
	if ChainHash("", "aa", when) != ChainHash("", "aa", stored) {
		t.Error("the link computed from a nanosecond timestamp differs from the link computed " +
			"from the timestamp the storage keeps. The chain would not verify itself")
	}
}

func TestTheChainHashIsTheSameInstantInAnyZone(t *testing.T) {
	// The chain is per UTC day, so the same instant rendered in two zones
	// has to be the same link. A link computed from a local rendering would
	// be a different link, and a recorder running in a timezone would break
	// the proof for every segment it wrote.
	utc := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	zone := time.FixedZone("elsewhere", 3*60*60)

	if ChainHash("", "aa", utc) != ChainHash("", "aa", utc.In(zone)) {
		t.Error("the same instant in two zones produced two links")
	}
}

func TestTheChainCrossesTheDayBoundary(t *testing.T) {
	// The chain is per DAY so that it can be SEALED per day, and it CROSSES
	// the boundary so that it is one chain. A chain that restarted at
	// midnight would be a chain that could be REPLACED: removing a whole day
	// would leave a chain that still verifies from the next morning.
	day := time.Date(2026, 10, 22, 23, 59, 30, 0, time.UTC)
	next := day.Add(time.Minute)

	if ChainHash("", "aa", day) == ChainHash("", "aa", next) {
		t.Error("two segments on either side of midnight chain the same")
	}
	// And the link the next day starts from is the one the previous day
	// ended on.
	head := ChainHash("", "aa", day)
	if ChainHash(head, "bb", next) == ChainHash("", "bb", next) {
		t.Error("the chain does not continue across the day boundary")
	}
}
