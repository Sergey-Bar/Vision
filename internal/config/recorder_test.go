package config_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/config"
)

// The recorder's configuration.
//
// The rules are the core's rules: a value whose absence would change
// behaviour is a REFUSAL naming the variable, and a value that is read is
// read through one function so that a test and the daemon see the same
// number.

func recorderEnv(pairs map[string]any) config.Lookup {
	return func(key string) string {
		v, ok := pairs[key]
		if !ok {
			return ""
		}
		switch t := v.(type) {
		case string:
			return t
		case int:
			return strconv.Itoa(t)
		default:
			return ""
		}
	}
}

func TestTheRecorderRefusesAnInstallWithNoRecordingRoot(t *testing.T) {
	// A recorder with no root writes its segments into a working directory
	// nobody will find again, and the site reports a recording it does not
	// have. So it is a refusal that NAMES the variable.
	_, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
		"SP_RECORDER_SOURCES": "/etc/security-platform/cameras.json",
		"SP_TENANT_ID":        "acme",
	}))
	if err == nil {
		t.Fatal("a recorder with no recording root started")
	}
	if !strings.Contains(err.Error(), "SP_RECORDING_ROOT") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

func TestTheRecorderRefusesARelativeRecordingRoot(t *testing.T) {
	// A relative root is relative to whatever directory the service unit
	// happens to start in, which is a property of the machine rather than a
	// decision.
	_, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
		"SP_RECORDING_ROOT":   "recordings",
		"SP_RECORDER_SOURCES": "/etc/security-platform/cameras.json",
		"SP_TENANT_ID":        "acme",
	}))
	if err == nil {
		t.Fatal("a recorder with a relative recording root started")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestTheRecorderRefusesAnInstallWithNoCameraList(t *testing.T) {
	_, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
		"SP_RECORDING_ROOT": "/var/lib/security-platform/recordings",
		"SP_TENANT_ID":      "acme",
	}))
	if err == nil {
		t.Fatal("a recorder with no camera list started")
	}
	if !strings.Contains(err.Error(), "SP_RECORDER_SOURCES") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

func TestTheRecorderDefaultsAreTheDocumentedOnes(t *testing.T) {
	c, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
		"SP_RECORDING_ROOT":   "/var/lib/security-platform/recordings",
		"SP_RECORDER_SOURCES": "/etc/security-platform/cameras.json",
		"SP_TENANT_ID":        "acme",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.SegmentDuration != config.DefaultRecorderSegmentDuration {
		t.Errorf("the segment duration is %s, want the published default %s",
			c.SegmentDuration, config.DefaultRecorderSegmentDuration)
	}
	if c.RetryDelay != config.DefaultRecorderRetryDelay {
		t.Errorf("the retry delay is %s, want the published default %s",
			c.RetryDelay, config.DefaultRecorderRetryDelay)
	}
	if c.RTSPTimeout != config.DefaultRecorderRTSPTimeout {
		t.Errorf("the RTSP timeout is %s, want the published default %s",
			c.RTSPTimeout, config.DefaultRecorderRTSPTimeout)
	}
}

func TestTheRecorderReadsItsValues(t *testing.T) {
	c, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
		"SP_RECORDING_ROOT":           "/var/lib/security-platform/recordings",
		"SP_RECORDER_SOURCES":         "/etc/security-platform/cameras.json",
		"SP_RECORDER_SEGMENT_SECONDS": "30",
		"SP_RECORDER_RETRY_DELAY":     "20s",
		"SP_RECORDER_RTSP_TIMEOUT":    "3s",
		"SP_TENANT_ID":                "acme",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.SegmentDuration != 30*time.Second {
		t.Errorf("the segment duration is %s, want 30s", c.SegmentDuration)
	}
	if c.RetryDelay != 20*time.Second {
		t.Errorf("the retry delay is %s, want 20s", c.RetryDelay)
	}
	if c.RTSPTimeout != 3*time.Second {
		t.Errorf("the RTSP timeout is %s, want 3s", c.RTSPTimeout)
	}
}

func TestTheRecorderRefusesANonsensicalSegmentLength(t *testing.T) {
	// A segment of zero seconds is a segment per frame, and a segment of a
	// day is a segment nobody finds. Both are refusals with the variable
	// named.
	for _, v := range []string{"0", "-1", "abc", "99999"} {
		_, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
			"SP_RECORDING_ROOT":           "/var/lib/security-platform/recordings",
			"SP_RECORDER_SOURCES":         "/etc/security-platform/cameras.json",
			"SP_RECORDER_SEGMENT_SECONDS": v,
			"SP_TENANT_ID":                "acme",
		}))
		if err == nil {
			t.Errorf("a segment length of %q was accepted", v)
			continue
		}
		if !strings.Contains(err.Error(), "SP_RECORDER_SEGMENT_SECONDS") {
			t.Errorf("the refusal for %q does not name the variable: %v", v, err)
		}
	}
}

func TestTheRenderedRecorderConfigurationCarriesNoSecret(t *testing.T) {
	// The startup log prints the redacted configuration, so it must not
	// carry a password. The camera list is a PATH, and the database policy
	// renders its DSN without the password — the same rule the core's
	// redacted configuration keeps.
	c, err := config.LoadRecorderFrom(recorderEnv(map[string]any{
		"SP_RECORDING_ROOT":   "/var/lib/security-platform/recordings",
		"SP_RECORDER_SOURCES": "/etc/security-platform/cameras.json",
		"SP_TENANT_ID":        "acme",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for key, value := range c.Redacted() {
		if s, ok := value.(string); ok {
			if strings.Contains(s, "hunter2") {
				t.Errorf("the rendered %q carries the password", key)
			}
		}
	}
}
