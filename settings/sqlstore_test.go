package settings

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMock(t *testing.T) (SQLStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return SQLStore{DB: db, Table: func(_ context.Context, name string) string { return "`tenantX`." + name }}, mock
}

func TestSQLStoreSettings(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM `tenantX`.settings WHERE `key` IN (?,?)")).
		WithArgs("A", "B").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("A", "1"))

	got, err := s.Settings(context.Background(), []string{"A", "B"})
	if err != nil || len(got) != 1 || got["A"] != "1" {
		t.Fatalf("got %v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLStoreFeatureFlags(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, is_enabled FROM `tenantX`.feature_flags WHERE `key` IN (?)")).
		WithArgs("FF_A").
		WillReturnRows(sqlmock.NewRows([]string{"key", "is_enabled"}).AddRow("FF_A", 1))

	got, err := s.FeatureFlags(context.Background(), []string{"FF_A"})
	if err != nil || !got["FF_A"] {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSQLStoreAliases(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	s := SQLStore{DB: db} // nil Table: bare names
	mock.ExpectQuery(regexp.QuoteMeta("SELECT legacy_key, new_key, target, source_of_truth FROM setting_aliases")).
		WillReturnRows(sqlmock.NewRows([]string{"legacy_key", "new_key", "target", "source_of_truth"}).
			AddRow("IS_SHOW_X", "FF_X", TargetFeatureFlags, SourceNew))

	got, err := s.Aliases(context.Background())
	if err != nil || len(got) != 1 || got[0] != (Alias{"IS_SHOW_X", "FF_X", TargetFeatureFlags, SourceNew}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSQLStoreEmptyKeysSkipQuery(t *testing.T) {
	s, mock := newMock(t)
	if got, err := s.Settings(context.Background(), nil); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if got, err := s.FeatureFlags(context.Background(), nil); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	s, mock := newMock(t)
	mock.ExpectQuery("settings").WillReturnError(boom)
	mock.ExpectQuery("feature_flags").WillReturnError(boom)
	mock.ExpectQuery("setting_aliases").WillReturnError(boom)
	if _, err := s.Settings(context.Background(), []string{"A"}); !errors.Is(err, boom) {
		t.Errorf("Settings err = %v", err)
	}
	if _, err := s.FeatureFlags(context.Background(), []string{"A"}); !errors.Is(err, boom) {
		t.Errorf("FeatureFlags err = %v", err)
	}
	if _, err := s.Aliases(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Aliases err = %v", err)
	}

	// Scan errors: wrong column count.
	mock.ExpectQuery("settings").WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("A"))
	mock.ExpectQuery("feature_flags").WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("A"))
	mock.ExpectQuery("setting_aliases").WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("A"))
	if _, err := s.Settings(context.Background(), []string{"A"}); err == nil {
		t.Error("want scan error")
	}
	if _, err := s.FeatureFlags(context.Background(), []string{"A"}); err == nil {
		t.Error("want scan error")
	}
	if _, err := s.Aliases(context.Background()); err == nil {
		t.Error("want scan error")
	}
}

// The SQL store plugs straight into the resolver.
func TestSQLStoreWithResolver(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("setting_aliases").WillReturnRows(sqlmock.NewRows([]string{"legacy_key", "new_key", "target", "source_of_truth"}).
		AddRow("IS_SHOW_X", "FF_X", TargetFeatureFlags, SourceNew))
	mock.ExpectQuery("settings").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("IS_SHOW_X", "false"))
	mock.ExpectQuery("feature_flags").WillReturnRows(sqlmock.NewRows([]string{"key", "is_enabled"}).AddRow("FF_X", 1))

	rec := &recorder{}
	r := New(s, rec.opts()...)
	on, err := r.IsEnabled(tenantCtx("t1"), "IS_SHOW_X")
	if err != nil || !on {
		t.Fatalf("IsEnabled = %v, %v; want the feature flag value", on, err)
	}
	if len(rec.deprecated) != 1 {
		t.Fatalf("deprecations = %v", rec.deprecated)
	}
}
