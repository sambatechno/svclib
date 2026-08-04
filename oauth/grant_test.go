package oauth

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

const gcQuery = "SELECT status FROM `tenantqapoints`.oauth_grants WHERE subject = \\? LIMIT 1"

func testClaims() *Claims { return &Claims{Tenant: "qapoints", Subject: "sub_abc"} }

func newMockGC(t *testing.T, opts ...GCOption) (*GrantChecker, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	gc := NewGrantChecker(db, opts...)
	return gc, mock, func() { db.Close() }
}

func TestGrant_Active(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	ok, err := gc.Active(context.Background(), testClaims())
	if !ok || err != nil {
		t.Fatalf("Active = (%v, %v), want (true, nil)", ok, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestGrant_Revoked(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("revoked"))

	ok, err := gc.Active(context.Background(), testClaims())
	if ok || !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("Active = (%v, %v), want (false, ErrGrantRevoked)", ok, err)
	}
}

func TestGrant_Missing(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").WillReturnError(sql.ErrNoRows)

	ok, err := gc.Active(context.Background(), testClaims())
	if ok || !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("Active = (%v, %v), want (false, ErrGrantRevoked)", ok, err)
	}
}

func TestGrant_CacheHitSkipsDB(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	// Only ONE query expected; the second call must be served from cache.
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	for i := 0; i < 2; i++ {
		if ok, err := gc.Active(context.Background(), testClaims()); !ok || err != nil {
			t.Fatalf("call %d: Active = (%v, %v)", i, ok, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected exactly one DB query: %v", err)
	}
}

func TestGrant_MissingNotCached(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	// Absence must NOT be cached: a grant created moments later should be seen immediately.
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	if _, err := gc.Active(context.Background(), testClaims()); !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("first call err = %v, want ErrGrantRevoked", err)
	}
	if ok, err := gc.Active(context.Background(), testClaims()); !ok || err != nil {
		t.Fatalf("second call = (%v, %v), want (true, nil)", ok, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected two DB queries: %v", err)
	}
}

func TestGrant_ServeStaleOnDBError(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	gc, mock, done := newMockGC(t, WithTTL(30*time.Second), WithStaleGrace(5*time.Minute), withClock(clock.Now))
	defer done()

	// 1st call: DB returns active -> cached at t0.
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
	if ok, err := gc.Active(context.Background(), testClaims()); !ok || err != nil {
		t.Fatalf("warm-up = (%v, %v)", ok, err)
	}

	// Advance past TTL (entry now stale) and make the DB fail. Within grace -> serve stale active.
	clock.advance(2 * time.Minute)
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").WillReturnError(errors.New("connection refused"))
	if ok, err := gc.Active(context.Background(), testClaims()); !ok || err != nil {
		t.Fatalf("stale-serve = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestGrant_UnavailableWhenNoCache(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").WillReturnError(errors.New("connection refused"))

	ok, err := gc.Active(context.Background(), testClaims())
	if ok || !errors.Is(err, ErrGrantUnavailable) {
		t.Fatalf("Active = (%v, %v), want (false, ErrGrantUnavailable)", ok, err)
	}
}

func TestGrant_StaleExpiredBeyondGrace(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	gc, mock, done := newMockGC(t, WithTTL(30*time.Second), WithStaleGrace(1*time.Minute), withClock(clock.Now))
	defer done()

	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
	gc.Active(context.Background(), testClaims())

	// Advance beyond ttl+grace: the stale entry is useless, so a DB error -> unavailable.
	clock.advance(10 * time.Minute)
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").WillReturnError(errors.New("down"))
	if _, err := gc.Active(context.Background(), testClaims()); !errors.Is(err, ErrGrantUnavailable) {
		t.Fatalf("err = %v, want ErrGrantUnavailable", err)
	}
}

func TestGrant_InvalidTenantFailsClosed(t *testing.T) {
	gc, _, done := newMockGC(t) // no query expected
	defer done()
	bad := &Claims{Tenant: "ten`ant", Subject: "s"} // backtick -> injection attempt
	if ok, err := gc.Active(context.Background(), bad); ok || !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("Active = (%v, %v), want (false, ErrGrantRevoked)", ok, err)
	}
}

func TestGrant_SingleflightCollapses(t *testing.T) {
	gc, mock, done := newMockGC(t)
	defer done()
	// One slow query; N concurrent callers must collapse to a single DB round-trip.
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillDelayFor(50 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := gc.Active(context.Background(), testClaims()); !ok || err != nil {
				t.Errorf("concurrent Active = (%v, %v)", ok, err)
			}
		}()
	}
	wg.Wait()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected a single collapsed query: %v", err)
	}
}

// fakeClock is a manually-advanced time source for deterministic TTL/grace tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
