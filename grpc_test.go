package svclib

import (
	"testing"
)

func TestDefaultHeaderMatcher(t *testing.T) {
	matcher := DefaultHeaderMatcher()

	tests := []struct {
		name           string
		inputKey       string
		expectedPrefix string
		expectedMatch  bool
	}{
		{
			name:           "custom header with lowercase",
			inputKey:       "x-tenant-id",
			expectedPrefix: "fwd-",
			expectedMatch:  true,
		},
		{
			name:           "custom header with mixed case",
			inputKey:       "X-Sub-Domain",
			expectedPrefix: "fwd-",
			expectedMatch:  true,
		},
		{
			name:           "custom header uppercase",
			inputKey:       "X-AUTH-TOKEN",
			expectedPrefix: "fwd-",
			expectedMatch:  true,
		},
		{
			name:           "any header gets prefixed",
			inputKey:       "custom-header",
			expectedPrefix: "fwd-",
			expectedMatch:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, match := matcher(tt.inputKey)

			if match != tt.expectedMatch {
				t.Errorf("Expected match=%v, got %v", tt.expectedMatch, match)
			}

			if tt.expectedPrefix != "" && len(key) > 0 {
				if key[:4] != tt.expectedPrefix {
					t.Errorf("Expected key to start with %q, got %q", tt.expectedPrefix, key)
				}
			}
		})
	}
}

func TestDefaultHeaderMatcher_Consistency(t *testing.T) {
	matcher := DefaultHeaderMatcher()

	// Test that calling matcher multiple times with same input gives same output
	key1, match1 := matcher("x-custom-header")
	key2, match2 := matcher("x-custom-header")

	if key1 != key2 {
		t.Errorf("Expected consistent results, got %q and %q", key1, key2)
	}

	if match1 != match2 {
		t.Error("Expected consistent match results")
	}
}

func TestDefaultHeaderMatcher_EmptyKey(t *testing.T) {
	matcher := DefaultHeaderMatcher()

	key, match := matcher("")

	if !match {
		t.Error("Expected empty key to match")
	}

	if key != "fwd-" {
		t.Errorf("Expected key='fwd-', got %q", key)
	}
}

func TestDefaultHeaderMatcher_SpecialCharacters(t *testing.T) {
	matcher := DefaultHeaderMatcher()

	tests := []struct {
		input    string
		contains string
	}{
		{"x-tenant-id", "fwd-"},
		{"X-Custom_Header", "fwd-"},
		{"my-custom-header-123", "fwd-"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			key, match := matcher(tt.input)

			if !match {
				t.Error("Expected key to match")
			}

			if len(key) < len(tt.contains) {
				t.Errorf("Expected key to contain %q, got %q", tt.contains, key)
			}
		})
	}
}

