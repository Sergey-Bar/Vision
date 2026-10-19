// Package shared is the root module's public face for the primitives the POC
// module needs.
//
// # Why these packages exist at all
//
// `poc/active-monitoring/` is a separate Go module with its own `go.mod`. A Go
// module boundary is also a visibility boundary: `poc/active-monitoring` cannot
// import `github.com/Sergey-Bar/vision/internal/...`, because `internal/` is
// visible only inside the module that declares it.
//
// So when the in-process ONNX detector, the class taxonomy, the sidecar wire
// and the box geometry moved from the POC module into the product module, the
// POC module was left unable to reach them. The two ways out are a second copy
// or a public face. A second copy is the option this repository's own route
// audit warns against -- two implementations of one thing, drifting apart
// silently -- and a copy of a decoder is the worst possible thing to let drift,
// because the drift shows up as "the product detected an object the POC did
// not" and nobody knows which one is wrong.
//
// So each package here is a thin alias over its `internal/` twin: the
// implementation stays behind `internal/`, where the product keeps it, and the
// alias publishes the same types under the same names. A type alias, not a
// wrapper: `shared/ortdetect.Box` and `internal/ortdetect.Box` are the same
// type, so a value crosses the boundary without a conversion and without a
// chance of the two sides disagreeing about what it means.
//
// # What this is not
//
// It is not a public API for the product. The product's HTTP surface is the
// API; this is a seam between two modules in one repository, and it exists
// because the Go toolchain offers no other way to share an internal package
// between them.
//
// Each alias package says which internal package it fronts, and why that
// package is internal.
package shared
