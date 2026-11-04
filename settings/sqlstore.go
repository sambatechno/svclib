package settings

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Queryer is the subset of *sql.DB / *sql.Tx / *sql.Conn the SQL store needs.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// SQLStore is a Store over database/sql for services that don't have their own
// query layer. Table maps a bare table name to the qualified name for the
// tenant in ctx, e.g. "settings" -> "`tenant<id>`.settings". A nil Table uses
// the bare name (the connection's default schema).
type SQLStore struct {
	DB    Queryer
	Table func(ctx context.Context, table string) string
}

func (s SQLStore) table(ctx context.Context, name string) string {
	if s.Table == nil {
		return name
	}
	return s.Table(ctx, name)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func args(keys []string) []any {
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out
}

// Settings implements Store.
func (s SQLStore) Settings(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	q := fmt.Sprintf("SELECT `key`, value FROM %s WHERE `key` IN (%s)", s.table(ctx, "settings"), placeholders(len(keys)))
	rows, err := s.DB.QueryContext(ctx, q, args(keys)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// FeatureFlags implements Store.
func (s SQLStore) FeatureFlags(ctx context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	q := fmt.Sprintf("SELECT `key`, is_enabled FROM %s WHERE `key` IN (%s)", s.table(ctx, "feature_flags"), placeholders(len(keys)))
	rows, err := s.DB.QueryContext(ctx, q, args(keys)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var on bool
		if err := rows.Scan(&k, &on); err != nil {
			return nil, err
		}
		out[k] = on
	}
	return out, rows.Err()
}

// Aliases implements Store.
func (s SQLStore) Aliases(ctx context.Context) ([]Alias, error) {
	q := fmt.Sprintf("SELECT legacy_key, new_key, target, source_of_truth FROM %s", s.table(ctx, "setting_aliases"))
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alias
	for rows.Next() {
		var a Alias
		if err := rows.Scan(&a.LegacyKey, &a.NewKey, &a.Target, &a.SourceOfTruth); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
