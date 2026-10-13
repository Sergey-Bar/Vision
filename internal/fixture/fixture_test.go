package fixture_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/fixture"
)

// The fixture is the thing that gets committed, so the sanitizer is the only
// thing standing between a live site's data and a public repository. These tests
// are the evidence for that claim; the plan's threat model names bundle and
// capture hygiene as a control.

func TestRedactsSecretsByKeyName(t *testing.T) {
	doc := map[string]any{
		"Id":            "evt-1",
		"password":      "hunter2",
		"ClientSecret":  "s3cr3t",
		"Authorization": "Bearer abcdefghijklmnopqrstuvwxyz012345",
		"api_key":       "key-material",
		"Password":      "camelCase-should-still-match",
		"nested": map[string]any{
			"token": "nested-token",
			"safe":  "kept",
		},
	}

	out, err := fixture.RedactValue("", doc)
	if err != nil {
		t.Fatalf("RedactValue: %v", err)
	}
	m := out.(map[string]any)

	for _, key := range []string{"password", "ClientSecret", "Authorization", "api_key", "Password"} {
		if m[key] != fixture.RedactionMarker {
			t.Errorf("%s was not redacted, got %v", key, m[key])
		}
	}
	if nested := m["nested"].(map[string]any); nested["token"] != fixture.RedactionMarker {
		t.Errorf("nested token was not redacted, got %v", nested["token"])
	}
	if m["Id"] != "evt-1" {
		t.Errorf("a non-secret field was altered: %v", m["Id"])
	}
	if nested := m["nested"].(map[string]any); nested["safe"] != "kept" {
		t.Errorf("a non-secret nested field was altered: %v", nested["safe"])
	}
}

// Key matching has to ignore the case and the separator conventions vendors
// disagree about, or the allow-list is a coin flip.
func TestSecretKeyMatchingIgnoresCaseAndSeparators(t *testing.T) {
	for _, key := range []string{
		"apiKey", "ApiKey", "API_KEY", "api-key", "api_key", "Api_Key",
	} {
		doc := map[string]any{key: "material"}
		out, err := fixture.RedactValue("", doc)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if out.(map[string]any)[key] != fixture.RedactionMarker {
			t.Errorf("%s was not redacted", key)
		}
	}
}

// Identifiers are not secrets, but a committed fixture carrying a real plate or
// badge number puts live site data in a public repository.
// A capture run against a live site produces a person, a face and a place. None
// of them is a credential, and a reviewer hunting for a leaked password does not
// flag any of them - which is exactly how they get into a committed file.
//
// The list the sanitizer had was a list of things that look like secrets, so it
// had none of these. A face is a biometric and a commit is permanent; a
// latitude and longitude with a display name is an identified individual.
func TestRedactsPeopleAndPlaces(t *testing.T) {
	doc := map[string]any{
		"Id":          "evt-1",
		"Direction":   "Inbound",
		"DisplayName": "A. Okonkwo",
		"FirstName":   "Ada",
		"EmployeeNo":  "EMP-0041",
		"Latitude":    51.5072,
		"Longitude":   -0.1276,
		"ImageUrl":    "https://cam.internal.example/frames/0001.jpg",
		"Photo":       "https://cam.internal.example/frames/0002.jpg",
		"FaceDetections": []any{
			map[string]any{"Confidence": 0.98, "FaceId": "face-77"},
		},
		"Coordinates": map[string]any{"lat": 51.5072, "lon": -0.1276},
	}
	out, err := fixture.RedactValue("", doc)
	if err != nil {
		t.Fatalf("RedactValue: %v", err)
	}
	m := out.(map[string]any)

	for _, key := range []string{
		"DisplayName", "FirstName", "EmployeeNo",
		"Latitude", "Longitude", "ImageUrl", "Photo",
	} {
		if m[key] != fixture.RedactionMarker {
			t.Errorf("%s was not redacted, got %v. A committed capture is permanent; a face "+
				"and a name and a location do not un-leak", key, m[key])
		}
	}
	faces, ok := m["FaceDetections"].([]any)
	if !ok || len(faces) != 1 {
		t.Fatalf("FaceDetections was not walked: %v", m["FaceDetections"])
	}
	face := faces[0].(map[string]any)
	if face["FaceId"] != fixture.RedactionMarker {
		t.Errorf("a face id survived redaction: %v", face["FaceId"])
	}
	if face["Confidence"] != 0.98 {
		t.Errorf("a detection score was altered: %v", face["Confidence"])
	}

	if m["Coordinates"] != fixture.RedactionMarker {
		t.Errorf("a nested coordinates object survived: %v", m["Coordinates"])
	}
	if m["Id"] != "evt-1" || m["Direction"] != "Inbound" {
		t.Errorf("fields that identify nobody were altered: %v", m)
	}
}

// A device identified by number is identified whether the number is a string or
// an integer, and the value-level rules never saw the integers: RedactValue's
// number branch passed a float64 straight through. site_id 3, device_id 41 and
// partition 7 are enough to reconstruct a site's layout from a public file.
func TestRedactsNumericDeviceIdentifiers(t *testing.T) {
	doc := map[string]any{
		"EventId":    "evt-1",
		"Confidence": 0.97,
		"LatencyMs":  4200,
		"Metadata": map[string]any{
			"SiteId":       3,
			"deviceId":     41,
			"Door":         float64(7),
			"Partition_No": 2,
			"ChannelId":    float64(9),
			"Direction":    "Inbound",
		},
	}
	out, err := fixture.RedactValue("", doc)
	if err != nil {
		t.Fatalf("RedactValue: %v", err)
	}
	meta := out.(map[string]any)["Metadata"].(map[string]any)

	for _, key := range []string{"SiteId", "deviceId", "Door", "Partition_No", "ChannelId"} {
		if meta[key] != fixture.RedactionMarker {
			t.Errorf("%s (a numeric device identifier) was not redacted, got %v (%T)", key, meta[key], meta[key])
		}
	}
	if meta["Direction"] != "Inbound" {
		t.Errorf("a field that identifies nothing was altered: %v", meta["Direction"])
	}

	top := out.(map[string]any)
	if top["Confidence"] != 0.97 || top["LatencyMs"] != 4200 {
		t.Errorf("measurements were redacted: %v", top)
	}
}

// The header allow-list is deny-by-default, and a header that is credential-shaped
// has no business being in it. `x-milestone-session` is a session: a captured
// value is a bearer of the vendor's authenticated session, committed to a
// repository. Nothing replays on it.
func TestTheHeaderAllowListCarriesNothingCredentialShaped(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}
	resp.Header.Set("Content-Type", "application/json")
	for _, h := range []string{
		"X-Milestone-Session", "Authorization", "Cookie", "Set-Cookie",
		"X-Api-Key", "X-Session-Token", "Proxy-Authorization",
	} {
		resp.Header.Set(h, "abc123")
	}
	_, headers, err := fixture.SanitizeResponse(resp)
	if err != nil {
		t.Fatalf("SanitizeResponse: %v", err)
	}
	for k := range headers {
		switch strings.ToLower(k) {
		case "authorization", "cookie", "set-cookie", "x-api-key", "x-session-token",
			"proxy-authorization", "x-milestone-session":
			t.Errorf("the allow-list carried the credential-shaped header %q. The list is "+
				"deny-by-default, so a credential in it is a credential in every committed "+
				"capture", k)
		}
	}
	if len(headers) == 0 {
		t.Error("no header survived; the allow-list has stopped carrying anything at all, " +
			"which is a different failure from carrying the wrong thing")
	}
}

func TestRedactsIdentifiers(t *testing.T) {
	doc := map[string]any{
		"Id":           "evt-1",
		"MacAddress":   "00:11:22:33:44:55",
		"LicensePlate": "AB-123-CD",
		"SerialNumber": "SN-99887",
		"Email":        "someone@example.invalid",
		"Metadata": map[string]any{
			"Plate":     "XX-999-YY",
			"Direction": "Inbound",
		},
	}
	out, _ := fixture.RedactValue("", doc)
	m := out.(map[string]any)

	for _, key := range []string{"MacAddress", "LicensePlate", "SerialNumber", "Email"} {
		if m[key] != fixture.RedactionMarker {
			t.Errorf("%s was not redacted, got %v", key, m[key])
		}
	}
	if m["Id"] != "evt-1" {
		t.Errorf("an unrelated field was altered: %v", m["Id"])
	}
	meta := m["Metadata"].(map[string]any)
	if meta["Plate"] != fixture.RedactionMarker {
		t.Errorf("nested plate was not redacted, got %v", meta["Plate"])
	}
	if meta["Direction"] != "Inbound" {
		t.Errorf("nested non-identifier was altered: %v", meta["Direction"])
	}
}

// A URL whose key is innocuous still carries the site hostname and, often, a
// token in the query string.
func TestRedactsURLs(t *testing.T) {
	cases := map[string]string{
		"url":           "https://milestone.internal.example/api/v1/Events?token=abc123",
		"baseUrl":       "http://192.168.1.50:8080/api",
		"webhookUrl":    "https://hooks.example.invalid/x",
		"callback":      "https://app.example.invalid/done",
		"unrelatedText": "https://not-a-url-key.example/x",
	}
	doc := make(map[string]any, len(cases))
	for k, v := range cases {
		doc[k] = v
	}
	out, _ := fixture.RedactValue("", doc)
	m := out.(map[string]any)

	for _, key := range []string{"url", "baseUrl", "webhookUrl", "callback"} {
		got := m[key].(string)
		if strings.Contains(got, "192.168.1.50") || strings.Contains(got, "milestone.internal.example") {
			t.Errorf("%s still carries the source host: %q", key, got)
		}
		if strings.Contains(got, "token=abc123") {
			t.Errorf("%s still carries a query string: %q", key, got)
		}
	}
	if m["unrelatedText"] != cases["unrelatedText"] {
		t.Errorf("a string under a non-URL key was altered: %v", m["unrelatedText"])
	}
}

// A long opaque value under an innocuous key is treated as a secret. This
// over-redacts deliberately: a fixture that lost a value still exercises routing
// and shape, while a fixture that kept a real token is a leak nobody can unsee.
func TestRedactsOpaqueLongStrings(t *testing.T) {
	doc := map[string]any{
		"description": "aGVsbG8gd29ybGQgdGhpcyBpcyBhIHRlc3Qgc3RyaW5nIHZhbHVl",
		"message":     "Motion detected in zone Loading Bay",
		"short":       "abc123",
	}
	out, _ := fixture.RedactValue("", doc)
	m := out.(map[string]any)
	if m["description"] != fixture.RedactionMarker {
		t.Errorf("an opaque long string was kept: %v", m["description"])
	}
	if m["message"] != "Motion detected in zone Loading Bay" {
		t.Errorf("an ordinary message was altered: %v", m["message"])
	}
	if m["short"] != "abc123" {
		t.Errorf("a short value was altered: %v", m["short"])
	}
}

func TestSanitizeResponse(t *testing.T) {
	body := `{"Id":"e1","Type":"Motion","Source":{"Host":"milestone.internal"},"Password":"hunter2","Items":[{"LicensePlate":"AB-1"}]}`
	resp := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type":  []string{"application/json"},
			"Set-Cookie":    []string{"session=should-never-appear"},
			"Authorization": []string{"Bearer leaked"},
			"X-Request-Id":  []string{"req-42"},
		},
		Body: newReadCloser(body),
	}

	clean, headers, err := fixture.SanitizeResponse(resp)
	if err != nil {
		t.Fatalf("SanitizeResponse: %v", err)
	}

	text := string(clean)
	for _, leak := range []string{"hunter2", "should-never-appear", "Bearer leaked", "milestone.internal", "AB-1"} {
		if strings.Contains(text, leak) {
			t.Errorf("sanitized body still contains %q:\n%s", leak, text)
		}
	}
	if !strings.Contains(text, "e1") {
		t.Errorf("sanitizer removed non-sensitive content:\n%s", text)
	}

	// Headers are allow-listed, not scrubbed, so a new header is absent until
	// somebody decides it matters.
	if headers["content-type"] != "application/json" {
		t.Errorf("allow-listed header was dropped: %v", headers)
	}
	if headers["x-request-id"] != "req-42" {
		t.Errorf("allow-listed header was dropped: %v", headers)
	}
	for _, banned := range []string{"set-cookie", "authorization"} {
		if _, ok := headers[banned]; ok {
			t.Errorf("header %q was not allow-listed out", banned)
		}
	}
}

func TestScenarioNameRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{
		"../escape", "a/b", `a\b`, "..", ".", ".hidden", "a\x00b",
	} {
		store, err := fixture.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		sc := fixture.Scenario{
			FormatVersion: fixture.FormatVersion,
			Name:          name,
			Epoch:         testEpoch(),
			SiteID:        "s1",
			Events: []fixture.Event{{
				OffsetMS: 0, Kind: "/api/v1/Events", Method: "GET", Envelope: json.RawMessage(`{}`),
			}},
		}
		if _, err := store.WriteScenario(sc); err == nil {
			t.Errorf("scenario name %q was accepted as a file name", name)
		}
	}
}

func TestValidateRejectsMalformedFixtures(t *testing.T) {
	valid := func() fixture.Scenario {
		return fixture.Scenario{
			FormatVersion: fixture.FormatVersion,
			Name:          "ok",
			Epoch:         testEpoch(),
			SiteID:        "s1",
			Events: []fixture.Event{{
				OffsetMS: 0, Kind: "/api/v1/Events", Method: "GET", Envelope: json.RawMessage(`{}`),
			}},
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("a valid scenario was rejected: %v", err)
	}

	cases := map[string]func(*fixture.Scenario){
		"wrong format version": func(s *fixture.Scenario) { s.FormatVersion = 99 },
		"no name":              func(s *fixture.Scenario) { s.Name = "" },
		"no epoch":             func(s *fixture.Scenario) { s.Epoch = zeroTime() },
		"no site":              func(s *fixture.Scenario) { s.SiteID = "" },
		"no events":            func(s *fixture.Scenario) { s.Events = nil },
		"no method":            func(s *fixture.Scenario) { s.Events[0].Method = "" },
		"bad method":           func(s *fixture.Scenario) { s.Events[0].Method = "DELETE" },
		"no kind":              func(s *fixture.Scenario) { s.Events[0].Kind = "" },
		"empty envelope":       func(s *fixture.Scenario) { s.Events[0].Envelope = nil },
		"non-json envelope":    func(s *fixture.Scenario) { s.Events[0].Envelope = json.RawMessage(`{not json`) },
		"disallowed header":    func(s *fixture.Scenario) { s.Events[0].Headers = map[string]string{"set-cookie": "x"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sc := valid()
			mutate(&sc)
			if err := sc.Validate(); err == nil {
				t.Fatalf("a scenario with %s was accepted", name)
			}
		})
	}
}

func TestStoreRoundTripAndManifestVerification(t *testing.T) {
	dir := t.TempDir()
	store, err := fixture.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "round-trip",
		Epoch:         testEpoch(),
		SiteID:        "s1",
		Events: []fixture.Event{{
			OffsetMS: 0, Kind: "/api/v1/Events", Method: "GET", Envelope: json.RawMessage(`{"Id":"e1"}`),
		}},
	}
	entry, err := store.WriteScenario(sc)
	if err != nil {
		t.Fatalf("WriteScenario: %v", err)
	}
	if err := store.WriteManifest(fixture.Manifest{Scenarios: []fixture.ManifestEntry{entry}}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if _, err := store.VerifyManifest(); err != nil {
		t.Fatalf("a freshly written set failed verification: %v", err)
	}

	loaded, err := store.LoadScenario("round-trip")
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if loaded.Name != sc.Name || len(loaded.Events) != 1 {
		t.Errorf("round trip changed the scenario: %+v", loaded)
	}
}

// A fixture edited after review is invisible to a test that simply replays it.
// The manifest is what catches it.
func TestVerifyManifestDetectsPostReviewEdit(t *testing.T) {
	dir := t.TempDir()
	store, _ := fixture.NewStore(dir)
	sc := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "tampered",
		Epoch:         testEpoch(),
		SiteID:        "s1",
		Events: []fixture.Event{{
			OffsetMS: 0, Kind: "/api/v1/Events", Method: "GET", Envelope: json.RawMessage(`{"Id":"e1","Type":"Motion"}`),
		}},
	}
	entry, err := store.WriteScenario(sc)
	if err != nil {
		t.Fatalf("WriteScenario: %v", err)
	}
	if err := store.WriteManifest(fixture.Manifest{Scenarios: []fixture.ManifestEntry{entry}}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	path := filepath.Join(dir, "tampered.json")
	raw, _ := os.ReadFile(path)
	edited := strings.Replace(string(raw), `"Motion"`, `"Analytics"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = store.VerifyManifest()
	if err == nil {
		t.Fatal("an edited fixture verified against the manifest")
	}
	if !strings.Contains(err.Error(), "edited after review") {
		t.Errorf("error should name the drift, got %q", err)
	}
}

func TestVerifyManifestDetectsMissingAndExtraScenarios(t *testing.T) {
	dir := t.TempDir()
	store, _ := fixture.NewStore(dir)
	sc := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "present",
		Epoch:         testEpoch(),
		SiteID:        "s1",
		Events: []fixture.Event{{
			OffsetMS: 0, Kind: "/api/v1/Events", Method: "GET", Envelope: json.RawMessage(`{}`),
		}},
	}
	entry, _ := store.WriteScenario(sc)
	// A second entry the directory does not have.
	_ = store.WriteManifest(fixture.Manifest{Scenarios: []fixture.ManifestEntry{
		entry, {Name: "absent", File: "absent.json", Hash: entry.Hash, Events: 1},
	}})

	_, err := store.VerifyManifest()
	if err == nil {
		t.Fatal("a manifest listing a missing scenario verified")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("error should name the missing scenario, got %q", err)
	}
}

// The committed fixture set must be internally consistent, and must not claim to
// be a capture when it is not.
func TestCommittedFixtureSetVerifies(t *testing.T) {
	dir := repoFixtureDir(t)
	store, err := fixture.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	manifest, err := store.VerifyManifest()
	if err != nil {
		t.Fatalf("the committed fixture set does not match its manifest: %v", err)
	}
	if len(manifest.Scenarios) == 0 {
		t.Fatal("the committed fixture set is empty")
	}

	realCaptures := 0
	for _, e := range manifest.Scenarios {
		sc, err := store.LoadScenario(e.Name)
		if err != nil {
			t.Fatalf("LoadScenario(%s): %v", e.Name, err)
		}
		if sc.Source == "synthetic" {
			continue
		}
		realCaptures++
		if sc.CapturedAt.IsZero() {
			t.Errorf("scenario %q claims to be a capture but has no capture time", e.Name)
		}
	}
	if realCaptures == 0 && manifest.Source == "capture" {
		t.Error("the manifest claims to be captured but every scenario is synthetic")
	}
}

// Every behaviour the plan names must have a scenario that says it covers it.
// A required case with no fixture is a gap that is invisible until a pilot.
func TestFixtureSetCoversEveryRequiredCase(t *testing.T) {
	dir := repoFixtureDir(t)
	store, _ := fixture.NewStore(dir)
	manifest, _, err := store.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	required := []string{
		"motion", "analytics", "alarm.created", "alarm.acknowledged",
		"device.camera_offline", "device.camera_online", "bookmark",
		"recording.started", "metadata", "malformed", "duplicate",
		"delayed", "out-of-order", "missing-camera", "reconnect",
	}
	covered := map[string]bool{}
	for _, e := range manifest.Scenarios {
		for _, c := range e.Covers {
			covered[c] = true
		}
	}
	var missing []string
	for _, r := range required {
		if !covered[r] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the fixture set does not cover: %s", strings.Join(missing, ", "))
	}
}
