// Package schemasync holds a Go-side copy of every published JSON Schema and
// fails when one of them drifts from the contract it was copied from.
//
// The copies exist so that a binary can validate against a schema without
// carrying the contracts/ tree, and they are the reason `make contract-sync`
// has to compare more than one file: before this, only `rule-bundle` had a
// Go-side copy, so the other five schemas could be edited in one place and
// validated against another without anything noticing.
//
// Nothing here parses a schema. These are byte copies on purpose: a copy that
// is re-serialised on the way in is a copy that can differ from the contract
// while looking identical, and "identical" is the whole property being checked.
package schemasync

import (
	"embed"
	"io/fs"
)

//go:embed rule-bundle.schema.json site-config.schema.json bundle-manifest.schema.json investigation-template.schema.json ai-observations/*.schema.json
var embedded embed.FS

// Published is the published path of each schema, relative to the repository
// root, mapped to the copy held here.
var Published = map[string]string{
	"contracts/json-schema/rule-bundle.schema.json":                        "rule-bundle.schema.json",
	"contracts/json-schema/site-config.schema.json":                        "site-config.schema.json",
	"contracts/json-schema/bundle-manifest.schema.json":                    "bundle-manifest.schema.json",
	"contracts/json-schema/investigation-template.schema.json":             "investigation-template.schema.json",
	"contracts/json-schema/ai-observations/door-forced.schema.json":        "ai-observations/door-forced.schema.json",
	"contracts/json-schema/ai-observations/restricted-person.schema.json":  "ai-observations/restricted-person.schema.json",
	"contracts/json-schema/ai-observations/general-monitoring.schema.json": "ai-observations/general-monitoring.schema.json",
	"contracts/json-schema/ai-observations/vehicle-at-gate.schema.json":    "ai-observations/vehicle-at-gate.schema.json",
}

// Bytes returns the embedded copy of one schema, named as it is named in
// Published.
func Bytes(name string) ([]byte, error) {
	return embedded.ReadFile(name)
}

// FS exposes the embedded copies as a filesystem, for a caller that wants to
// compile a validator from them.
func FS() (fs.FS, error) {
	return fs.Sub(embedded, ".")
}
