package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// The health probe, tested against a real socket.
//
// The claim being made is the narrow one a container depends on: this command
// exits 0 when the service is serving and non-zero when it is not, and it makes a
// real HTTP request either way. A probe that "works" by construction — by reading
// a file, or by trusting that the process it runs inside is alive — is exactly
// the kind of check this repository keeps finding.

// runProbe executes the command through the real command tree, so the registration
// in newRootCommand is exercised too. A test that called runHealthcheck directly
// would keep passing if the command were never registered.
func runProbe(t *testing.T, args ...string) (code command.ExitCode, stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	env := &command.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}
	code = newRootCommand().Execute(context.Background(), env, append([]string{"healthcheck"}, args...))
	return code, out.String(), errOut.String()
}

func TestTheProbePassesAgainstAServiceThatIsServing(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	code, stdout, stderr := runProbe(t, "--url", srv.URL)
	if code != command.ExitOK {
		t.Fatalf("exit %d against a serving service; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "/v1/health") {
		t.Errorf("the probe did not report the route it asked: %q", stdout)
	}
	if gotPath != "/v1/health" {
		t.Errorf("the probe asked for %q, want /v1/health", gotPath)
	}
}

func TestTheProbeFailsAndSaysWhyWhenTheServiceIsNotServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"reason":"the evidence volume is not writable"}`))
	}))
	defer srv.Close()

	code, _, stderr := runProbe(t, "--url", srv.URL)
	if code == command.ExitOK {
		t.Fatal("the probe exited 0 against a 503; a container healthcheck that cannot " +
			"fail is a container that stays up while it serves nothing")
	}
	// The reason has to survive into the output. A probe that reports "unhealthy"
	// with no reason is a restart loop nobody can diagnose.
	if !strings.Contains(stderr, "503") {
		t.Errorf("the failure did not report the status: %q", stderr)
	}
	if !strings.Contains(stderr, "evidence volume") {
		t.Errorf("the failure did not report what the service said: %q", stderr)
	}
}

// The stricter question has to be reachable. A probe that could only ever ask
// /v1/health could not fail on a service that is up and refusing work, which is
// the state a load balancer exists to keep out of rotation.
func TestTheProbeCanAskTheStricterQuestion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		switch r.URL.Path {
		case "/v1/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/health/ready":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Liveness passes and readiness fails on the same service, which is the
	// point: they are different questions and a probe has to be able to ask both.
	if code, _, stderr := runProbe(t, "--url", srv.URL); code != command.ExitOK {
		t.Fatalf("liveness exit %d; stderr: %s", code, stderr)
	}
	if code, _, _ := runProbe(t, "--url", srv.URL, "--ready"); code == command.ExitOK {
		t.Fatal("readiness exited 0 while /v1/health/ready answered 503")
	}
	if gotPath != "/v1/health/ready" {
		t.Errorf("--ready asked for %q", gotPath)
	}
}

// Nothing listening is the case a container restart loop is made of, and it is
// the one an implementation is most likely to turn into a hang.
func TestTheProbeFailsWhenNothingIsListening(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // the port is now closed, which is the state under test

	code, _, stderr := runProbe(t, "--url", url, "--timeout", "2s")
	if code == command.ExitOK {
		t.Fatal("the probe exited 0 with nothing listening")
	}
	if !strings.Contains(stderr, url) {
		t.Errorf("the failure did not name the URL it probed: %q", stderr)
	}
}

// A refused scheme is refused rather than guessed. `example.com:8443` parses as a
// URL with scheme "example.com", and a probe that quietly retried it as http would
// be reporting on a host the operator never named.
func TestTheProbeRefusesAURLItCannotSpeak(t *testing.T) {
	code, _, stderr := runProbe(t, "--url", "example.com:8443")
	if code != command.ExitRefused {
		t.Fatalf("exit %d for a scheme-less URL, want the refusal (%d); stderr: %s",
			code, command.ExitRefused, stderr)
	}
	if !strings.Contains(stderr, "http://") {
		t.Errorf("the refusal did not say what it wanted instead: %q", stderr)
	}
}

// The body is bounded. A probe that reads an unbounded response from a service
// that is failing loudly turns one line in a container log into a megabyte.
func TestTheProbeBoundsTheBodyItReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("E", 64*1024)))
	}))
	defer srv.Close()

	code, _, stderr := runProbe(t, "--url", srv.URL)
	if code == command.ExitOK {
		t.Fatal("the probe exited 0 against a 500")
	}
	if len(stderr) > 4*maxBodyBytes {
		t.Errorf("the probe reported %d bytes of body; it is bounded at %d",
			len(stderr), maxBodyBytes)
	}
}

// --ca is how the container healthcheck reaches a console serving a certificate
// the seed minted, and it is the whole reason the flag exists instead of an
// --insecure. So it has to do both halves: accept the certificate it was given,
// and refuse one it was not.
func TestTheProbeTrustsOnlyTheCertificateItWasGiven(t *testing.T) {
	trusted, trustedPair := serveSelfSigned(t)
	defer trusted.Close()
	imposter, _ := serveSelfSigned(t)
	defer imposter.Close()

	// The right certificate: the probe gets through.
	if code, _, stderr := runProbe(t, "--url", trusted.URL, "--ca", trustedPair.caPath); code != command.ExitOK {
		t.Fatalf("the probe refused the service it was told to trust; exit %d, stderr: %s",
			code, stderr)
	}

	// The wrong certificate, on a service that is otherwise perfectly healthy: the
	// probe must still fail. An impostor holding a valid-looking certificate is
	// exactly what --insecure would have waved through.
	code, _, stderr := runProbe(t, "--url", imposter.URL, "--ca", trustedPair.caPath)
	if code == command.ExitOK {
		t.Fatal("the probe accepted a certificate it was not given")
	}
	if !strings.Contains(stderr, "certificate") {
		t.Errorf("the refusal did not name the certificate as the reason: %q", stderr)
	}

	// And no --ca at all is equally refused: the system roots do not include a
	// self-signed leaf, which is the whole reason --ca has to be passed.
	if code, _, _ := runProbe(t, "--url", trusted.URL); code == command.ExitOK {
		t.Fatal("the probe accepted a self-signed certificate with no --ca given")
	}
}

// tlsPair is one generated certificate and the file it was written to.
type tlsPair struct {
	caPath string
	cert   tls.Certificate
}

// serveSelfSigned starts an HTTPS test server whose certificate was generated
// here, and writes that certificate to a PEM file. Two calls give two unrelated
// certificates, which is what the impostor case needs.
func serveSelfSigned(t *testing.T) (srv *httptest.Server, pair tlsPair) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create the certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("encode the key: %v", err)
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, leafPEM, 0o600); err != nil {
		t.Fatalf("write the CA: %v", err)
	}
	cert, err := tls.X509KeyPair(leafPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatalf("pair the key: %v", err)
	}

	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	return srv, tlsPair{caPath: caPath, cert: cert}
}

// A --ca that is not a certificate is refused rather than ignored. A probe that
// fell back to the system roots would be a probe that stopped checking what it
// was told to check, silently, which is the failure mode --insecure would have had
// and this flag exists to avoid.
func TestTheProbeRefusesACAFileWithNoCertificate(t *testing.T) {
	notCert := filepath.Join(t.TempDir(), "not-a-cert.pem")
	if err := os.WriteFile(notCert, []byte("this is not PEM\n"), 0o600); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	code, _, stderr := runProbe(t, "--url", "https://127.0.0.1:1", "--ca", notCert)
	if code != command.ExitRefused {
		t.Fatalf("exit %d for a CA file with no certificate in it, want the refusal (%d); stderr: %s",
			code, command.ExitRefused, stderr)
	}
	if !strings.Contains(stderr, "no PEM certificate") {
		t.Errorf("the refusal did not say what was wrong with the file: %q", stderr)
	}
}
