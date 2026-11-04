package settings

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/sambatechno/svclib"
)

type fakeStore struct {
	mu          sync.Mutex
	settings    map[string]string
	flags       map[string]bool
	aliases     []Alias
	settingsErr error
	flagsErr    error
	aliasesErr  error
	aliasCalls  int
	settingReqs [][]string
}

func (f *fakeStore) Settings(_ context.Context, keys []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	f.settingReqs = append(f.settingReqs, sorted)
	if f.settingsErr != nil {
		return nil, f.settingsErr
	}
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := f.settings[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (f *fakeStore) FeatureFlags(_ context.Context, keys []string) (map[string]bool, error) {
	if f.flagsErr != nil {
		return nil, f.flagsErr
	}
	out := map[string]bool{}
	for _, k := range keys {
		if v, ok := f.flags[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (f *fakeStore) Aliases(context.Context) ([]Alias, error) {
	f.mu.Lock()
	f.aliasCalls++
	f.mu.Unlock()
	if f.aliasesErr != nil {
		return nil, f.aliasesErr
	}
	return f.aliases, nil
}

type recorder struct {
	mu          sync.Mutex
	deprecated  []Deprecation
	degradedFor []string
}

func (r *recorder) opts() []Option {
	return []Option{
		WithService("test-svc"),
		WithDeprecationHandler(func(_ context.Context, d Deprecation) {
			r.mu.Lock()
			r.deprecated = append(r.deprecated, d)
			r.mu.Unlock()
		}),
		WithDegradedHandler(func(_ context.Context, what string, _ error) {
			r.mu.Lock()
			r.degradedFor = append(r.degradedFor, what)
			r.mu.Unlock()
		}),
	}
}

func tenantCtx(id string) context.Context {
	return svclib.WithTenantID(context.Background(), id)
}

func pilotStore() *fakeStore {
	return &fakeStore{
		settings: map[string]string{
			"CURRENCY":                 "SGD",
			"IS_SHOW_SPECIAL_TIER":     "true",
			"IS_SHOW_LEGACY_ONLY":      "TRUE",
			"GA4_ENABLE_USER_EVENT":    "true",
			"IS_ENABLE_GA4_USER_EVENT": "false",
			"OLD_ONLY":                 "old",
		},
		flags: map[string]bool{
			"FF_SPECIAL_TIER": true,
			"FF_NO_ALIAS":     false,
		},
		aliases: []Alias{
			{LegacyKey: "IS_SHOW_SPECIAL_TIER", NewKey: "FF_SPECIAL_TIER", Target: TargetFeatureFlags, SourceOfTruth: SourceNew},
			{LegacyKey: "IS_SHOW_LEGACY_ONLY", NewKey: "FF_LEGACY_ONLY", Target: TargetFeatureFlags, SourceOfTruth: SourceNew},
			{LegacyKey: "GA4_ENABLE_USER_EVENT", NewKey: "IS_ENABLE_GA4_USER_EVENT", Target: TargetSettings, SourceOfTruth: SourceLegacy},
			{LegacyKey: "OLD_ONLY", NewKey: "NEW_MISSING", Target: TargetSettings, SourceOfTruth: SourceNew},
		},
	}
}

func TestGetMany_ResolvesLegacyAndNewNamesToSameValue(t *testing.T) {
	rec := &recorder{}
	r := New(pilotStore(), rec.opts()...)
	ctx := tenantCtx("t1")

	got, err := r.GetMany(ctx, []string{
		"CURRENCY", "IS_SHOW_SPECIAL_TIER", "FF_SPECIAL_TIER",
		"GA4_ENABLE_USER_EVENT", "IS_ENABLE_GA4_USER_EVENT",
		"FF_NO_ALIAS", "MISSING", "FF_MISSING",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"CURRENCY":             "SGD",
		"IS_SHOW_SPECIAL_TIER": "true",
		"FF_SPECIAL_TIER":      "true",
		// source_of_truth = legacy: both names return the legacy row.
		"GA4_ENABLE_USER_EVENT":    "true",
		"IS_ENABLE_GA4_USER_EVENT": "true",
		"FF_NO_ALIAS":              "false",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	// Only legacy names warn.
	if len(rec.deprecated) != 2 {
		t.Fatalf("deprecations = %+v, want 2", rec.deprecated)
	}
	byKey := map[string]Deprecation{}
	for _, d := range rec.deprecated {
		byKey[d.LegacyKey] = d
	}
	d := byKey["IS_SHOW_SPECIAL_TIER"]
	if d.Service != "test-svc" || d.TenantID != "t1" || d.NewKey != "FF_SPECIAL_TIER" || d.Target != TargetFeatureFlags {
		t.Errorf("unexpected deprecation %+v", d)
	}
	if _, ok := byKey["GA4_ENABLE_USER_EVENT"]; !ok {
		t.Errorf("missing deprecation for GA4_ENABLE_USER_EVENT")
	}
}

func TestFeatureFlagFallsBackToLegacySetting(t *testing.T) {
	r := New(pilotStore(), (&recorder{}).opts()...)
	ctx := tenantCtx("t1")

	// FF_LEGACY_ONLY has no feature_flags row; the legacy "TRUE" is normalized.
	for _, k := range []string{"FF_LEGACY_ONLY", "IS_SHOW_LEGACY_ONLY"} {
		v, found, err := r.Get(ctx, k)
		if err != nil || !found || v != "true" {
			t.Errorf("Get(%s) = %q, %v, %v; want true", k, v, found, err)
		}
	}
}

func TestSettingsAliasFallsBackToOtherRow(t *testing.T) {
	r := New(pilotStore(), (&recorder{}).opts()...)
	// source_of_truth = new, but NEW_MISSING has no row yet.
	for _, k := range []string{"OLD_ONLY", "NEW_MISSING"} {
		v, found, err := r.Get(tenantCtx("t1"), k)
		if err != nil || !found || v != "old" {
			t.Errorf("Get(%s) = %q, %v, %v; want old", k, v, found, err)
		}
	}
}

func TestIsEnabled(t *testing.T) {
	r := New(pilotStore(), (&recorder{}).opts()...)
	ctx := tenantCtx("t1")
	cases := map[string]bool{
		"FF_SPECIAL_TIER":      true,
		"IS_SHOW_SPECIAL_TIER": true,
		"FF_NO_ALIAS":          false,
		"MISSING":              false,
		"CURRENCY":             false,
	}
	for k, want := range cases {
		got, err := r.IsEnabled(ctx, k)
		if err != nil || got != want {
			t.Errorf("IsEnabled(%s) = %v, %v; want %v", k, got, err, want)
		}
	}
}

func TestDeprecationWarningIsRateLimitedPerTenantAndKey(t *testing.T) {
	rec := &recorder{}
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	opts := append(rec.opts(), withClock(func() time.Time { return now }), WithWarnInterval(10*time.Minute))
	r := New(pilotStore(), opts...)

	for i := 0; i < 5; i++ {
		_, _, _ = r.Get(tenantCtx("t1"), "IS_SHOW_SPECIAL_TIER")
	}
	_, _, _ = r.Get(tenantCtx("t2"), "IS_SHOW_SPECIAL_TIER")
	if len(rec.deprecated) != 2 {
		t.Fatalf("got %d warnings, want 2 (one per tenant)", len(rec.deprecated))
	}

	now = now.Add(11 * time.Minute)
	_, _, _ = r.Get(tenantCtx("t1"), "IS_SHOW_SPECIAL_TIER")
	if len(rec.deprecated) != 3 {
		t.Fatalf("got %d warnings after interval, want 3", len(rec.deprecated))
	}
}

func TestAliasesAreCachedPerTenant(t *testing.T) {
	store := pilotStore()
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	opts := append((&recorder{}).opts(), withClock(func() time.Time { return now }), WithAliasTTL(5*time.Minute))
	r := New(store, opts...)

	for i := 0; i < 3; i++ {
		_, _ = r.GetMany(tenantCtx("t1"), []string{"CURRENCY"})
	}
	_, _ = r.GetMany(tenantCtx("t2"), []string{"CURRENCY"})
	if store.aliasCalls != 2 {
		t.Fatalf("alias loads = %d, want 2", store.aliasCalls)
	}

	now = now.Add(6 * time.Minute)
	_, _ = r.GetMany(tenantCtx("t1"), []string{"CURRENCY"})
	if store.aliasCalls != 3 {
		t.Fatalf("alias loads after TTL = %d, want 3", store.aliasCalls)
	}

	r.Invalidate(tenantCtx("t1"))
	_, _ = r.GetMany(tenantCtx("t1"), []string{"CURRENCY"})
	if store.aliasCalls != 4 {
		t.Fatalf("alias loads after Invalidate = %d, want 4", store.aliasCalls)
	}
}

func TestAliasTableMissingDegradesToPlainSettings(t *testing.T) {
	store := pilotStore()
	store.aliasesErr = errors.New("Table 'setting_aliases' doesn't exist")
	store.flagsErr = errors.New("Table 'feature_flags' doesn't exist")
	rec := &recorder{}
	r := New(store, rec.opts()...)
	ctx := tenantCtx("t1")

	got, err := r.GetMany(ctx, []string{"IS_SHOW_SPECIAL_TIER", "FF_SPECIAL_TIER"})
	if err != nil {
		t.Fatal(err)
	}
	if got["IS_SHOW_SPECIAL_TIER"] != "true" {
		t.Errorf("legacy key should still read its own row, got %v", got)
	}
	if _, ok := got["FF_SPECIAL_TIER"]; ok {
		t.Errorf("FF key without table or alias should be missing, got %v", got)
	}
	if len(rec.deprecated) != 0 {
		t.Errorf("no aliases known, so no deprecation expected: %+v", rec.deprecated)
	}
	if len(rec.degradedFor) != 2 {
		t.Errorf("degraded = %v, want setting_aliases and feature_flags", rec.degradedFor)
	}
}

func TestFeatureFlagReadErrorFallsBackToLegacy(t *testing.T) {
	store := pilotStore()
	store.flagsErr = errors.New("boom")
	r := New(store, (&recorder{}).opts()...)
	v, found, err := r.Get(tenantCtx("t1"), "FF_SPECIAL_TIER")
	if err != nil || !found || v != "true" {
		t.Fatalf("Get = %q, %v, %v; want legacy fallback true", v, found, err)
	}
}

func TestSettingsErrorIsReturned(t *testing.T) {
	store := pilotStore()
	store.settingsErr = errors.New("db down")
	r := New(store, (&recorder{}).opts()...)
	if _, err := r.GetMany(tenantCtx("t1"), []string{"CURRENCY"}); err == nil {
		t.Fatal("want error")
	}
	if _, _, err := r.Get(tenantCtx("t1"), "CURRENCY"); err == nil {
		t.Fatal("want error from Get")
	}
	if _, err := r.IsEnabled(tenantCtx("t1"), "CURRENCY"); err == nil {
		t.Fatal("want error from IsEnabled")
	}
}

func TestOnlyFlagKeysSkipSettingsQuery(t *testing.T) {
	store := pilotStore()
	store.aliases = nil
	r := New(store, (&recorder{}).opts()...)
	_, _ = r.GetMany(tenantCtx("t1"), []string{"FF_NO_ALIAS"})
	if len(store.settingReqs) != 0 {
		t.Fatalf("settings queried for flag-only read: %v", store.settingReqs)
	}
}

func TestAliasRowsWithEmptyKeysAreIgnored(t *testing.T) {
	store := pilotStore()
	store.aliases = append(store.aliases, Alias{LegacyKey: "CURRENCY", NewKey: ""})
	rec := &recorder{}
	r := New(store, rec.opts()...)
	v, _, _ := r.Get(tenantCtx("t1"), "CURRENCY")
	if v != "SGD" || len(rec.deprecated) != 0 {
		t.Fatalf("CURRENCY = %q, deprecations %v", v, rec.deprecated)
	}
}

func TestWithTenantFunc(t *testing.T) {
	rec := &recorder{}
	type key struct{}
	opts := append(rec.opts(), WithTenantFunc(func(ctx context.Context) string {
		s, _ := ctx.Value(key{}).(string)
		return s
	}))
	r := New(pilotStore(), opts...)
	_, _, _ = r.Get(context.WithValue(context.Background(), key{}, "custom"), "IS_SHOW_SPECIAL_TIER")
	if len(rec.deprecated) != 1 || rec.deprecated[0].TenantID != "custom" {
		t.Fatalf("deprecations = %+v", rec.deprecated)
	}
}

func TestDefaultLoggersDoNotPanic(t *testing.T) {
	store := pilotStore()
	store.aliasesErr = errors.New("missing")
	r := New(store, WithService("svc"), WithWarnInterval(0))
	_, _, _ = r.Get(tenantCtx("t1"), "CURRENCY")

	store.aliasesErr = nil
	r = New(store, WithService("svc"))
	_, _, _ = r.Get(tenantCtx("t1"), "IS_SHOW_SPECIAL_TIER")
}

func TestDefaultService(t *testing.T) {
	t.Setenv("K_SERVICE", "cloud-run-svc")
	if got := New(pilotStore()).service; got != "cloud-run-svc" {
		t.Fatalf("service = %q", got)
	}
	t.Setenv("K_SERVICE", "")
	if got := New(pilotStore()).service; got == "" {
		t.Fatal("service should fall back to the binary name")
	}
}

func TestParseAndFormatBool(t *testing.T) {
	for _, v := range []string{"true", "TRUE", " 1 ", "yes", "on", "ENABLE", "enabled"} {
		if !ParseBool(v) {
			t.Errorf("ParseBool(%q) = false", v)
		}
	}
	for _, v := range []string{"", "false", "0", "DISABLE", "OFF", "NOTSET", "maybe"} {
		if ParseBool(v) {
			t.Errorf("ParseBool(%q) = true", v)
		}
	}
	if FormatBool(true) != "true" || FormatBool(false) != "false" {
		t.Error("FormatBool")
	}
}

func TestBuiltinAliasesApplyWhenTableMissing(t *testing.T) {
	store := pilotStore()
	store.aliasesErr = errors.New("Table 'setting_aliases' doesn't exist")
	store.flagsErr = errors.New("Table 'feature_flags' doesn't exist")
	rec := &recorder{}
	opts := append(rec.opts(), WithBuiltinAliases(
		Alias{LegacyKey: "IS_SHOW_SPECIAL_TIER", NewKey: "FF_SPECIAL_TIER", Target: TargetFeatureFlags, SourceOfTruth: SourceNew},
		Alias{LegacyKey: "", NewKey: "FF_IGNORED", Target: TargetFeatureFlags},
	))
	r := New(store, opts...)

	on, err := r.IsEnabled(tenantCtx("t1"), "FF_SPECIAL_TIER")
	if err != nil || !on {
		t.Fatalf("FF_SPECIAL_TIER = %v, %v; want legacy fallback true", on, err)
	}
}

func TestDatabaseAliasWinsOverBuiltin(t *testing.T) {
	store := pilotStore()
	rec := &recorder{}
	opts := append(rec.opts(), WithBuiltinAliases(
		// Conflicts with the DB row for IS_SHOW_SPECIAL_TIER: ignored.
		Alias{LegacyKey: "IS_SHOW_SPECIAL_TIER", NewKey: "FF_OTHER", Target: TargetFeatureFlags},
		// Conflicts with the DB row's new key: ignored.
		Alias{LegacyKey: "IS_SOMETHING", NewKey: "FF_SPECIAL_TIER", Target: TargetFeatureFlags},
		// Not in the DB: applies.
		Alias{LegacyKey: "CURRENCY", NewKey: "FF_CURRENCY_FLAG", Target: TargetFeatureFlags},
	))
	r := New(store, opts...)
	ctx := tenantCtx("t1")

	_, _, _ = r.Get(ctx, "IS_SHOW_SPECIAL_TIER")
	if len(rec.deprecated) != 1 || rec.deprecated[0].NewKey != "FF_SPECIAL_TIER" {
		t.Fatalf("deprecations = %+v, want the DB alias", rec.deprecated)
	}
	if _, found, _ := r.Get(ctx, "IS_SOMETHING"); found {
		t.Fatal("IS_SOMETHING must not resolve: its builtin alias was shadowed")
	}
	v, found, _ := r.Get(ctx, "CURRENCY")
	if !found || v != "false" {
		t.Fatalf("CURRENCY via builtin alias = %q, %v; want legacy normalized to false", v, found)
	}
}
