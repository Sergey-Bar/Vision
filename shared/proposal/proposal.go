// Package proposal is the public face of the box geometry.
//
// The implementation is `github.com/Sergey-Bar/vision/internal/proposal`. It is
// internal because a box is a wire type with a wire contract -- the JSON tags
// are the sidecar's -- and a wire type that half the tree can reshape is a wire
// type that stops being a contract.
//
// The POC module sends and receives these boxes, so it comes here, over the
// module boundary, as aliases of the same types.
package proposal

import "github.com/Sergey-Bar/vision/internal/proposal"

// The geometry.
type (
	// Box is a region in pixel coordinates.
	Box = proposal.Box
	// Candidate is a class and a score.
	Candidate = proposal.Candidate
)
