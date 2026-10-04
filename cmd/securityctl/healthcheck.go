package main

// The in-binary health probe, and why it is a command rather than a comment.
//
// The product image is `gcr.io/distroless/static-debian12`, which has no shell, no
// `wget`, no `curl` and no `/bin/busybox/sh` — verified rather than assumed: an
// attempt to run `/busybox/sh` in that image fails with
// `stat /busybox/sh: no such file or directory`. A container healthcheck is a
// command run *inside* the container, so on a distroless base there is exactly one
// thing it can be: a binary that is already in the image.
//
// So this is the same move `grpc_health_probe` makes for a gRPC server: a small
// program whose only job is to ask the service whether it is serving and to exit
// 0 or not. The alternative was to abandon distroless for a base with a shell,
// which costs the point of the image — a static binary on a base with no package
// manager, no shell, and no `/etc/passwd` entry to get wrong.
//
// # What it does NOT do
//
// It does not read `/v1/health/ready` unless asked. `/v1/health` answers "this
// process is up and these are the checks it knows about"; `/v1/health/ready` is
// the stricter one and it exists so a load balancer and an operator can ask
// different questions. `--ready` selects it, because a probe that could only ever
// ask the weaker question would be a probe that cannot fail.
//
// And there is no `--insecure`. A demo console serves a self-signed certificate
// because an install has to mint one, and the obvious shortcut is a flag that
// turns verification off. `--ca` is the honest form: name the certificate you
// expect, trust only that one, and keep verification on. A probe in a security
// product that cannot verify its peer is a probe that will report an impostor
// holding a valid certificate as the healthy service.
//
// # The failure is loud and bounded
//
// It returns the status and a bounded prefix of the body as the error, which the
// command tree prints once. A health check that failed silently becomes a
// container restarted for a reason nobody can read, and the body prefix is enough
// to name a reason code or a failing readiness check without putting an unbounded
// response into the runtime's log.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// maxBodyBytes is how much of an unhealthy response is reported.
//
// Bounded because a probe's output goes to the container runtime's log, which is
// not somewhere an unbounded body belongs. 512 bytes is enough to name a reason
// code or a failing readiness check.
const maxBodyBytes = 512

// healthcheckCommand is the probe.
//
// `--url` rather than no flag, because the console and the core are the same
// binary on different ports and a probe that could only address one of them is a
// probe somebody has to work around.
func healthcheckCommand() *command.Command {
	return &command.Command{
		Name:    "healthcheck",
		Summary: "Ask a running core whether it is serving, and exit 0 or not",
		Usage: `healthcheck [--url <base>] [--ready] [--ca <file>] [--timeout <duration>]

Exits 0 when the service answered 2xx and 1 otherwise, reporting the status and a
bounded prefix of the body when it did not. Intended as a container HEALTHCHECK on
a distroless image, where there is no shell to run curl from.

  --url      base URL of the running service. Default http://127.0.0.1:8443
  --ready    ask /v1/health/ready rather than /v1/health
  --ca       PEM file to trust as the only root, for a console with a self-signed
             or internal-CA certificate. There is deliberately no --insecure: a
             security product whose own probe cannot verify a certificate is a
             probe that will happily report the wrong server as healthy.
  --timeout  client deadline for one probe. Default 3s`,
		Run: runHealthcheck,
	}
}

func runHealthcheck(ctx context.Context, env *command.Env, args []string) error {
	// The handler owns its FlagSet, which is the shape `Execute` documents for a
	// command that needs its flags to actually arrive.
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	base := fs.String("url", "http://127.0.0.1:8443", "Base URL of the running service.")
	ready := fs.Bool("ready", false, "Ask the readiness endpoint rather than the liveness one.")
	caFile := fs.String("ca", "", "PEM file to trust as the only root.")
	timeout := fs.Duration("timeout", 3*time.Second, "Client deadline for one probe.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	target := strings.TrimRight(strings.TrimSpace(*base), "/") + healthPath(*ready)
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		return command.Refuse("--url %q is not an http:// or https:// URL; a probe that "+
			"guessed a scheme would be a probe that could report a healthy service it "+
			"never reached", *base)
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(*caFile) != "" {
		pem, err := os.ReadFile(*caFile)
		if err != nil {
			return command.Refuse("read the CA at %s: %v", *caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return command.Refuse("the CA at %s contains no PEM certificate. A probe that fell "+
				"back to the system roots here would be a probe that silently stops "+
				"checking what it was told to check", *caFile)
		}
		// Roots only. The point of --ca is that THIS certificate is the one, and a
		// pool with the system roots in it too would accept any publicly trusted
		// certificate for the same name — which is the attack --ca exists to
		// prevent, an impostor with a Let's Encrypt certificate.
		tlsCfg.RootCAs = pool
	}

	probeCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build the probe request for %s: %w", target, err)
	}
	client := &http.Client{
		Timeout: *timeout,
		// Redirects are refused rather than followed. A probe that follows one is a
		// probe whose success says nothing about the URL it was given.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			// Never a proxy: the question is whether THIS process is serving.
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: *timeout}).DialContext,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: *timeout,
			TLSClientConfig:     tlsCfg,
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s did not answer: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	prefix := strings.TrimSpace(string(body))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Fprintf(env.Stdout, "ok %s %d\n", target, resp.StatusCode)
		return nil
	}
	if prefix != "" {
		return fmt.Errorf("%s answered %s: %s", target, resp.Status, prefix)
	}
	return fmt.Errorf("%s answered %s", target, resp.Status)
}

// healthPath is the route, and the only difference between the two questions.
//
// `/v1/health` and `/v1/health/ready` are both registered in `internal/api` and
// neither is `/healthz`, which is what a reader of this file will otherwise try.
func healthPath(ready bool) string {
	if ready {
		return "/v1/health/ready"
	}
	return "/v1/health"
}
