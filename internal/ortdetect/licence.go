package ortdetect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// modelManifest is what the fetch command writes beside a model.
//
// It is the model's provenance: where it came from, the digest it arrived with,
// the licence it is under, and when it was fetched. The detector reads the
// licence out of it so that every measurement names the terms the answer
// arrived under.
type modelManifest struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Licence   string `json:"licence"`
	Source    string `json:"source"`
	Note      string `json:"note"`
	FetchedAt string `json:"fetched_at"`
}

// manifestName is the file the manifest is written under, beside the model.
const manifestName = "model.json"

// readLicence reads the licence out of the manifest beside a model.
//
// It returns the empty string when there is no manifest, when the manifest has
// no licence, or when the manifest cannot be read. The empty string is the
// honest answer in all three cases -- "reported rather than defaulted" -- and
// it is why [Identity.Licence] documents itself as empty when nothing said.
//
// The search is for `model.json` in the model's own directory. A manifest
// somewhere else is a manifest about another model, and reading it would be
// recording one model's licence against another's answer.
func readLicence(modelPath string) string {
	if strings.TrimSpace(modelPath) == "" {
		return ""
	}
	dir := filepath.Dir(modelPath)
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return ""
	}
	var manifest modelManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ""
	}
	return strings.TrimSpace(manifest.Licence)
}
