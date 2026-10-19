package fixture

// The fixture site's topology.
//
// The committed scenarios name devices in their `Source` fields — "Entrance
// Camera 01 [CAM-001]", "Loading Bay Door [DOR-004]", "Perimeter North
// [CAM-014]" — and a rule that filters on a ZONE needs to know which zone each
// of those is in. Without this table a zone condition cannot be evaluated at
// all, and the simulation that is supposed to prove a bundle's behaviour
// reports every zone rule as dead.
//
// That is not a hypothetical. The simulation built its signals from the
// adapter's records and left the zone EMPTY, so the bundle's production rules
// — every one of which filters on `zone.pathPrefix` — never fired in the gate
// that exists to catch a bundle change that would matter in production. A
// change that only differs BY ZONE looked like no change at all.
//
// The table lives here, in the package that owns the scenarios, for two
// reasons: the seeder writes this topology into a real database, and the
// simulation reads it to answer the same question the database answers in
// production. One table, two readers, and a change to the fixtures' devices is
// a change to both.
//
// The zone paths are the bundle's contract. `pathHasPrefix` matches a
// contiguous run of WHOLE SEGMENTS anywhere in the path rather than a literal
// string prefix, so "/north-gate/restricted/loading-bay" carries the run
// "/restricted" and matches, and "/north-gate/restricted-bay" would not. A
// topology with the wrong path here makes the bundle's rules dead rather than
// failing, which is the specific outcome `internal/rules/semantic.go` exists to
// prevent.

// Device is one device the committed scenarios name.
type Device struct {
	// SourceDeviceID is the reference the source wrote, e.g. "CAM-001".
	SourceDeviceID string
	// DeviceID is the platform's own id for it.
	DeviceID string
	// ZoneID is the zone it is in.
	ZoneID string
	// Kind is "camera" or "door".
	Kind string
	// Name is what an operator would call it.
	Name string
}

// Zone is one zone of the fixture site.
type Zone struct {
	ZoneID string
	Name   string
	Path   string
	Parent string
	Depth  int
}

// Devices are the devices the committed scenarios name.
//
// The list is derived from the fixtures rather than invented, and the seeder
// prints it. If a scenario gains a device reference, the seed says so rather
// than quietly attributing the event to the nearest camera it knows.
var Devices = []Device{
	{"CAM-001", "dev-entrance-01", "zone-restricted-bay", "camera", "Entrance Camera 01"},
	{"CAM-014", "dev-perimeter-north", "zone-perimeter", "camera", "Perimeter North"},
	{"DOR-004", "dev-loading-bay-door", "zone-restricted-bay", "door", "Loading Bay Door"},
}

// Zones are the zones the shipped bundle's production rules need.
//
// `core.person-in-restricted-zone-after-hours` filters on
// `zone.pathPrefix: /restricted`, and `pathHasPrefix` matches a contiguous run
// of WHOLE SEGMENTS anywhere in the path rather than a literal string prefix —
// so "/north-gate/restricted/loading-bay" carries the run "/restricted" and
// matches, and "/north-gate/restricted-bay" would not.
//
// Depth is written because the column is `CHECK (depth >= 0)` and because a
// zone tree with a wrong depth is a zone tree the recursive traversal gets
// wrong.
var Zones = []Zone{
	{"zone-site", "North Gate", "/north-gate", "", 0},
	{"zone-restricted", "Restricted", "/north-gate/restricted", "zone-site", 1},
	{"zone-restricted-bay", "Loading Bay", "/north-gate/restricted/loading-bay", "zone-restricted", 2},
	{"zone-perimeter", "Perimeter", "/north-gate/perimeter", "zone-site", 1},
}

// ZonePathFor returns the zone path of the zone a source device is in.
//
// The second answer is false when the device is not one the fixture site knows:
// a device that is not registered produces an event with no device, and an event
// with no zone cannot satisfy a zone condition — which is the correct answer,
// because "we do not know where this happened" and "it happened in the
// restricted zone" are different facts.
func ZonePathFor(sourceDeviceID string) (zoneID string, zonePath string, ok bool) {
	sourceDeviceID = trimBrackets(sourceDeviceID)
	var deviceZone string
	for _, d := range Devices {
		if d.SourceDeviceID == sourceDeviceID {
			deviceZone = d.ZoneID
			break
		}
	}
	if deviceZone == "" {
		return "", "", false
	}
	for _, z := range Zones {
		if z.ZoneID == deviceZone {
			return z.ZoneID, z.Path, true
		}
	}
	return deviceZone, "", false
}

// trimBrackets removes the "[CAM-001]" a source appends to a device name.
func trimBrackets(ref string) string {
	ref = trimSpace(ref)
	if open := lastIndexByte(ref, '['); open >= 0 {
		if end := lastIndexByte(ref, ']'); end > open {
			return trimSpace(ref[open+1 : end])
		}
	}
	return ref
}

// trimSpace trims ASCII space.
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// lastIndexByte is strings.LastIndexByte, named locally so the table's helpers
// read as one block.
func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
