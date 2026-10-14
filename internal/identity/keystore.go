package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// FileSecretStore wraps MFA secrets with AES-256-GCM under a key read from a
// file.
//
// It exists because SecretStore is an interface precisely so this package never
// holds a key, and until now nothing implemented it - which meant a running core
// could not enrol an operator's TOTP factor at all, and `identity.unwrapTOTP`
// returned "no secret store is configured" for every login attempt. The tests
// used a reversible prefix, so the production path had never been written.
//
// # The key
//
// A 32-byte key in a file the installer creates at 0400 and the core reads.
// Not an environment variable: an environment variable is inherited by every
// child process, is visible in /proc/<pid>/environ to anything that can read
// the process, and routinely ends up in a diagnostic dump. The file is opened
// per operation rather than held, so the key is not resident for the life of the
// process.
//
// # AAD
//
// Every ciphertext is bound to the tenant, the user and the factor. A wrapped
// secret is therefore useless if copied into another row: an attacker who can
// write to `mfa_factors` cannot replay Alice's secret as Bob's. GCM authenticates
// the AAD, so a mismatch fails closed rather than decrypting to something.
//
// # What it refuses
//
// A short key, a wrong-length key, a readable-by-others key file on a platform
// whose mode bits enforce it, and a ciphertext this store did not produce. Each
// is refused with the reason, because "could not unwrap the secret" with no
// further detail is a dead end for whoever is locked out. The world-readable key
// file is a warning rather than a refusal on Windows, where the mode is
// advisory and the ACL is the control -- see checkPermissions.
type FileSecretStore struct {
	keyPath string
}

// NewFileSecretStore builds a store over a key file.
func NewFileSecretStore(keyPath string) (*FileSecretStore, error) {
	if strings.TrimSpace(keyPath) == "" {
		return nil, errors.New("identity: a secret key path is required")
	}
	return &FileSecretStore{keyPath: keyPath}, nil
}

// KeySize is the AES-256 key length.
const KeySize = 32

// GenerateSecretKey writes a fresh key to path with restrictive permissions.
//
// It is idempotent in the way that matters: it refuses to overwrite an existing
// key. Rotating the key by overwriting it would make every enrolled factor
// permanently undecryptable, and an operator discovering that during a rotation
// is the worst moment to discover it.
func GenerateSecretKey(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("identity: a secret key path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("identity: %s already exists; overwriting it would make every "+
			"enrolled factor permanently undecryptable", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("identity: create the key directory: %w", err)
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("identity: read randomness for the secret key: %w", err)
	}
	// 0400 on Unix. On Windows the mode is advisory and the ACL is what matters;
	// the check in `key` is therefore advisory there too rather than a false
	// assurance, which is stated rather than hidden.
	if err := os.WriteFile(path, key, 0o400); err != nil {
		return fmt.Errorf("identity: write the secret key: %w", err)
	}
	return nil
}

// WrapFor encrypts a secret with the row it belongs to bound in.
//
// The binding is the point: it is what makes a ciphertext copied to another
// `mfa_credentials` row fail to decrypt rather than decrypt to the wrong operator's
// secret. **There is no unbound form of this** -- see `identity.SecretStore` for why
// an unbound seal is a capability nobody should hold.
func (s *FileSecretStore) WrapFor(tenant, userID, factorID string, plaintext []byte) ([]byte, error) {
	if s == nil || s.keyPath == "" {
		return nil, errors.New("identity: no secret key path is configured")
	}
	aead, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("identity: read randomness for the secret nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, plaintext, secretAAD(tenant, userID, factorID))
	return []byte(base64.StdEncoding.EncodeToString(sealed)), nil
}

// UnwrapFor decrypts a secret and requires it to have been bound to this row.
//
// A ciphertext wrapped without a binding, or bound to a different row, fails
// here. That is the whole point: an attacker who can write to mfa_factors
// cannot move a secret between rows.
func (s *FileSecretStore) UnwrapFor(tenant, userID, factorID string, ciphertext []byte) ([]byte, error) {
	return s.open(ciphertext, secretAAD(tenant, userID, factorID))
}

func (s *FileSecretStore) open(ciphertext []byte, aad []byte) ([]byte, error) {
	if s == nil || s.keyPath == "" {
		return nil, errors.New("identity: no secret key path is configured")
	}
	if len(ciphertext) == 0 {
		return nil, errors.New("identity: the wrapped secret is empty")
	}
	aead, err := s.aead()
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(string(ciphertext))
	if err != nil {
		return nil, fmt.Errorf("identity: the wrapped secret is not base64: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return nil, errors.New("identity: the wrapped secret is shorter than its nonce")
	}
	nonce, sealed := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		// Deliberately does not distinguish a wrong key from a wrong AAD from
		// tampering: telling an attacker which of the three they got right turns
		// a decryption failure into an oracle.
		return nil, errors.New("identity: the wrapped secret could not be decrypted; the " +
			"key may have been rotated, the secret may belong to another row, or it may " +
			"have been modified")
	}
	return plain, nil
}

// secretAAD builds the additional authenticated data for a wrapped secret.
//
// The separators matter: without them ("ab","c") and ("a","bc") would produce
// the same string, and a secret could be replayed into a row whose id is a
// rearrangement of the original.
func secretAAD(tenant, userID, factorID string) []byte {
	return []byte(fmt.Sprintf("identity.secret\x00%s\x00%s\x00%s", tenant, userID, factorID))
}

func (s *FileSecretStore) aead() (cipher.AEAD, error) {
	key, err := s.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("identity: the secret key is not a usable AES key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("identity: build the secret cipher: %w", err)
	}
	return aead, nil
}

// key reads and validates the key file.
func (s *FileSecretStore) key() ([]byte, error) {
	f, err := os.Open(s.keyPath)
	if err != nil {
		return nil, fmt.Errorf("identity: open the secret key at %s: %w", s.keyPath, err)
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return nil, fmt.Errorf("identity: read the secret key: %w", err)
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("identity: the secret key at %s is %d bytes, want %d. A key of "+
			"the wrong length is not padded or stretched here: a derived key is a key "+
			"somebody chose, and this file was written to be exactly %d bytes",
			s.keyPath, len(raw), KeySize, KeySize)
	}
	if err := s.checkPermissions(); err != nil {
		return nil, err
	}
	return raw, nil
}

// checkPermissions refuses when the key file is readable by anyone other than
// its owner, and only on a platform where the mode bits decide that.
//
// The split is the point, and it is stated rather than inferred. On POSIX the
// mode is the mechanism, so a world-readable key is a refusal. On Windows the
// mode is advisory -- os.Chmod cannot clear the bits and the ACL is what
// actually protects the file -- so the same condition is reported once as a
// warning and the store continues. The previous behaviour returned the
// refusal on every platform, and because os.Chmod cannot produce 0400 on
// Windows the mode it checked was permanently 0666 there: the sole path that
// enrols an administrator's TOTP factor failed after migrate and connect had
// both succeeded, which reads as a TOTP fault and is nothing of the kind.
// Refusing everywhere is one defect; enforcing nowhere would be the other.
func (s *FileSecretStore) checkPermissions() error {
	info, err := os.Stat(s.keyPath)
	if err != nil {
		return fmt.Errorf("identity: stat the secret key: %w", err)
	}
	if problem := keyPermissionProblem(info.Mode().Perm()); problem != "" {
		if permissionBitsMeanSomething() {
			return fmt.Errorf("identity: the secret key at %s is %s; run securityctl "+
				"install to set it to 0400", s.keyPath, problem)
		}
		warnAdvisoryKeyPermissions(s.keyPath, problem)
	}
	return nil
}

// keyPermissionProblem reports why a key file's mode is unacceptable -- naming
// the offending mode -- or "" when it is acceptable.
//
// Pure over the mode. The rule is identical on every platform; only whether it
// is ENFORCED differs, and that is permissionBitsMeanSomething's decision. The
// separation is what lets the rule be tested on a host whose mode bits enforce
// nothing, which is the only way to prove the control on the very machine that
// does not enforce it. Without this split the rule is a POSIX assertion that is
// invisible where most pilots run.
func keyPermissionProblem(mode os.FileMode) string {
	if mode.Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("readable by anyone other than its owner (mode %04o)", mode.Perm())
}

// permissionBitsMeanSomething reports whether this platform decides file access
// by the mode bits. True on POSIX; false on Windows and Plan 9, where the mode
// is advisory and the ACL -- or the read-only flag -- is the real control.
//
// This is the entire platform split in one predicate, so it is read rather
// than guessed: concluding "the mode does not mean anything here" from which
// bits happened to be set is how a genuinely world-readable key on a real POSIX
// host becomes indistinguishable from an advisory one on Windows. It takes no
// argument and reads runtime.GOOS directly so that a test can assert the
// control in both directions on the single machine it is running on.
func permissionBitsMeanSomething() bool {
	return runtime.GOOS != "windows" && runtime.GOOS != "plan9"
}

// warnAdvisoryKeyPermissions reports a world-readable key file once per process,
// on a platform where the mode does not enforce it. Once rather than every
// operation, because checkPermissions is reached through every wrap and every
// unwrap, and a warning nobody can read past the first of a thousand is not a
// warning. It is a warning and not a refusal by design: refusing an advisory
// condition would make the product unstartable on Windows, and silently
// accepting it would let a genuinely exposed key look protected.
var advisoryKeyWarned sync.Once

func warnAdvisoryKeyPermissions(path, problem string) {
	advisoryKeyWarned.Do(func() {
		slog.Default().Warn(
			"the secret key file is world-readable; this platform's mode bits are advisory "+
				"and do not enforce 0400, so restrict the file's ACL or run securityctl install to protect it",
			"path", path, "problem", problem)
	})
}
