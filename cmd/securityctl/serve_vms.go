package main

// `serve-vms` — a long-running scripted Milestone endpoint, for criterion 09.
//
// # What it is
//
// An HTTP server that answers the nine routes in `milestone.Routes` and the
// bookmark write, with the vendor's own envelope shapes: `{array, eventCount,
// _links}` for the feed, four tolerated spellings for the clock, and an
// `Idempotency-Key` header on every write.
//
// # What it is not
//
// Not a Milestone installation, and not a recorder. It cannot show you the second
// because it has no second. What it does is close the loop the product controls —
// the decision, the drift correction, the pre-roll arithmetic, the idempotency key
// and the write — and let the acceptance report measure that the resulting window
// CONTAINS the incident, with lead-in, in milliseconds rather than in the thirty
// seconds the criterion budgets.
//
// # Why the idempotency behaviour is not optional
//
// A bookmark write that is retried after a timeout must not create a second
// recording. This server refuses a write with no `Idempotency-Key` and returns the
// ORIGINAL reference for a repeated one, so a client that forgot the key cannot
// pass against it. That is the whole reason a scripted endpoint is worth having:
// the refusal is only interesting if the peer enforces it.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

func serveVMSCommand() *command.Command {
	return &command.Command{
		Name:    "serve-vms",
		Summary: "Serve a scripted Milestone endpoint for criterion 09",
		Usage: `serve-vms [--listen <addr>] [--token <bearer>] [--events <n>] [--latency <d>]

Answers the vendor routes the adapter reads and the bookmark write it makes, with
the vendor's own envelope shapes and a real idempotency check.

  --listen    host:port. Port 0 asks for one and the URL printed is the real one.
  --token     the Bearer credential every request must carry. Set it to exercise
              the credential path; leave it empty and the server refuses
              everything, which is the safe default for something on a socket.
  --events    how many synthetic events the feed serves. Default 12.
  --latency   how long each response takes. Non-zero shows the thirty-second
              budget being spent rather than assumed.

Prints the base URL, then every request and every bookmark written, then exits on
Ctrl-C.`,
		Run: runServeVMS,
	}
}

func runServeVMS(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("serve-vms", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	listen := fs.String("listen", "127.0.0.1:0", "host:port to listen on.")
	token := fs.String("token", "", "Bearer credential every request must carry.")
	events := fs.Int("events", 12, "How many synthetic events the feed serves.")
	latency := fs.Duration("latency", 0, "How long each response takes.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *events < 1 {
		return command.Refuse("--events %d is not a count; the feed would be empty and an empty "+
			"feed is a contradiction a probe would report as one", *events)
	}

	// Fixed base instant, so a demo of this server is repeatable: the events are at
	// known offsets from a known start, not at whatever the wall clock was.
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	feed := make([]milestone.LiveEvent, 0, *events)
	for i := 0; i < *events; i++ {
		feed = append(feed, milestone.LiveEvent{
			ID:       fmt.Sprintf("evt-live-%03d", i+1),
			Type:     "Motion",
			DeviceID: "e028f597-c64b-4f27-bb8d-08f5cd768e1c",
			Value:    fmt.Sprintf("%.3f", 0.80+0.01*float64(i)),
			At:       base.Add(time.Duration(i) * 500 * time.Millisecond),
		})
	}

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := milestone.StartLiveServer(serveCtx, milestone.LiveOptions{
		Listen: *listen, Token: *token, Latency: *latency, Events: feed,
	})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	started := time.Now()
	fmt.Fprintf(env.Stdout, "A SCRIPTED VENDOR ENDPOINT, NOT A MILESTONE INSTALLATION\n")
	fmt.Fprintf(env.Stdout, "  base URL   %s\n", srv.URL())
	fmt.Fprintf(env.Stdout, "  routes     %d from milestone.Routes, plus the bookmark write\n", len(milestone.Routes))
	fmt.Fprintf(env.Stdout, "  events     %d, from %s\n", len(feed), base.Format(time.RFC3339))
	fmt.Fprintf(env.Stdout, "  credential %s\n", credentialLine(*token))
	fmt.Fprintf(env.Stdout, "  latency    %s per response\n\n", *latency)

	// One command a reader can paste, which is the difference between a server and
	// a thing that can be demonstrated.
	fmt.Fprintf(env.Stdout, "  securityctl adapter-probe --endpoint %s\n", srv.URL())
	fmt.Fprintf(env.Stdout, "  securityctl acceptance\n\n")
	fmt.Fprintf(env.Stdout, "Ctrl-C to stop.\n\n")

	// Every route's Unverified flag is still true. Nothing this command does can
	// change that, and saying so here is cheaper than a reader assuming the server
	// promoted its own routes.
	fmt.Fprintf(env.Stdout, "  note: all %d routes remain Unverified. This server does not\n", len(milestone.UnverifiedRoutes()))
	fmt.Fprintf(env.Stdout, "        promote a route; only a probe against a real device can.\n\n")

	seen := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-serveCtx.Done():
			fmt.Fprintf(env.Stdout, "\nstopped after %s\n", time.Since(started).Round(time.Millisecond))
			fmt.Fprintf(env.Stdout, "requests: %s\n", summariseRequests(srv.Requests()))
			for _, b := range srv.Placed() {
				replay := ""
				if b.Replayed {
					replay = "  (a replay: the same idempotency key, the same recording)"
				}
				fmt.Fprintf(env.Stdout, "bookmark %s  %s .. %s%s\n", b.Reference,
					b.Begin.Format(time.RFC3339), b.End.Format(time.RFC3339), replay)
			}
			return nil
		case <-ticker.C:
			reqs := srv.Requests()
			if len(reqs) > seen {
				for _, r := range reqs[seen:] {
					fmt.Fprintf(env.Stdout, "  <- %s\n", r)
				}
				seen = len(reqs)
			}
			if placed := srv.Placed(); len(placed) > seen {
				seen = len(reqs)
				for _, b := range placed[len(placed)-1:] {
					fmt.Fprintf(env.Stdout, "  -> bookmark %s covering %s .. %s\n",
						b.Reference, b.Begin.Format(time.RFC3339), b.End.Format(time.RFC3339))
				}
			}
		}
	}
}

func credentialLine(token string) string {
	if token == "" {
		return "none — every request is answered 401. Set --token to exercise the path."
	}
	// Not the value: this is printed to a terminal that may be a screen share.
	return "Bearer token required"
}

// summariseRequests collapses a request list to what a reader needs: which methods
// were used, in order of first appearance.
func summariseRequests(reqs []string) string {
	if len(reqs) == 0 {
		return "none"
	}
	var order []string
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, r := range reqs {
		method, _, _ := strings.Cut(r, " ")
		counts[method]++
		if !seen[method] {
			seen[method] = true
			order = append(order, method)
		}
	}
	parts := make([]string, 0, len(order))
	for _, m := range order {
		parts = append(parts, fmt.Sprintf("%s x%d", m, counts[m]))
	}
	return strings.Join(parts, ", ")
}
