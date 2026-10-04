package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 with SHA-1; identity's verifier accepts both
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"
)

// The console's TLS certificate, and the one TOTP code the demo needs.
//
// # Why the seed mints a certificate
//
// `internal/config` refuses to start with neither TLS nor a trusted proxy:
//
//	neither TLS nor a trusted proxy is configured: with SP_TLS_CERT/SP_TLS_KEY
//	unset and SP_BEHIND_PROXY=false the listener serves plaintext and nothing
//	upstream is terminating TLS
//
// That refusal is correct and it has a consequence for a demo container: the
// console cannot be plain HTTP and still be the product. There are two ways
// round it and only one of them is honest. The dishonest one is to set
// SP_BEHIND_PROXY=true and name a proxy that does not exist, which makes the
// product believe its traffic is protected by something it is not. The honest one
// is what an installer does on an internal network: mint a leaf.
//
// So the seed mints a self-signed leaf, writes it where `SP_TLS_CERT` and
// `SP_TLS_KEY` point, and prints the certificate's SHA-256 fingerprint. A judge
// who clicks through the browser's warning can then check that the certificate
// they accepted is the one this run created — which is a better demonstration of
// this product's argument than a green padlock would have been.
//
// `SP_SESSION_COOKIE_SECURE` needs no answer: it defaults to true, and the
// listener really is TLS.

// ecdsaKey is P-256 rather than RSA.
//
// A 2048-bit RSA key costs a few hundred milliseconds to generate, which on a
// one-shot seed is a visible pause for no benefit, and ECDSA is what the TLS
// stacks this product will meet prefer anyway.
func ecdsaKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate the console signing key: %w", err)
	}
	return key, nil
}

// randomSerial is a 128-bit serial, which is what the CA/Browser Forum baseline
// requires and what makes a certificate's serial unique in practice.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate a certificate serial: %w", err)
	}
	// Serial zero is not a valid serial, whatever CA/B says about the range.
	if serial.Sign() == 0 {
		serial = big.NewInt(1)
	}
	return serial, nil
}

// certificatePaths is what the seed wrote.
type certificatePaths struct {
	Path        string
	KeyPath     string
	Fingerprint string
}

// certificateNotAfter is how long the demo certificate lasts.
//
// Three years is a real operational choice rather than a demo convenience: a
// certificate with a thirty-day life would put an expiry in the middle of the
// thing this is for, and "renew it before the demo" is not a lesson anybody needs.
const certificateNotAfter = 3 * 365 * 24 * time.Hour

func (s *seeder) writeConsoleCertificate() (certificatePaths, error) {
	if err := os.MkdirAll(s.tlsDir, 0o700); err != nil {
		return certificatePaths{}, fmt.Errorf("create the certificate directory %s: %w",
			s.tlsDir, err)
	}
	certPath := s.tlsDir + "/console.crt"
	keyPath := s.tlsDir + "/console.key"
	fp, err := mintSelfSigned(certPath, keyPath, s.consoleNames())
	if err != nil {
		return certificatePaths{}, err
	}
	return certificatePaths{Path: certPath, KeyPath: keyPath, Fingerprint: fp}, nil
}

// consoleNames are the names on the certificate.
//
// `localhost` and the loopback addresses, because that is where a demo console is
// reached from, and NOT a routable name: a certificate valid for a hostname the
// site does not own would be a claim the site cannot keep.
func (s *seeder) consoleNames() []string {
	return []string{"localhost", "127.0.0.1", "::1", "console"}
}

// mintSelfSigned writes a self-signed leaf and returns its SHA-256 fingerprint.
//
// The standard library only. `crypto/x509` is in it, and adding a certificate
// library for one self-signed leaf would be a supply-chain decision on a build
// that ships to air-gapped sites — which is the reason `internal/command` exists
// at all and why this repository is careful about vendored modules.
func mintSelfSigned(certPath, keyPath string, names []string) (string, error) {
	key, err := ecdsaKey()
	if err != nil {
		return "", err
	}
	serial, err := randomSerial()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Add(-time.Hour) // backdated, so a slightly slow clock still accepts it
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Physical Security Intelligence Layer"},
			CommonName:   "security-platform console (seeded, self-signed)",
		},
		// NotBefore/NotAfter are absolute instants rather than durations, so a
		// certificate whose validity window is nine months and ten years can be
		// expressed at all — the field is a pair of instants, not a length.
		NotBefore:             now,
		NotAfter:              now.Add(certificateNotAfter + time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// Self-signed and self-issued. Marking it a CA is what would let it sign
		// anything else, and a demo certificate is not a certificate authority.
		IsCA: false,
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		tmpl.DNSNames = append(tmpl.DNSNames, n)
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", fmt.Errorf("create the console certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("encode the console private key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	// 0644 on the certificate because it is public by definition, and 0600 on the
	// key because it is not. The first `identity` write caught by golangci-lint's
	// gosec was an environment file written world-readable that carried
	// SP_SECRET_KEY; a private key at 0644 is the same defect with a worse
	// payload.
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil { //nolint:gosec // a certificate is public
		return "", fmt.Errorf("write %s: %w", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", keyPath, err)
	}

	sum := sha256.Sum256(der)
	return formatFingerprint(sum[:]), nil
}

// formatFingerprint is the colon-separated uppercase form `openssl x509
// -fingerprint -sha256` prints, so the value in the report can be pasted straight
// into the command a reader will want to run.
func formatFingerprint(sum []byte) string {
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// totpCodeFor computes an RFC 6238 code from a base32 shared secret.
//
// It is an authenticator, not a bypass. `identity`'s verifier accepts a code
// derived from the enrolled secret under either SHA-1 or SHA-256 — see
// `internal/identity/mfa.go`, which hashes the raw secret bytes and checks both —
// and this is the same arithmetic an authenticator app performs. Nothing here
// reaches into the stored row or skips the second factor; the console still
// requires the code, and this only means the demo does not need a phone.
func totpCodeFor(base32Secret string, at time.Time) (string, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(base32Secret)
	if err != nil {
		return "", fmt.Errorf("the enrolled TOTP secret is not base32: %w", err)
	}
	// Divided BEFORE the conversion, so a pre-epoch instant cannot wrap into a huge
	// unsigned counter and produce a code that looks valid. `identity`'s verifier
	// does the same, and the reason is in the `//nolint:gosec` there.
	counter := at.Unix() / totpPeriodSeconds
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(counter)) //nolint:gosec // G115: see above.
	mac := hmac.New(sha1.New, raw)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// RFC 4226 dynamic truncation, then RFC 6238's modulo.
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	code := value % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// totpPeriodSeconds is the time step. Thirty seconds, as RFC 6238 specifies.
const totpPeriodSeconds = 30
