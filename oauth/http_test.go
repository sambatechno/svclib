package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// verifyFunc is a Verifier backed by a function, so adapter tests don't need real JWTs.
type verifyFunc func(ctx context.Context, token string) (*Claims, error)

func (f verifyFunc) Verify(ctx context.Context, token string) (*Claims, error) { return f(ctx, token) }

// okVerifier returns fixed claims for any non-empty token, ErrTokenMissing for "".
func okVerifier(c *Claims) Verifier {
	return verifyFunc(func(_ context.Context, token string) (*Claims, error) {
		if token == "" {
			return nil, ErrTokenMissing
		}
		return c, nil
	})
}

func doReq(h http.Handler, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTP_Success(t *testing.T) {
	want := &Claims{Subject: "sub_1", Tenant: "qapoints", Scopes: []string{ScopeOrders}}
	var gotTenant string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClaimsFrom(r.Context())
		if !ok {
			t.Fatal("claims not in context")
		}
		gotTenant = c.Tenant
		w.WriteHeader(http.StatusOK)
	})
	h := Middleware(okVerifier(want), WithRequiredScopes(ScopeOrders))(next)

	w := doReq(h, "Bearer abc")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if gotTenant != "qapoints" {
		t.Errorf("tenant in ctx = %q", gotTenant)
	}
}

func TestHTTP_ErrorMapping(t *testing.T) {
	claims := &Claims{Subject: "s", Tenant: "qapoints", Scopes: []string{ScopeOrders}}
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { nextCalled = true })

	cases := []struct {
		name       string
		verifier   Verifier
		opts       []MWOption
		auth       string
		wantStatus int
		wantWWW    string
	}{
		{"missing", okVerifier(claims), nil, "", 401, "Bearer"},
		{"not-bearer", okVerifier(claims), nil, "Basic xyz", 401, "Bearer"},
		{"invalid", verifyFunc(func(context.Context, string) (*Claims, error) { return nil, ErrTokenInvalid }), nil, "Bearer bad", 401, `Bearer error="invalid_token"`},
		{"insufficient-scope", okVerifier(&Claims{Subject: "s", Tenant: "t", Scopes: []string{"other"}}), []MWOption{WithRequiredScopes(ScopeOrders)}, "Bearer x", 403, `Bearer error="insufficient_scope"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled = false
			h := Middleware(tc.verifier, tc.opts...)(next)
			w := doReq(h, tc.auth)
			if w.Code != tc.wantStatus {
				t.Errorf("code = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != tc.wantWWW {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tc.wantWWW)
			}
			if nextCalled {
				t.Error("next handler should not be called on auth failure")
			}
		})
	}
}

func TestHTTP_GrantRevoked401(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("revoked"))
	gc := NewGrantChecker(db)

	claims := &Claims{Subject: "sub_abc", Tenant: "qapoints", Scopes: []string{ScopeOrders}}
	nextCalled := false
	h := Middleware(okVerifier(claims), WithGrantCheck(gc))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true }))

	w := doReq(h, "Bearer x")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != `Bearer error="invalid_token"` {
		t.Errorf("WWW-Authenticate = %q", got)
	}
	if nextCalled {
		t.Error("handler must not run on a revoked grant")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestHTTP_GrantUnavailable503(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery(gcQuery).WithArgs("sub_abc").WillReturnError(errors.New("connection refused"))
	gc := NewGrantChecker(db)

	claims := &Claims{Subject: "sub_abc", Tenant: "qapoints", Scopes: []string{ScopeOrders}}
	nextCalled := false
	h := Middleware(okVerifier(claims), WithGrantCheck(gc))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true }))

	w := doReq(h, "Bearer x")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("503 should carry no WWW-Authenticate, got %q", got)
	}
	if nextCalled {
		t.Error("handler must not run when grant status is unavailable")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestHTTP_CustomErrorHandler(t *testing.T) {
	called := false
	h := Middleware(okVerifier(nil),
		WithErrorHandler(func(w http.ResponseWriter, _ *http.Request, err error) {
			called = true
			if !errors.Is(err, ErrTokenMissing) {
				t.Errorf("err = %v, want ErrTokenMissing", err)
			}
			w.WriteHeader(http.StatusTeapot)
		}),
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	w := doReq(h, "")
	if !called || w.Code != http.StatusTeapot {
		t.Errorf("custom handler not used: called=%v code=%d", called, w.Code)
	}
}
