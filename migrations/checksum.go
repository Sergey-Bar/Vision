package migrations

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"os"
)

// checksum is the SHA-256 over the migration body, recorded at apply time and
// re-verified on every subsequent run. Editing an applied migration changes the
// checksum, and the runner refuses to proceed rather than silently diverge from
// what a live site actually executed.
func checksum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Compatibility is the release's declared schema window. A binary refuses to
// run against a database outside it: too old and it will misread columns, too
// new and it may write rows the running code cannot represent.
//
// Expand/contract exists precisely so that the window is wide enough to hold
// both the old and the new binary at once, which is what makes an in-place
// upgrade safe on a site with no maintenance window.
type Compatibility struct {
	MinSchemaVersion int    `json:"minSchemaVersion"`
	MaxSchemaVersion int    `json:"maxSchemaVersion"`
	MinCoreVersion   string `json:"minCoreVersion"`
	MaxCoreVersion   string `json:"maxCoreVersion"`
	ContractsVersion string `json:"contractsVersion"`
	Notes            string `json:"notes,omitempty"`
}

const compatFile = "compat.json"

//go:embed compat.json
var compatBytes []byte

// ReleaseCompatibility returns the schema window this build was compiled
// against.
func ReleaseCompatibility() (Compatibility, error) {
	var c Compatibility
	if err := json.Unmarshal(compatBytes, &c); err != nil {
		return c, err
	}
	if c.MinSchemaVersion <= 0 || c.MaxSchemaVersion < c.MinSchemaVersion {
		return c, &IncompatibleSchemaError{
			Current: 0, Min: c.MinSchemaVersion, Max: c.MaxSchemaVersion,
			Reason: "release manifest declares an empty schema window",
		}
	}
	return c, nil
}

// LatestVersion is the highest migration version in the embedded set, which is
// what a fresh install converges to.
func LatestVersion() (int, error) {
	all, err := All()
	if err != nil {
		return 0, err
	}
	if len(all) == 0 {
		return 0, nil
	}
	return all[len(all)-1].Version, nil
}

// IncompatibleSchemaError is returned when the database's schema version falls
// outside the window this build supports.
type IncompatibleSchemaError struct {
	Current int
	Min     int
	Max     int
	Reason  string
}

func (e *IncompatibleSchemaError) Error() string {
	return "schema version " + itoa(e.Current) + " is outside the supported window [" +
		itoa(e.Min) + ", " + itoa(e.Max) + "]: " + e.Reason +
		". Upgrade or roll back the binary rather than forcing a migration."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// LoadCompatibilityFromFile reads a compatibility manifest from disk. Used by
// the release verifier, which must check the manifest inside an archive rather
// than the one compiled into a binary it has not yet validated.
func LoadCompatibilityFromFile(path string) (Compatibility, error) {
	var c Compatibility
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, nil
}
