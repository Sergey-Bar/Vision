package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// ed25519PublicKey aliases the key type so the operator file reads as prose.
// It is the declared type decodePublicKey hands back, which is why the operator
// command passes the decoded key through without a redundant conversion.
type ed25519PublicKey = ed25519.PublicKey

// newIdentityService builds the identity service for the CLI.
//
// The Audit writer is not optional here. Without it every authentication and
// bootstrap event is silently dropped, which is exactly the hole the threat
// model names: an identity layer that can be made not to record is an identity
// layer nobody can investigate.
func newIdentityService() (*identity.Service, error) {
	pool, err := openPool(context.Background())
	if err != nil {
		return nil, err
	}
	return &identity.Service{
		Pool:  pool,
		Audit: &audit.Writer{Pool: pool},
	}, nil
}

func bufioReader(env *command.Env) *bufio.Reader { return bufio.NewReader(env.Stdin) }

// loadOrCreateKey reads a signing key, creating one if the file is absent.
//
// A generated key is written owner-only and never printed. The private half of a
// checkpoint signing key that ends up in a backup, a support ticket, or a
// repository is not a signing key any more.
func loadOrCreateKey(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(key) != ed25519.PrivateKeySize {
			return nil, nil, fmt.Errorf("the signing key file is not a %d-byte hex private key",
				ed25519.PrivateKeySize)
		}
		priv := ed25519.PrivateKey(key)
		return priv, priv.Public().(ed25519.PublicKey), nil
	}
	if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read signing key: %w", err)
	}

	pub, priv, genErr := ed25519.GenerateKey(nil)
	if genErr != nil {
		return nil, nil, fmt.Errorf("generate signing key: %w", genErr)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create key directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		return nil, nil, fmt.Errorf("write signing key: %w", err)
	}
	fmt.Fprintf(os.Stderr,
		"created a new signing key at %s, owner-only. Back it up somewhere the database "+
			"host cannot reach, or losing it makes every future seal unverifiable.\n", path)
	return priv, pub, nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
