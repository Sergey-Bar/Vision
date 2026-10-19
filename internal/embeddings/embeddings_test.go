package embeddings

import (
	"math"
	"strings"
	"testing"
)

// The vector is deterministic, and that is the property the whole search rests
// on.
//
// A recorded session has to replay to the same results. A model on a GPU does
// not promise that, and a vector that moved between two runs would make a
// recorded answer unreproducible -- which is the defect the fixture design
// this repository is built on exists to prevent.
func TestTheEmbeddingIsDeterministic(t *testing.T) {
	doc := "analytics.person_with_bag entrance cam-1 restricted-zone"
	first := Embed(doc)
	second := Embed(doc)
	if len(first) != Dimensions {
		t.Fatalf("the vector is %d long, want %d", len(first), Dimensions)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("the same document produced two different vectors at %d: %v and %v",
				i, first[i], second[i])
		}
	}
}

// The vector is unit-length, so the cosine distance is a plain dot product and
// a long document does not outrank a short one merely by being long.
func TestTheVectorIsUnitLength(t *testing.T) {
	for _, doc := range []string{
		"analytics.person_with_bag entrance cam-1 restricted-zone",
		"analytics.vehicle street cam-2",
		"a",
	} {
		vec := Embed(doc)
		var norm float64
		for _, v := range vec {
			norm += float64(v) * float64(v)
		}
		if math.Abs(math.Sqrt(norm)-1) > 1e-5 {
			t.Errorf("the vector for %q has length %f, want 1", doc, math.Sqrt(norm))
		}
	}
}

// A document that shares words with the query scores above one that shares
// none.
//
// This is the property that makes the vector a RANKING rather than noise. It
// is lexical, not semantic: "bag" finds "backpack" and "drone" does not find
// "aerial vehicle", and the package says so rather than pretending otherwise.
func TestADocumentThatSharesWordsScoresHigher(t *testing.T) {
	query := Embed("bag backpack person entrance")
	near := Embed("analytics.person_with_bag entrance cam-1 backpack")
	far := Embed("analytics.vehicle street cam-2 minibus")

	if dot(query, near) <= dot(query, far) {
		t.Errorf("the document about a backpack scored %f and the one about a minibus scored %f. "+
			"The ranking has to prefer the one that shares the query's words",
			dot(query, near), dot(query, far))
	}
}

// An unrelated token CANCELS rather than adding, which is the second hash's
// job and the reason the cosine distance means anything when the vocabulary is
// larger than the vector.
func TestAnUnrelatedTokenDoesNotAdd(t *testing.T) {
	// Two documents with the same number of tokens, one shared and one not.
	// If the signs did not cancel, the unrelated token would raise the score.
	a := Embed("alpha beta")
	b := Embed("alpha gamma")
	c := Embed("alpha delta")
	if math.Abs(dot(a, b)-dot(a, c)) > 1e-6 {
		t.Errorf("two documents that differ in one token scored %f and %f; the unrelated "+
			"token should neither add nor subtract systematically", dot(a, b), dot(a, c))
	}
}

// An empty document is a zero vector, and not a panic.
func TestAnEmptyDocumentIsAZeroVector(t *testing.T) {
	vec := Embed("")
	if len(vec) != Dimensions {
		t.Fatalf("the vector is %d long, want %d", len(vec), Dimensions)
	}
	for i, v := range vec {
		if v != 0 {
			t.Fatalf("the vector is not zero at %d: %f", i, v)
		}
	}
}

// The document carries the words a question would use, and nothing else.
//
// The payload is not included wholesale: a search index is the wrong place for
// a raw vendor payload, and the words that matter are the type, the device,
// the zone and the label.
func TestTheDocumentCarriesTheWordsAQuestionWouldUse(t *testing.T) {
	doc := Document("analytics.person_with_bag", "entrance", "restricted-zone", "person with bag")
	for _, want := range []string{"analytics.person_with_bag", "entrance", "restricted-zone", "person with bag"} {
		if !strings.Contains(doc, want) {
			t.Errorf("the document %q does not carry %q", doc, want)
		}
	}
	if got := Document("analytics.vehicle", "", "", ""); strings.Contains(got, "  ") {
		t.Errorf("the document %q has an empty part in it", got)
	}
}

// The encoded vector is the pgvector literal.
//
// The format is the column's: `[v1,v2,...]`, and a literal that is not in it
// is a write that fails at the database rather than at the test.
func TestTheEncodedVectorIsThePgvectorLiteral(t *testing.T) {
	encoded := Encode(Embed("analytics.vehicle street cam-2"))
	if !strings.HasPrefix(encoded, "[") || !strings.HasSuffix(encoded, "]") {
		t.Errorf("the encoded vector is %q, and pgvector's literal is bracketed", encoded)
	}
	parts := strings.Split(strings.Trim(encoded, "[]"), ",")
	if len(parts) != Dimensions {
		t.Errorf("the encoded vector has %d components, want %d", len(parts), Dimensions)
	}
}

// dot is the cosine similarity of two unit vectors.
func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		if i < len(b) {
			sum += float64(a[i]) * float64(b[i])
		}
	}
	return sum
}
