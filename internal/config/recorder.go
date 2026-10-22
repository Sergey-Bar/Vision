package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The recorder's configuration.
//
// # Why the recorder has its OWN loader
//
// The core's `Load` requires an evidence root, because a core that starts
// with no idea where evidence lives will accept evidence and put it somewhere
// it cannot find again. The recorder does not keep evidence — it keeps
// RECORDINGS, on the customer's own disk, and the root it needs is a
// different one.
//
// So the two binaries share the DATABASE policy and nothing else. A recorder
// that had to be configured with an evidence root it never writes to would be
// a recorder whose configuration says it stores something it does not.
//
// # No silent defaults
//
// Same rule as the core: a root with no value is a refusal naming the
// variable, not a default of "" and not the current directory. A recorder
// with no root writes its segments into a working directory nobody will ever
// find, and the site reports a recording it does not have.

// Recorder is the recorder service's configuration.
type Recorder struct {
	// Database is the connection policy. The password is not here: it goes
	// straight from `SP_DATABASE_PASSWORD` to `dbx.Open`, for the reason the
	// package comment gives.
	Database dbx.Config

	// TenantID and SiteID are the scope the recorder writes for.
	//
	// They come from the configuration and never from a request: a value a
	// caller sends is an instruction and not an authorisation.
	TenantID string
	SiteID   string

	// Root is where the recorded segments live, as `SP_RECORDING_ROOT`.
	//
	// It is a ROOT and not a path: a segment's name is built inside it, and
	// the reference the index carries is RELATIVE to it. A reference that
	// named an absolute path would be a path that could point anywhere the
	// process can read, and the index is read by a different process from
	// the one that wrote the bytes.
	Root string

	// Sources is the camera list, as `SP_RECORDER_SOURCES`.
	//
	// It is a PATH and not the list itself, because the list holds camera
	// credentials and a credential on Config is a credential somebody will
	// print.
	Sources string

	// SegmentDuration is the target length of one segment, as
	// `SP_RECORDER_SEGMENT_SECONDS`.
	//
	// Ten seconds. A segment small enough to find in a timeline, large
	// enough that the index does not become the largest thing in the
	// install.
	SegmentDuration time.Duration

	// RTSPTimeout bounds the connect and every request, as
	// `SP_RECORDER_RTSP_TIMEOUT`.
	//
	// A camera that accepts a connection and then stops answering must not
	// hold a recorder forever.
	RTSPTimeout time.Duration

	// RetryDelay is how long a recorder waits before reconnecting a camera
	// that failed, as `SP_RECORDER_RETRY_DELAY`.
	//
	// A camera that is rebooting comes back in seconds; one that is
	// unplugged comes back in hours, and a retry every second for an hour
	// is a log nobody reads.
	RetryDelay time.Duration

	// LogLevel is one of debug, info, warn, error.
	LogLevel string
}

// DefaultRecorderSegmentDuration is the target length of one segment.
const DefaultRecorderSegmentDuration = 10 * time.Second

// DefaultRecorderRetryDelay is how long a recorder waits before reconnecting.
const DefaultRecorderRetryDelay = 5 * time.Second

// DefaultRecorderRTSPTimeout bounds the connect and every request.
//
// Ten seconds, and it is the same bound the RTSP client declares for
// itself: a camera that accepts a connection and then stops answering must
// not hold a recorder forever.
const DefaultRecorderRTSPTimeout = 10 * time.Second

// LoadRecorder reads the recorder's configuration.
func LoadRecorder() (Recorder, error) { return LoadRecorderFrom(DefaultLookup) }

// LoadRecorderFrom reads the recorder's configuration through an injected
// lookup.
func LoadRecorderFrom(get Lookup) (Recorder, error) {
	if get == nil {
		get = DefaultLookup
	}
	var c Recorder

	c.Database = dbx.DefaultConfig()
	if dsn := strings.TrimSpace(get("SP_DATABASE_URL")); dsn != "" {
		if err := c.Database.ApplyDSN(dsn); err != nil {
			return c, fmt.Errorf("config: SP_DATABASE_URL: %w", err)
		}
	}

	c.Root = strings.TrimSpace(get("SP_RECORDING_ROOT"))
	c.Sources = strings.TrimSpace(get("SP_RECORDER_SOURCES"))
	c.TenantID = strings.TrimSpace(get("SP_TENANT_ID"))
	c.SiteID = strings.TrimSpace(get("SP_SITE_ID"))
	c.LogLevel = strings.ToLower(envOr(get, "SP_LOG_LEVEL", DefaultLogLevel))

	c.SegmentDuration = DefaultRecorderSegmentDuration
	if v := strings.TrimSpace(get("SP_RECORDER_SEGMENT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 3600 {
			return c, fmt.Errorf("config: SP_RECORDER_SEGMENT_SECONDS must be a whole number of "+
				"seconds between 1 and 3600, got %q", v)
		}
		c.SegmentDuration = time.Duration(n) * time.Second
	}
	c.RTSPTimeout = DefaultRecorderRTSPTimeout
	if v := strings.TrimSpace(get("SP_RECORDER_RTSP_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_RECORDER_RTSP_TIMEOUT: %w", err)
		}
		c.RTSPTimeout = d
	}
	c.RetryDelay = DefaultRecorderRetryDelay
	if v := strings.TrimSpace(get("SP_RECORDER_RETRY_DELAY")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_RECORDER_RETRY_DELAY: %w", err)
		}
		c.RetryDelay = d
	}

	if err := c.validate(); err != nil {
		return c, err
	}
	return c, nil
}

// validate refuses an incomplete recorder configuration.
func (c Recorder) validate() error {
	if c.Root == "" {
		return fmt.Errorf("config: SP_RECORDING_ROOT is required: the recorder writes the " +
			"recorded segments to the customer's own disk, and a recorder with no root " +
			"writes them into a working directory nobody will find again")
	}
	if !filepath.IsAbs(c.Root) && !strings.HasPrefix(c.Root, "/") {
		return fmt.Errorf("config: SP_RECORDING_ROOT must be an absolute path, got %q: a "+
			"relative root is relative to whatever directory the service unit happens "+
			"to start in", c.Root)
	}
	if c.Sources == "" {
		return fmt.Errorf("config: SP_RECORDER_SOURCES is required: the camera list names the " +
			"cameras to record, and a recorder with no list records nothing")
	}
	if c.TenantID == "" {
		return fmt.Errorf("config: SP_TENANT_ID is required: the recorder writes into the " +
			"tenant's recording index, and a row with no tenant is a row nobody can find")
	}
	return nil
}

// Sites returns the sites the recorder covers.
//
// It is the recorder's OWN scope and not the principal's: the recorder is not
// an operator with a site scope, it is a process that records the site it was
// installed for. An empty list means EVERY site, which is the single-site
// install's case and the one the device registry's reader expects.
//
// The site comes from the configuration and never from a request, for the
// reason every other scope in this product comes from configuration: a value
// a caller sends is an instruction and not an authorisation.
func (c Recorder) Sites() []string {
	site := strings.TrimSpace(c.SiteID)
	if site == "" {
		return nil
	}
	return []string{site}
}

// Redacted renders the recorder's configuration for the startup log.
//
// It names what is SET and never what is SECRET. The camera list is a path
// and not a list, so nothing that could carry a credential is here.
func (c Recorder) Redacted() map[string]any {
	return map[string]any{
		"database":         c.Database.DSN(),
		"recording_root":   c.Root,
		"recorder_sources": c.Sources,
		"segment_seconds":  c.SegmentDuration.Seconds(),
		"rtsp_timeout":     c.RTSPTimeout.String(),
		"retry_delay":      c.RetryDelay.String(),
	}
}

// DatabasePassword reads the database password.
//
// It is a function and not a field, for the reason the package comment gives:
// a field on Config is a field that somebody will print, and an environment
// variable survives into child processes and diagnostic dumps.
func DatabasePassword() string { return os.Getenv("SP_DATABASE_PASSWORD") }
