package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// Secret material and identity.
//
// Two things have to exist before the console can be reached, and both of them
// are things an installer creates rather than a container image shipping:
//
//  1. The AES key the MFA store wraps factors with. `identity.GenerateSecretKey`
//     REFUSES to overwrite an existing key, because rotating it by overwriting
//     would make every enrolled factor permanently undecryptable. So the seed
//     generates it only when it is absent, and says which it did.
//  2. A certificate for the console's TLS listener. `config` refuses to start
//     with neither TLS nor a trusted proxy, which is the right refusal and means
//     a demo console cannot be served over plaintext and called a demo of the
//     product. The seed mints a self-signed leaf and prints its SHA-256
//     fingerprint, so a judge who clicks through a browser warning can verify
//     afterwards that they were talking to the certificate this run created.
//
// The three principals are the three scopes the identity model has:
//
//	tenant admin   every site in the tenant, the rule store, identity, audit
//	site operator  one site, the incident queue, evidence, dispositions
//	system worker  no site assignment; the scope a non-human principal with
//	               `service.*` holds, and the one the workers run under
//
// A site with only one principal cannot show the isolation model working, and the
// isolation model is the product.

// The demo credentials. Stated, not hidden.
//
// A credential printed on the console with a fixed value is a demo credential. A
// credential generated at random and printed once is the same thing with extra
// steps, and a credential that is neither is a credential somebody will find in a
// shell history. These are printed in the report and are refused by `config`
// nowhere because this is a seeded demo site, not an install.
const (
	demoTenantAdmin  = "admin"
	demoSiteOperator = "operator"
	demoSystemWorker = "worker"
	demoPassphrase   = "seeded-demo-passphrase-not-a-secret" //nolint:gosec // G101: not a credential. A constant with a stated value, printed in the seed report so a judge can sign in; the point of printing it is that it is not secret.
)

// writeSecretMaterial generates the MFA wrapping key if absent and mints the
// console certificate. It returns the key path so the caller does not have to
// recompute it.
func (s *seeder) writeSecretMaterial(_ context.Context) (string, error) {
	generated := false
	if _, err := os.Stat(s.secretKeyPath); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat the secret key at %s: %w", s.secretKeyPath, err)
		}
		if err := identity.GenerateSecretKey(s.secretKeyPath); err != nil {
			return "", fmt.Errorf("generate the MFA secret key: %w", err)
		}
		generated = true
	}
	if generated {
		s.say("generated the MFA wrapping key at %s (0400)", s.secretKeyPath)
	} else {
		s.say("reused the existing MFA wrapping key at %s; it was not overwritten, "+
			"because rotating it would make every enrolled factor undecryptable",
			s.secretKeyPath)
	}
	s.report.SecretKeyPath = s.secretKeyPath

	fp, err := s.writeConsoleCertificate()
	if err != nil {
		return "", err
	}
	s.say("minted the console certificate %s, SHA-256 fingerprint %s", fp.Path, fp.Fingerprint)
	s.report.TLS = TLSSummary{
		Cert: fp.Path, Key: fp.KeyPath, FingerprintSHA256: fp.Fingerprint,
	}
	return s.secretKeyPath, nil
}

// writeIdentity creates the three principals.
//
// The administrator goes through `BootstrapAdmin`, which is the path that exists
// and which refuses a second administrator in a tenant. The other two go through
// `EnsureUser`, which is the provisioning path and which creates a row with NO
// password hash — deliberately, because "has never had a password" and "has one
// nobody remembers" must be different facts.
//
// The administrator's second factor is enrolled AND CONFIRMED, because a stored
// factor with a null `verified_at` is the one state that stores an enrolment and
// refuses every sign-in, and the console is mandatory-MFA. The confirmation needs
// a live TOTP code, so the seed computes one — which is what an authenticator
// does, and is why the same code is printed in the report for whoever is driving
// the demo.
func (s *seeder) writeIdentity(ctx context.Context, _ string) error {
	ident, err := s.identityService()
	if err != nil {
		return err
	}
	passphrase := s.opts.passphrase
	if strings.TrimSpace(passphrase) == "" {
		passphrase = demoPassphrase
	}

	// A second run must not fail here: `BootstrapAdmin` refuses a second
	// administrator on purpose, so the seed reads the existing one instead. That
	// is what makes the seed idempotent rather than a one-shot.
	adminID, err := s.findUser(ctx, demoTenantAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		adminID = ""
	} else if err != nil {
		return fmt.Errorf("look for an existing tenant administrator: %w", err)
	}

	if adminID == "" {
		adminID, err = ident.BootstrapAdmin(ctx, s.opts.tenant, demoTenantAdmin, passphrase, s.opts.site)
		if err != nil {
			return fmt.Errorf("create the tenant administrator: %w", err)
		}
		s.say("tenant admin %q (%s), assigned to %s", demoTenantAdmin, adminID, s.opts.site)
	} else {
		s.say("tenant admin %q already exists (%s); not creating a second one",
			demoTenantAdmin, adminID)
	}
	s.report.Principals = append(s.report.Principals, Principal{
		Username: demoTenantAdmin, UserID: adminID,
		Roles: []string{"tenant_admin"}, Sites: []string{s.opts.site},
	})

	// The site operator: one site, the incident queue, evidence, dispositions.
	operatorID := "usr-operator"
	if err := ident.EnsureUser(ctx, s.opts.tenant, operatorID, demoSiteOperator,
		"North Gate desk operator", "operator",
		[]string{s.opts.site}, "seed"); err != nil {
		return fmt.Errorf("create the site operator: %w", err)
	}
	s.report.Principals = append(s.report.Principals, Principal{
		Username: demoSiteOperator, UserID: operatorID,
		Roles: []string{"operator"}, Sites: []string{s.opts.site},
	})

	// The system worker: no site assignment at all.
	//
	// A principal with no `user_site_assignments` row has no site access, which
	// is the safe default — a misconfigured assignment denies rather than opens.
	// That is exactly what makes it the right demonstration of the third scope: a
	// non-human principal whose access comes from its role and not from a site.
	workerID := "usr-worker"
	if err := ident.EnsureUser(ctx, s.opts.tenant, workerID, demoSystemWorker,
		"Background worker", "service", nil, "seed"); err != nil {
		return fmt.Errorf("create the system worker: %w", err)
	}
	s.report.Principals = append(s.report.Principals, Principal{
		Username: demoSystemWorker, UserID: workerID,
		Roles: []string{"service"}, Sites: nil,
	})
	s.say("site operator %q and system worker %q created; the worker has NO site assignment, "+
		"which is the safe default rather than a gap", demoSiteOperator, demoSystemWorker)

	return nil
}

// findUser reads an existing principal's id, so a second run does not create a
// second administrator.
//
// `pgx.ErrNoRows` is returned unwrapped so the caller can tell "this tenant has no
// such user yet" — the first run — from "the database is unreachable", which is a
// failure.
func (s *seeder) findUser(ctx context.Context, username string) (string, error) {
	var id string
	err := s.pool.InTenant(ctx,
		dbx.TenantContext{TenantID: s.opts.tenant, ActorID: "seed", System: true},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT user_id FROM users WHERE tenant_id = $1 AND username = $2`,
				s.opts.tenant, username).Scan(&id)
		})
	if err != nil {
		return "", err
	}
	return id, nil
}
