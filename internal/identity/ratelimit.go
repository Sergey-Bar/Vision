package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
)

// DefaultLoginRateLimitPerMinute is how many authentication attempts one
// source may make per minute when the site configuration does not say.
//
// It is the value the site configuration publishes as
// `console.loginRateLimitPerMinute`
// (contracts/json-schema/site-config.schema.json) and the value the threat
// model records as mitigated (docs/threat-model.md A3.5).
//
// Five is low for a person and low for a machine, and that is the point. The
// control is against a source SPRAYING credentials - one address trying
// account after account, which is what credential stuffing is - and not
// against an operator signing in. A site that genuinely has six operators
// behind one address raises the number; the number is a deployment decision
// and it is named in the configuration for exactly that reason.
const DefaultLoginRateLimitPerMinute = 5

// ErrRateLimited is returned when a source has spent its per-minute budget.
//
// It is a separate identity from ErrLocked because the two are facts about
// different subjects. ErrLocked is about an ACCOUNT - this account is inside
// its backoff window - and ErrRateLimited is about a SOURCE - this address has
// spent its budget. The operator behind a shared address is refused while
// their own account is perfectly healthy, and a refusal that does not say
// which of the two happened sends them to wait out a lockout that was never
// theirs.
var ErrRateLimited = errors.New("identity: too many authentication attempts from this source")

// loginRateWindow is the window the per-source limit is measured over.
//
// A sliding window rather than a fixed one, because a fixed window has a
// boundary an attacker can aim at: five attempts at 12:00:59 and five more at
// 12:01:00 are ten attempts inside one second, and a limiter that counts per
// clock-minute admits both.
const loginRateWindow = time.Minute

// maxTrackedSources bounds how many sources the limiter remembers.
//
// The limiter keeps one entry per source it has seen, and a spray from a
// million addresses would otherwise be a million entries: the control would
// become the denial of service it was built to stop.
const maxTrackedSources = 4096

// The two kinds of attempt a source can make.
//
// They are counted separately because they are two different routes with two
// different budgets in the published API: the password step and the
// second-factor verification each declare their own 429. One budget shared
// between them would mean a legitimate sign-in - a password step and a code -
// spent two of the five, so a third sign-in in a minute would be refused
// while every one of them was correct.
const (
	attemptKindPassword = "password"
	attemptKindFactor   = "factor"
)

// sourceLimiter counts authentication attempts per source over a sliding
// window.
//
// # Why the state is in memory and not in the database
//
// The counter is a throttle on a source's throughput, not a durable record of
// what that source did, and the durable record is the audit ledger: every
// refused attempt is written there with its source address, and that is where
// an investigator looks. A database counter would mean a write on every
// authentication attempt - including the ones that succeed - and a row that
// has to be swept, for a window that is a minute wide.
//
// The cost is stated rather than hidden: a process restart clears the
// counters, so an attacker who can restart the console gets a fresh budget,
// and two processes behind one address each keep their own counters. Both are
// accepted for a throttle whose window is a minute. They would not be accepted
// for the replay cache, which is why that one lives in the database
// (`consumeTOTPStep`): a code that may be presented once is a correctness
// property and a counter is a rate property.
type sourceLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[string][]time.Time
}

// allow reports whether an attempt from this source may proceed, and records
// it against the source's window.
//
// The window slides: an attempt counts against its source for a minute after
// it was made, so a burst that straddles a minute boundary is still a burst.
func (l *sourceLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	seen := l.attempts[key]
	// Filtered in place: the surviving attempts are a prefix of the slice, so
	// writing them back over the same backing array is safe.
	live := seen[:0]
	for _, at := range seen {
		if at.After(cutoff) {
			live = append(live, at)
		}
	}
	if len(live) >= l.limit {
		l.attempts[key] = live
		return false
	}
	l.attempts[key] = append(live, now)
	l.evict(cutoff)
	return true
}

// evict drops the sources that have been quiet for a whole window, and the
// oldest ones if the map is still over its bound.
//
// The bound is the point: the limiter remembers one entry per source it has
// seen, and a spray from a million addresses is a million entries unless
// something refuses to grow.
func (l *sourceLimiter) evict(cutoff time.Time) {
	if len(l.attempts) <= maxTrackedSources {
		return
	}
	for key, seen := range l.attempts {
		if len(seen) == 0 || !seen[len(seen)-1].After(cutoff) {
			delete(l.attempts, key)
		}
	}
	// Still over the bound, which means every source it knows about is live.
	// Drop the oldest until it is not. A dropped source starts its window
	// again, which is a weakening under a spray wide enough to reach this,
	// and it is the right trade: the alternative is unbounded memory for a
	// throttle, and a throttle that can be made to allocate without limit is
	// not a control.
	for len(l.attempts) > maxTrackedSources {
		oldestKey := ""
		var oldestAt time.Time
		for key, seen := range l.attempts {
			if len(seen) == 0 {
				continue
			}
			last := seen[len(seen)-1]
			if oldestKey == "" || last.Before(oldestAt) {
				oldestKey, oldestAt = key, last
			}
		}
		if oldestKey == "" {
			return
		}
		delete(l.attempts, oldestKey)
	}
}

// loginLimiter returns the per-source limiter, building it on first use.
//
// The service is constructed as a struct literal in several places, and the
// limit is a configuration value, so the limiter is built when the first
// authentication attempt arrives rather than in a constructor every caller
// would have to remember to call.
//
// The limit is read ONCE, at that moment. A service whose limit changes
// after it has been used is a configuration error, and reading it on every
// attempt would make the number a value that moves under an operator's feet
// halfway through a burst.
func (s *Service) loginLimiter() *sourceLimiter {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	if s.limiter == nil {
		s.limiter = &sourceLimiter{
			limit:    s.loginRateLimit(),
			window:   loginRateWindow,
			attempts: make(map[string][]time.Time),
		}
	}
	return s.limiter
}

// loginRateLimit is the configured limit, or the published default.
func (s *Service) loginRateLimit() int {
	if s.LoginRateLimitPerMinute > 0 {
		return s.LoginRateLimitPerMinute
	}
	return DefaultLoginRateLimitPerMinute
}

// allowLoginAttempt applies the per-source rate limit to one authentication
// attempt and returns ErrRateLimited when the source has spent its budget.
//
// The check is BEFORE the account is read and before the password is
// verified, and that ordering is the control. The password hash is Argon2id
// on purpose, and a limiter that runs after the hash has been computed does
// not slow the attack down at all: it only refuses the session afterwards.
// Refusing before the hash means the attacker's own throughput is what the
// limit bounds.
//
// The refusal is audited, because a source that is being refused is the fact
// an investigator wants and it is invisible in the ledger otherwise. It is
// recorded as a failed login rather than as a lockout, because no account was
// locked: the account-level backoff is a different control and a different
// subject.
//
// The actor is the name that was tried, and the source address when there was
// no name -- the second-factor route is refused before the challenge row has
// been read, so at that moment the source is the only thing known about the
// attempt.
func (s *Service) allowLoginAttempt(ctx context.Context, tenant, username, kind, sourceIP string) error {
	limit := s.loginRateLimit()
	// The tenant is part of the key. Two tenants behind one address are two
	// budgets: an attack against one of them must not spend the other's.
	key := tenant + "\x00" + kind + "\x00" + sourceIP
	if s.loginLimiter().allow(key, s.now()) {
		return nil
	}
	s.recordAuth(ctx, tenant, username, sourceIP, audit.ActionLoginFailed, sourceIP,
		fmt.Sprintf("rate limited: more than %d %s attempts from this source in %s",
			limit, kind, loginRateWindow))
	return fmt.Errorf("%w: %d %s attempts from this source in the last %s",
		ErrRateLimited, limit, kind, loginRateWindow)
}
