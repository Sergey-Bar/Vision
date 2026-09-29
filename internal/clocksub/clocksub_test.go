package clocksub_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/clocksub"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

var reference = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// registerDevice adds a device to a tenant. Measure refuses to record drift for
// a device the site has never registered, which is right: a number nobody can act
// on is not a measurement.
func registerDevice(t *testing.T, tenant, site, device string) {
	t.Helper()
	testutil.Exec(t, testutil.Pool(t),
		`INSERT INTO devices (tenant_id, device_id, site_id, kind) VALUES ($1, $2, $3, 'camera')`,
		tenant, device, site)
}

func open(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t) // skips, rather than fails, with no database
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func service(t *testing.T, pool *dbx.Pool) *clocksub.Service {
	t.Helper()
	return &clocksub.Service{Pool: pool, Reference: clock.SourceNTP, Now: func() time.Time { return reference }}
}

// A device whose clock is 30 days fast is not producing a large number. It is
// producing a number that means "this clock is not usable", and letting the
// int32 conversion wrap turns a 30-day error into a small one that the severe
// band then reads as a healthy device. The drift measurement is the input to
// every time-windowed corroboration decision in the product, so the direction of
// the error is the whole point.
func TestAHugeDriftSaturatesRatherThanWrapping(t *testing.T) {
	pool := open(t)
	svc := service(t, pool)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	for _, d := range []string{"cam-wild", "cam-wild-back", "cam-warn", "cam-severe", "cam-silent"} {
		registerDevice(t, tenant, site, d)
	}

	state, err := svc.Measure(context.Background(), clock.DefaultPolicy(), clocksub.Observation{
		TenantID: tenant, DeviceID: "cam-wild", SiteID: site,
		Reported:    reference.Add(400 * 24 * time.Hour),
		Source:      clock.SourceHost,
		ReferenceID: "site-reference",
	})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if state.DriftMs <= 0 {
		t.Fatalf("a device 400 days fast is recorded as %dms of drift. The conversion to "+
			"int32 wrapped, and a wrapped drift reads as a small one", state.DriftMs)
	}
	// Exactly, not approximately. 400 days is 34,560,000,000ms, which is not a
	// multiple of 2^32: the narrow conversion lands on 200,261,632 - a positive
	// number, in the severe band, and therefore looking entirely plausible while
	// being wrong by a factor of about 172. A test that only asked "is it
	// positive and severe" would pass on the broken code, which is the whole
	// reason this asserts the value rather than the band.
	const ceiling = int32(1<<31 - 1)
	if state.DriftMs != ceiling {
		t.Errorf("drift is %dms, want %d. 400 days does not divide by 2^32, so the narrow "+
			"conversion does not produce a negative number — it produces a plausible-looking "+
			"one that is wrong by two orders of magnitude", state.DriftMs, ceiling)
	}
	if state.Band != clock.BandSevere {
		t.Errorf("a device 400 days fast is in band %q, want %q. A wrapped value is small, and "+
			"a small drift is inside the normal band, so the device stays in time-windowed "+
			"corroboration it should be excluded from", state.Band, clock.BandSevere)
	}

	// The same in the other direction.
	back, err := svc.Measure(context.Background(), clock.DefaultPolicy(), clocksub.Observation{
		TenantID: tenant, DeviceID: "cam-wild-back", SiteID: site,
		Reported:    reference.Add(-400 * 24 * time.Hour),
		Source:      clock.SourceHost,
		ReferenceID: "site-reference",
	})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if back.DriftMs != -int32(1<<31-1) {
		t.Errorf("a device 400 days slow is recorded as %dms, want the negative ceiling. "+
			"Saturating has to work in both directions or a clock that is behind is "+
			"recorded as one that is ahead", back.DriftMs)
	}
	if back.Band != clock.BandSevere {
		t.Errorf("a device 400 days slow is recorded as %dms in band %q; the conversion "+
			"wrapped in the negative direction too", back.DriftMs, back.Band)
	}
}

// The ordinary case, which the clamp must not disturb: a signed drift, the right
// band, and a stored row a later read can find.
func TestAnOrdinaryDriftIsMeasuredStoredAndReadBack(t *testing.T) {
	pool := open(t)
	svc := service(t, pool)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	for _, d := range []string{"cam-wild", "cam-wild-back", "cam-warn", "cam-severe", "cam-silent"} {
		registerDevice(t, tenant, site, d)
	}
	ctx := context.Background()

	// The default policy's severe band is 10s, so 4s is warn and 40s is severe.
	state, err := svc.Measure(ctx, clock.DefaultPolicy(), clocksub.Observation{
		TenantID: tenant, DeviceID: "cam-warn", SiteID: site,
		Reported: reference.Add(4 * time.Second), Source: clock.SourceHost,
		ReferenceID: "site-reference",
	})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if state.DriftMs != 4000 {
		t.Errorf("drift is %dms, want 4000", state.DriftMs)
	}
	if state.Band != clock.BandWarn {
		t.Errorf("band is %q, want %q", state.Band, clock.BandWarn)
	}

	severe, err := svc.Measure(ctx, clock.DefaultPolicy(), clocksub.Observation{
		TenantID: tenant, DeviceID: "cam-severe", SiteID: site,
		Reported: reference.Add(-40 * time.Second), Source: clock.SourceHost,
		ReferenceID: "site-reference",
	})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if severe.DriftMs != -40000 {
		t.Errorf("drift is %dms, want -40000; a device whose clock is behind is corrected "+
			"the other way", severe.DriftMs)
	}
	if severe.Band != clock.BandSevere {
		t.Errorf("band is %q, want %q", severe.Band, clock.BandSevere)
	}

	// The severe count is what the console and the rules read, so a device that
	// is excluded from time-windowed corroboration has to be countable.
	if n, err := svc.SevereCount(ctx, tenant); err != nil {
		t.Fatalf("SevereCount: %v", err)
	} else if n != 1 {
		t.Errorf("the tenant has %d severe devices, want 1", n)
	}

	got, found, err := svc.State(ctx, tenant, "cam-severe")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !found {
		t.Fatal("a measured device is not readable back")
	}
	if got.DriftMs != -40000 {
		t.Errorf("the stored drift is %dms, want -40000", got.DriftMs)
	}
}

// A device that has never reported a time cannot be measured, and saying so is
// better than storing a drift of zero - which would look like a perfectly good
// clock and put the device straight back into corroboration.
func TestADeviceThatHasNotReportedIsRefused(t *testing.T) {
	pool := open(t)
	svc := service(t, pool)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	for _, d := range []string{"cam-wild", "cam-wild-back", "cam-warn", "cam-severe", "cam-silent"} {
		registerDevice(t, tenant, site, d)
	}

	if _, err := svc.Measure(context.Background(), clock.DefaultPolicy(), clocksub.Observation{
		TenantID: tenant, DeviceID: "cam-silent", SiteID: site,
		Source: clock.SourceUnknown, ReferenceID: "site-reference",
	}); err == nil {
		t.Fatal("a device with no reported time was measured. A drift of zero would put it " +
			"into time-windowed corroboration it has no clock for")
	}
}

// A VMS is not a device, and its drift is the drift of every event it produced -
// which is why it is stored under a reserved id rather than in a second table.
func TestASourceSystemIsStoredUnderAReservedID(t *testing.T) {
	pool := open(t)
	svc := service(t, pool)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	for _, d := range []string{"cam-wild", "cam-wild-back", "cam-warn", "cam-severe", "cam-silent"} {
		registerDevice(t, tenant, site, d)
	}
	ctx := context.Background()

	state, err := svc.MeasureFromSourceTimes(ctx, clock.DefaultPolicy(),
		tenant, site, "int-vms", "site-reference",
		reference.Add(40*time.Second), reference)
	if err != nil {
		t.Fatalf("MeasureFromSourceTimes: %v", err)
	}
	if state.DeviceID != "source:int-vms" {
		t.Errorf("the VMS is stored under %q, want the reserved id", state.DeviceID)
	}
	if state.DriftMs != 40000 || state.Band != clock.BandSevere {
		t.Errorf("a VMS 40s fast is %dms in band %q; the system is the clock, so this is every "+
			"event it produced", state.DriftMs, state.Band)
	}
	if _, found, err := svc.State(ctx, tenant, "source:int-vms"); err != nil {
		t.Fatalf("State: %v", err)
	} else if !found {
		t.Error("the VMS is not readable back under its reserved id")
	}
}
