package svclib

import (
	"os"
	"testing"
)

func TestParseMockTenant(t *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	t.Run("no arguments", func(t *testing.T) {
		os.Args = []string{"program"}
		tenant := ParseMockTenant()
		if tenant != "" {
			t.Errorf("Expected empty string, got %q", tenant)
		}
	})

	t.Run("with tenant argument", func(t *testing.T) {
		os.Args = []string{"program", "test-tenant"}
		tenant := ParseMockTenant()
		if tenant != "test-tenant" {
			t.Errorf("Expected 'test-tenant', got %q", tenant)
		}
	})

	t.Run("with multiple arguments", func(t *testing.T) {
		os.Args = []string{"program", "first-tenant", "second-arg"}
		tenant := ParseMockTenant()
		if tenant != "first-tenant" {
			t.Errorf("Expected 'first-tenant', got %q", tenant)
		}
	})

	t.Run("empty tenant argument", func(t *testing.T) {
		os.Args = []string{"program", ""}
		tenant := ParseMockTenant()
		if tenant != "" {
			t.Errorf("Expected empty string, got %q", tenant)
		}
	})

	t.Run("tenant with special characters", func(t *testing.T) {
		os.Args = []string{"program", "tenant-123_test"}
		tenant := ParseMockTenant()
		if tenant != "tenant-123_test" {
			t.Errorf("Expected 'tenant-123_test', got %q", tenant)
		}
	})
}

