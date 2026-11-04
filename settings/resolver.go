package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sambatechno/svclib"
	"github.com/sambatechno/svclib/logger"
)

// MsgDeprecated is the log message of a deprecated-key warning. Filter on it in
// Cloud Logging to find the services that still read legacy keys.
const MsgDeprecated = "svclib/settings: deprecated setting key"

// MsgStoreDegraded is logged when the aliases or feature_flags table can't be
// read (for example on a tenant whose migration has not run yet). The resolver
// then answers from settings alone.
const MsgStoreDegraded = "svclib/settings: alias or feature flag lookup failed, using settings only"

const (
	defaultAliasTTL     = 5 * time.Minute
	defaultFailureTTL   = time.Minute
	defaultWarnInterval = 10 * time.Minute
)

// Deprecation describes one read of a legacy key.
type Deprecation struct {
	Service   string
	TenantID  string
	LegacyKey string
	NewKey    string
	Target    string
}

// Resolver answers setting and feature flag reads by legacy or new name. It
// is safe for concurrent use; create one per process.
type Resolver struct {
	store        Store
	service      string
	tenantOf     func(context.Context) string
	aliasTTL     time.Duration
	failureTTL   time.Duration
	warnInterval time.Duration
	now          func() time.Time
	onDeprecated func(context.Context, Deprecation)
	onDegraded   func(context.Context, string, error)
	builtin      []Alias

	mu      sync.Mutex
	aliases map[string]*aliasSet // by tenant
	warned  map[string]time.Time // by tenant|key
}

type aliasSet struct {
	byLegacy  map[string]Alias
	byNew     map[string]Alias
	expiresAt time.Time
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithService names the calling service in deprecation warnings. Defaults to
// $K_SERVICE (set by Cloud Run), then the binary name.
func WithService(name string) Option {
	return func(r *Resolver) { r.service = name }
}

// WithTenantFunc tells the resolver how to read the tenant ID from ctx, for
// services that don't use svclib.WithTenantID. The tenant scopes the alias
// cache and the warning rate limit.
func WithTenantFunc(fn func(context.Context) string) Option {
	return func(r *Resolver) { r.tenantOf = fn }
}

// WithAliasTTL sets how long a tenant's aliases are cached (default 5m).
func WithAliasTTL(d time.Duration) Option {
	return func(r *Resolver) { r.aliasTTL = d }
}

// WithWarnInterval sets the minimum time between two warnings for the same
// tenant and legacy key (default 10m). Zero logs every read.
func WithWarnInterval(d time.Duration) Option {
	return func(r *Resolver) { r.warnInterval = d }
}

// WithDeprecationHandler replaces the default WARNING log for legacy reads.
func WithDeprecationHandler(fn func(context.Context, Deprecation)) Option {
	return func(r *Resolver) { r.onDeprecated = fn }
}

// WithDegradedHandler replaces the default WARNING log when the alias or
// feature flag table can't be read.
func WithDegradedHandler(fn func(ctx context.Context, what string, err error)) Option {
	return func(r *Resolver) { r.onDegraded = fn }
}

// WithBuiltinAliases registers aliases the service knows at compile time. They
// apply when the tenant's setting_aliases table can't be read or has no row
// for that key (for example when the service deploys before the migration
// runs); a setting_aliases row for the same legacy or new key always wins.
func WithBuiltinAliases(aliases ...Alias) Option {
	return func(r *Resolver) { r.builtin = append(r.builtin, aliases...) }
}

func withClock(now func() time.Time) Option {
	return func(r *Resolver) { r.now = now }
}

// New returns a Resolver that reads through store.
func New(store Store, opts ...Option) *Resolver {
	r := &Resolver{
		store:        store,
		service:      defaultService(),
		tenantOf:     svclibTenant,
		aliasTTL:     defaultAliasTTL,
		failureTTL:   defaultFailureTTL,
		warnInterval: defaultWarnInterval,
		now:          time.Now,
		aliases:      map[string]*aliasSet{},
		warned:       map[string]time.Time{},
	}
	r.onDeprecated = r.logDeprecated
	r.onDegraded = r.logDegraded
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func defaultService() string {
	if s := os.Getenv("K_SERVICE"); s != "" {
		return s
	}
	if len(os.Args) > 0 {
		return filepath.Base(os.Args[0])
	}
	return "unknown"
}

func svclibTenant(ctx context.Context) string {
	id, _ := svclib.GetTenantID(ctx)
	return id
}

// Get returns the value of one key, read by legacy or new name. found is false
// when no row exists under either name.
func (r *Resolver) Get(ctx context.Context, key string) (value string, found bool, err error) {
	values, err := r.GetMany(ctx, []string{key})
	if err != nil {
		return "", false, err
	}
	value, found = values[key]
	return value, found, nil
}

// IsEnabled reports whether a boolean setting or feature flag is on. Missing
// rows read as false.
func (r *Resolver) IsEnabled(ctx context.Context, key string) (bool, error) {
	v, _, err := r.Get(ctx, key)
	if err != nil {
		return false, err
	}
	return ParseBool(v), nil
}

// GetMany returns the values of keys, keyed by the name the caller asked for.
// Keys with no row under either name are left out.
//
//   - A legacy key is answered from its replacement and logs a deprecation
//     warning.
//   - A feature flag (FF_ prefix, or the new side of a feature_flags alias)
//     is read from feature_flags and returned as "true" / "false". When the
//     flag row is missing it falls back to the legacy setting.
//   - A settings alias reads the row named by its source of truth, falling
//     back to the other row, so both names always return the same value.
//
// Only a failed settings read is returned as an error. A failed alias or
// feature flag read is logged and the resolver answers from settings alone.
func (r *Resolver) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	aliases := r.aliasesFor(ctx)

	type plan struct {
		key   string
		alias *Alias
	}
	plans := make([]plan, 0, len(keys))
	settingKeys := map[string]struct{}{}
	flagKeys := map[string]struct{}{}

	for _, k := range keys {
		p := plan{key: k}
		if a, ok := aliases.byLegacy[k]; ok {
			r.deprecated(ctx, a)
			p.alias = &a
		} else if a, ok := aliases.byNew[k]; ok {
			p.alias = &a
		}

		switch {
		case p.alias == nil && strings.HasPrefix(k, FeatureFlagPrefix):
			flagKeys[k] = struct{}{}
		case p.alias == nil:
			settingKeys[k] = struct{}{}
		case p.alias.Target == TargetFeatureFlags:
			flagKeys[p.alias.NewKey] = struct{}{}
			settingKeys[p.alias.LegacyKey] = struct{}{}
		default:
			settingKeys[p.alias.LegacyKey] = struct{}{}
			settingKeys[p.alias.NewKey] = struct{}{}
		}
		plans = append(plans, p)
	}

	settingValues := map[string]string{}
	if len(settingKeys) > 0 {
		var err error
		settingValues, err = r.store.Settings(ctx, setKeys(settingKeys))
		if err != nil {
			return nil, err
		}
	}

	flagValues := map[string]bool{}
	if len(flagKeys) > 0 {
		var err error
		flagValues, err = r.store.FeatureFlags(ctx, setKeys(flagKeys))
		if err != nil {
			r.onDegraded(ctx, "feature_flags", err)
			flagValues = map[string]bool{}
		}
	}

	out := make(map[string]string, len(plans))
	for _, p := range plans {
		if v, ok := resolve(p.key, p.alias, settingValues, flagValues); ok {
			out[p.key] = v
		}
	}
	return out, nil
}

func resolve(key string, a *Alias, settingValues map[string]string, flagValues map[string]bool) (string, bool) {
	if a == nil {
		if strings.HasPrefix(key, FeatureFlagPrefix) {
			if on, ok := flagValues[key]; ok {
				return FormatBool(on), true
			}
			return "", false
		}
		v, ok := settingValues[key]
		return v, ok
	}

	if a.Target == TargetFeatureFlags {
		if on, ok := flagValues[a.NewKey]; ok {
			return FormatBool(on), true
		}
		if v, ok := settingValues[a.LegacyKey]; ok {
			return FormatBool(ParseBool(v)), true
		}
		return "", false
	}

	primary, secondary := a.NewKey, a.LegacyKey
	if a.SourceOfTruth == SourceLegacy {
		primary, secondary = a.LegacyKey, a.NewKey
	}
	if v, ok := settingValues[primary]; ok {
		return v, true
	}
	v, ok := settingValues[secondary]
	return v, ok
}

func setKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// aliasesFor returns the tenant's aliases, loading them when the cache is cold
// or expired. A failed load is cached briefly as "no aliases".
func (r *Resolver) aliasesFor(ctx context.Context) *aliasSet {
	tenant := r.tenantOf(ctx)
	now := r.now()

	r.mu.Lock()
	cached, ok := r.aliases[tenant]
	r.mu.Unlock()
	if ok && now.Before(cached.expiresAt) {
		return cached
	}

	set := &aliasSet{byLegacy: map[string]Alias{}, byNew: map[string]Alias{}, expiresAt: now.Add(r.aliasTTL)}
	rows, err := r.store.Aliases(ctx)
	if err != nil {
		r.onDegraded(ctx, "setting_aliases", err)
		set.expiresAt = now.Add(r.failureTTL)
	}
	for _, a := range rows {
		if a.LegacyKey == "" || a.NewKey == "" {
			continue
		}
		set.byLegacy[a.LegacyKey] = a
		set.byNew[a.NewKey] = a
	}
	for _, a := range r.builtin {
		if a.LegacyKey == "" || a.NewKey == "" {
			continue
		}
		if _, ok := set.byLegacy[a.LegacyKey]; ok {
			continue
		}
		if _, ok := set.byNew[a.NewKey]; ok {
			continue
		}
		set.byLegacy[a.LegacyKey] = a
		set.byNew[a.NewKey] = a
	}

	r.mu.Lock()
	r.aliases[tenant] = set
	r.mu.Unlock()
	return set
}

// Invalidate drops the cached aliases of the tenant in ctx.
func (r *Resolver) Invalidate(ctx context.Context) {
	r.mu.Lock()
	delete(r.aliases, r.tenantOf(ctx))
	r.mu.Unlock()
}

func (r *Resolver) deprecated(ctx context.Context, a Alias) {
	tenant := r.tenantOf(ctx)
	if !r.allow(tenant + "|" + a.LegacyKey) {
		return
	}
	r.onDeprecated(ctx, Deprecation{
		Service:   r.service,
		TenantID:  tenant,
		LegacyKey: a.LegacyKey,
		NewKey:    a.NewKey,
		Target:    a.Target,
	})
}

// allow rate-limits warnings per key.
func (r *Resolver) allow(key string) bool {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.warned[key]; ok && now.Sub(last) < r.warnInterval {
		return false
	}
	r.warned[key] = now
	return true
}

func (r *Resolver) logDeprecated(ctx context.Context, d Deprecation) {
	logger.New("svclib/settings").WithContext(ctx).WithFields(map[string]any{
		"service":    d.Service,
		"tenant_id":  d.TenantID,
		"legacy_key": d.LegacyKey,
		"new_key":    d.NewKey,
		"target":     d.Target,
	}).Warn(MsgDeprecated, nil)
}

func (r *Resolver) logDegraded(ctx context.Context, what string, err error) {
	tenant := r.tenantOf(ctx)
	if !r.allow(tenant + "|degraded|" + what) {
		return
	}
	logger.New("svclib/settings").WithContext(ctx).WithFields(map[string]any{
		"service":   r.service,
		"tenant_id": tenant,
		"table":     what,
	}).Warn(MsgStoreDegraded, err)
}
