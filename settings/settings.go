// Package settings resolves tenant settings and feature flags by name, so a
// renamed key keeps working under both its old and its new name.
//
// The tenant database has three parts (dashboard-app ADR 0013):
//
//   - settings: key/value rows. Merchant toggles are named IS_ENABLE_<X>.
//   - feature_flags: Cata-controlled flags named FF_<X> (is_enabled 0/1).
//   - setting_aliases: legacy key -> new key, with the table that holds the
//     new key and which row is the source of truth while both exist.
//
// A Resolver reads through a Store (each service adapts its own DB layer),
// answers for legacy and new names alike, and logs a WARNING naming the
// calling service whenever a legacy key is read, so the remaining readers can
// be found in Cloud Logging and migrated:
//
//	severity=WARNING
//	jsonPayload.message="svclib/settings: deprecated setting key"
//
// Usage:
//
//	r := settings.New(store, settings.WithService("backoffice-v2-be"))
//	on, err := r.IsEnabled(ctx, "FF_SPECIAL_TIER")
//	vals, err := r.GetMany(ctx, []string{"CURRENCY", "IS_SHOW_SPECIAL_TIER"})
package settings

import (
	"context"
	"strings"
)

// Alias targets: the table that holds Alias.NewKey.
const (
	TargetFeatureFlags = "feature_flags"
	TargetSettings     = "settings"
)

// Alias sources of truth: the row the resolver reads while both rows exist.
const (
	SourceLegacy = "legacy"
	SourceNew    = "new"
)

// FeatureFlagPrefix marks keys that live in the feature_flags table.
const FeatureFlagPrefix = "FF_"

// Alias is one row of setting_aliases.
type Alias struct {
	LegacyKey     string
	NewKey        string
	Target        string // TargetFeatureFlags or TargetSettings
	SourceOfTruth string // SourceLegacy or SourceNew
}

// Store reads one tenant's rows. The tenant comes from ctx, the same way the
// service scopes every other query.
//
// Missing rows are left out of the returned maps; they are not errors.
type Store interface {
	// Settings returns settings.value by key.
	Settings(ctx context.Context, keys []string) (map[string]string, error)
	// FeatureFlags returns feature_flags.is_enabled by key.
	FeatureFlags(ctx context.Context, keys []string) (map[string]bool, error)
	// Aliases returns every setting_aliases row.
	Aliases(ctx context.Context) ([]Alias, error)
}

// ParseBool reads the boolean forms found in settings values: true, 1, yes,
// on, enable, enabled (any case, surrounding spaces ignored). Anything else,
// including the empty string, is false.
func ParseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on", "enable", "enabled":
		return true
	}
	return false
}

// FormatBool is the canonical settings form of a boolean: "true" or "false".
func FormatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
