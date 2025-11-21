package svclib

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantTaggingMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify tenant ID is in context
		tenantID, ok := GetTenantID(r.Context())
		if ok && tenantID != "" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	middleware := TenantTaggingMiddleware(handler)

	t.Run("without tenant in context", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		middleware.ServeHTTP(rec, req)

		// Should still work, just without tenant
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}
	})

	t.Run("with tenant in context", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		ctx := WithTenantID(req.Context(), "test-tenant")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		middleware.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})

	t.Run("with subdomain header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		ctx := WithTenantID(req.Context(), "test-tenant")
		req = req.WithContext(ctx)
		req.Header.Set("x-sub-domain", "testsubdomain")
		rec := httptest.NewRecorder()

		middleware.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})
}

func TestTenantTaggingMiddlewareWithExtractor(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	extractor := func(r *http.Request) (tenantID, subdomain string) {
		return "extracted-tenant", "extracted-subdomain"
	}

	middleware := TenantTaggingMiddlewareWithExtractor(extractor)
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Should not panic even without Sentry hub
	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestTenantTaggingMiddlewareWithExtractor_EmptyValues(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	extractor := func(r *http.Request) (tenantID, subdomain string) {
		return "", "" // Return empty values
	}

	middleware := TenantTaggingMiddlewareWithExtractor(extractor)
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Should not panic with empty values
	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestMiddleware_PreservesResponseWriter(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("test response"))
	})

	middleware := TenantTaggingMiddleware(handler)

	req := httptest.NewRequest("POST", "/test", nil)
	ctx := WithTenantID(req.Context(), "test-tenant")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	middleware.ServeHTTP(rec, req)

	// Verify response is preserved
	if rec.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", rec.Code)
	}

	if rec.Header().Get("X-Custom-Header") != "test-value" {
		t.Error("Expected custom header to be preserved")
	}

	if rec.Body.String() != "test response" {
		t.Errorf("Expected body 'test response', got %q", rec.Body.String())
	}
}

