package milestone_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/emulator"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// TestTheEmulatorAndTheAdapterAgreeOnThePrefix is the pin between the two
// sides of the wire.
//
// The emulator writes the prefix out in its own source, because it is the other
// end of the wire and a device does not import the platform's adapter. The
// adapter writes it out in its own source, because an adapter does not import
// the device.
//
// Two literals that must be equal are exactly the case where a test that only
// reads one of them proves nothing -- so this test reads BOTH and fails when
// they disagree. It lives here rather than in the emulator's package because
// the rule this repository keeps is that a package outside the composition root
// does not import the vendor's package, and the emulator is deliberately on the
// other side of that seam.
//
// The failure this pins is not hypothetical. The product's default base URL
// carried the prefix as well, so every poll asked for `/api/api/rest/v1/events`
// and the adapter reported a quiet site rather than a wrong URL -- which is the
// worst of both, because the symptom is a working system that sees nothing.
func TestTheEmulatorAndTheAdapterAgreeOnThePrefix(t *testing.T) {
	if milestone.APIPrefix != emulator.APIPrefix {
		t.Fatalf("the adapter's prefix is %q and the emulator's is %q. They are two literals "+
			"that have to be equal: the adapter joins its base with a route path that already "+
			"begins with the prefix, and a disagreement asks for a path that does not exist",
			milestone.APIPrefix, emulator.APIPrefix)
	}
}

// TestEveryRouteTheAdapterReadsIsAnsweredByTheEmulator is the other half of
// the pin: not that the prefixes agree, but that the two sides agree about
// WHICH ROUTES there are.
//
// The route table is the vendor's vocabulary and the emulator is the vendor, so
// the check belongs here rather than in the emulator's own package. A route the
// adapter reads and the emulator does not serve is a 404 in production and a
// "quiet site" in the adapter's report -- which is the shape of a working
// system that sees nothing.
//
// The write-side routes are skipped: reaching one means writing to the
// customer's recording, and a test that did that on every run would be a test
// nobody ran twice.
func TestEveryRouteTheAdapterReadsIsAnsweredByTheEmulator(t *testing.T) {
	engine, err := emulator.New(emulator.Options{
		Listen:  "127.0.0.1:0",
		Token:   "pin-token",
		Sources: []emulator.Source{{ID: "entrance", Name: "Entrance", Kind: emulator.KindFile, Location: "test.mp4"}},
	})
	if err != nil {
		t.Fatalf("build the emulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = engine.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(5 * time.Second)
	for engine.URL() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	base := engine.URL()
	if base == "" {
		t.Fatal("the emulator never reported its URL")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, route := range milestone.Routes {
		if route.WriteBack {
			continue
		}
		t.Run(route.Path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, base+route.Path, nil)
			if err != nil {
				t.Fatalf("build the request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer pin-token")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("reach %s: %v", route.Path, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("%s answered 404. The adapter reads that route, so in production it "+
					"would be a quiet site rather than an error", route.Path)
			}
		})
	}
}
