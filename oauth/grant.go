package oauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"
)

// grant status values as stored in oauth_grants.status (MySQL ENUM('active','revoked')).
const (
	statusActive = "active"
)

// Default cache timings. The TTL bounds how long a revocation takes to be honored fleet-wide; the
// stale grace bounds how long a value stays usable as a fallback when the DB is unreachable.
const (
	defaultGrantTTL        = 45 * time.Second
	defaultGrantStaleGrace = 10 * time.Minute
)

// Queryer is the minimal database seam the GrantChecker needs. It is satisfied as-is by *sql.DB,
// *sql.Tx, and sqlc's generated DBTX — so a service hands over whatever handle it already has, no
// adapter, no driver coupling. (Every Cata service is database/sql + MySQL, so this is uniform.)
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GrantChecker performs the revocation half of validation: is the merchant's grant still active?
// It reads oauth_grants in the token's own tenant schema, caches the result, and degrades
// gracefully — a DB blip serves the last known value within a grace window rather than failing
// every partner. Construct it once and share it; it is safe for concurrent use.
type GrantChecker struct {
	db    Queryer
	cache Cache
	ttl   time.Duration
	grace time.Duration
	group singleflight.Group
}

// GCOption configures a GrantChecker.
type GCOption func(*grantConfig)

type grantConfig struct {
	cache Cache
	ttl   time.Duration
	grace time.Duration
	now   func() time.Time
}

// WithCache overrides the default process-local cache (e.g. for tests or a custom impl).
func WithCache(c Cache) GCOption { return func(g *grantConfig) { g.cache = c } }

// WithTTL sets how long a grant-status result is treated as fresh (default 45s). This is the
// fleet-wide upper bound on revocation latency.
func WithTTL(d time.Duration) GCOption { return func(g *grantConfig) { g.ttl = d } }

// WithStaleGrace sets how long past the TTL a cached value may still be served as a fallback when
// the DB is unreachable (default 10m).
func WithStaleGrace(d time.Duration) GCOption { return func(g *grantConfig) { g.grace = d } }

// withClock overrides the time source (tests only).
func withClock(now func() time.Time) GCOption { return func(g *grantConfig) { g.now = now } }

// NewGrantChecker builds a GrantChecker over db. By default it uses a process-local cache with a
// 45s TTL and a 10m stale grace.
func NewGrantChecker(db Queryer, opts ...GCOption) *GrantChecker {
	cfg := grantConfig{ttl: defaultGrantTTL, grace: defaultGrantStaleGrace, now: time.Now}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.ttl <= 0 {
		cfg.ttl = defaultGrantTTL
	}
	if cfg.grace < 0 {
		cfg.grace = 0
	}
	if cfg.cache == nil {
		cfg.cache = newMemCache(cfg.ttl+cfg.grace, cfg.now)
	}
	return &GrantChecker{db: db, cache: cfg.cache, ttl: cfg.ttl, grace: cfg.grace}
}

// Active reports whether the grant behind claims is still active. It returns:
//   - (true, nil)  grant is active;
//   - (false, ErrGrantRevoked)      grant is revoked or absent (fail-closed);
//   - (false, ErrGrantUnavailable)  the status could not be determined (DB error, no usable cache).
//
// A fresh cache hit skips the DB entirely; concurrent misses for the same key collapse into one
// query via singleflight; on a DB error a cached value within the stale grace is used.
func (g *GrantChecker) Active(ctx context.Context, claims *Claims) (bool, error) {
	// Validate the tenant label up front. A malformed label is a bad token, not a DB outage, so it
	// fails closed (403) rather than masquerading as ErrGrantUnavailable (503).
	schema, err := tenantSchema(claims.Tenant)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrGrantRevoked, err)
	}
	key := claims.Tenant + "\x00" + claims.Subject

	if val, age, ok := g.cache.Get(key); ok && age <= g.ttl {
		return activeOrRevoked(val)
	}

	v, err, _ := g.group.Do(key, func() (any, error) {
		status, found, qErr := g.queryStatus(ctx, schema, claims.Subject)
		if qErr != nil {
			return nil, qErr
		}
		if !found {
			// Absent grant -> not active, but don't cache the absence: a grant created moments
			// later should be seen without waiting out a TTL (mirrors middleware).
			return "", nil
		}
		g.cache.Set(key, status)
		return status, nil
	})
	if err != nil {
		// DB unreachable: serve a stale-but-recent cached value if we have one.
		if val, age, ok := g.cache.Get(key); ok && age <= g.ttl+g.grace {
			return activeOrRevoked(val)
		}
		return false, fmt.Errorf("%w: %v", ErrGrantUnavailable, err)
	}
	return activeOrRevoked(v.(string))
}

// activeOrRevoked maps a status string to the (bool, error) contract: active -> (true, nil),
// anything else (revoked, or the "" absent sentinel) -> (false, ErrGrantRevoked).
func activeOrRevoked(status string) (bool, error) {
	if status == statusActive {
		return true, nil
	}
	return false, ErrGrantRevoked
}

// queryStatus reads oauth_grants.status for subject in the given tenant schema. found is false
// when there is no such grant row. schema was already validated (backtick-quoted, strict charset)
// by the caller, so it is not a SQL-identifier-injection vector; subject is a bound parameter.
func (g *GrantChecker) queryStatus(ctx context.Context, schema, subject string) (status string, found bool, err error) {
	query := "SELECT status FROM `" + schema + "`.oauth_grants WHERE subject = ? LIMIT 1"
	err = g.db.QueryRowContext(ctx, query, subject).Scan(&status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return status, true, nil
}

// tenantSchema builds the `tenant<id>` MySQL schema name from a tenant label, rejecting anything
// outside [A-Za-z0-9_-] (notably backticks/quotes/dots/whitespace) so the interpolated identifier
// cannot break out. Mirrors middleware's isTagLabel gate.
func tenantSchema(tenant string) (string, error) {
	if tenant == "" {
		return "", errors.New("oauth: empty tenant")
	}
	for _, r := range tenant {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return "", fmt.Errorf("oauth: invalid tenant label %q", tenant)
		}
	}
	return "tenant" + tenant, nil
}
