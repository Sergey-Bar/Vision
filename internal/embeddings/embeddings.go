// Package embeddings writes the searchable text and the vector for a canonical
// event.
//
// # Why this exists
//
// The free-text search has always answered "unavailable", and the reason was
// one missing writer. `event_embeddings` has existed since migration 0027 --
// the table, the extension, the index -- and nothing ever wrote a row, so the
// search's two free-text paths both inner-joined an empty table and every
// question the console asked came back with nothing.
//
// # What the vector is
//
// A HASHED BAG OF WORDS, and it says so.
//
// It is not a neural sentence encoder. It is the feature-hashing trick: every
// token of the document is hashed to a bucket of a fixed-length vector and
// counted, with a second hash deciding the sign, so that the dot product of
// two documents approximates the overlap of their token sets and an unrelated
// token cancels rather than adding.
//
// That is a real embedding, and it is the honest one for this product:
//
//   - It is DETERMINISTIC. The same document always produces the same vector,
//     so a recorded session replays to the same search results. A neural model
//     on a GPU is not, and the fixture design this repository is built on
//     depends on the determinism.
//   - It needs no model, no download and no GPU, so the demo keeps working on a
//     machine with no network and no NVIDIA driver.
//   - It matches on WORDS. "bag" finds "backpack"; "drone" does not find
//     "aerial vehicle". A sentence encoder would match on meaning, and would
//     also need a model this product does not ship.
//
// The vector path is a RANKING, not the answer. The keyword path over the same
// document is the one that decides what matched, and the vector decides what
// came first. A search that ranked by a hashed bag of words and told you the
// ranking was semantic would be lying about what it did.
//
// # The document
//
// The document is the searchable text: the event's type, the device, the zone,
// and whatever the event says about the object it saw. It is written ALONGSIDE
// the vector rather than instead of it, because a question that matches a word
// the model never saw must still be able to return the event.
package embeddings

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Dimensions is the vector length.
//
// It is 384 because the column is `vector(384)`, and it is not negotiable: a
// vector of another length does not fit the column and the write fails.
const Dimensions = 384

// Model names the model that produced a vector.
//
// It is a name, and the honest one. It is in the column, so a row says what
// computed it, and a row computed by a different model is comparable to
// neither. It says "hashed-bag-of-words" because that is what computed it.
const Model = "hashed-bag-of-words-384"

// Document builds the searchable text for a canonical event.
//
// It is the event's own words and nothing else: the type, the device, the zone
// and the object's label. The event's payload is NOT included wholesale,
// because the payload can hold anything the vendor sent and a search index is
// the wrong place for a raw payload.
func Document(eventType, deviceID, zoneID, label string) string {
	parts := make([]string, 0, 4)
	for _, p := range []string{eventType, deviceID, zoneID, label} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// Embed returns the vector for a document.
//
// The sign hash is the second hash: it makes two documents that share a token
// but differ in many others score near zero rather than near one, which is what
// keeps the cosine distance meaningful when the vocabulary is larger than the
// vector.
func Embed(document string) []float32 {
	vec := make([]float32, Dimensions)
	tokens := tokenize(document)
	if len(tokens) == 0 {
		return vec
	}
	for _, token := range tokens {
		bucket := fnv.New64a()
		_, _ = bucket.Write([]byte(token))
		h := bucket.Sum64()
		idx := int(h % uint64(Dimensions))
		sign := float32(1)
		if h2 := sha256.Sum256([]byte("sign:" + token)); h2[0]&1 == 1 {
			sign = -1
		}
		vec[idx] += sign
	}
	// L2-normalise, so the cosine distance is a plain dot product and the
	// magnitudes do not depend on how long the document was.
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
	return vec
}

// tokenize splits a document into lower-cased word tokens.
//
// It splits on everything that is not a letter or a digit, and it keeps the
// numbers: "cam-3" and "cam3" are the same camera, and "14:00" is a time.
func tokenize(document string) []string {
	fields := strings.FieldsFunc(strings.ToLower(document), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Encode renders a vector as the pgvector literal.
//
// pgvector's text input is `[v1,v2,...]`, and the numbers are written with
// enough precision to survive the round trip: the vector is a float32 and the
// text is a float64, so writing seven decimal places is enough and writing
// seventeen is noise.
func Encode(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.7g", v)
	}
	b.WriteByte(']')
	return b.String()
}

// Embedder is the search surface's model.
//
// It satisfies `eventsearch.Embedder` structurally: the search surface names
// no model, and this is the one the product ships.
//
// The context is accepted and ignored. There is no I/O here -- no model file,
// no download, no GPU -- so there is nothing for it to cancel. It is in the
// signature because the interface it satisfies has it, and an implementation
// that took a context it ignored would be a lie about being cancellable.
type Embedder struct{}

// Embed returns the vector for a document.
func (Embedder) Embed(_ context.Context, text string) ([]float32, error) {
	return Embed(text), nil
}

// Writer writes the embedding row for a canonical event.
//
// It is called INSIDE the transaction that writes the canonical event, so the
// two cannot disagree: there is no window in which an event exists and its
// search row does not, and none in which a search row exists without its
// event.
type Writer struct {
	// Enabled says whether the writer runs at all. A database without pgvector
	// has no `vector` type, and a write against one is an error. The writer
	// then does nothing, and the search says it could not rank rather than
	// failing the ingest.
	Enabled bool
}

// Write writes the row for one event.
//
// The write is an UPSERT: a re-normalization of the same event must not fail on
// a duplicate key, and the document it produces is the same document, so
// overwriting it is a no-op that is also correct.
func (w *Writer) Write(ctx context.Context, tx pgx.Tx, tenantID, eventID, document string) error {
	if w == nil || !w.Enabled {
		return nil
	}
	if strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("embeddings: no event id")
	}
	vector := Encode(Embed(document))
	_, err := tx.Exec(ctx, `
		INSERT INTO event_embeddings (tenant_id, event_id, embedding, document, model)
		VALUES ($1, $2, $3::vector, $4, $5)
		ON CONFLICT (tenant_id, event_id) DO UPDATE
		   SET embedding = EXCLUDED.embedding,
		       document  = EXCLUDED.document,
		       model     = EXCLUDED.model`,
		tenantID, eventID, vector, document, Model)
	if err != nil {
		return fmt.Errorf("embeddings: write the row for %s: %w", eventID, err)
	}
	return nil
}
