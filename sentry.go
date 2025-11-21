package svclib

import (
	"log"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// Config holds the configuration for Sentry initialization.
type Config struct {
	// DSN is the Sentry Data Source Name
	DSN string

	// Environment specifies the environment (e.g., "local", "development", "production")
	Environment string

	// TracesSampleRate is the sample rate for traces (0.0 to 1.0)
	// Recommended: 1.0 for local/dev, 0.1 for production
	TracesSampleRate float64

	// EnableTracing enables distributed tracing
	EnableTracing bool

	// Repanic controls whether to repanic after capturing an error
	// Default is true (matches integration-service behavior)
	Repanic bool
}

// Init initializes Sentry with the provided configuration and returns an HTTP handler.
//
// Example usage:
//
//	handler, err := svclib.Init(svclib.Config{
//	    DSN:              "https://...",
//	    Environment:      "production",
//	    TracesSampleRate: 0.1,
//	    EnableTracing:    true,
//	    Repanic:          true,
//	})
//	if err != nil {
//	    log.Printf("Sentry initialization failed: %v\n", err)
//	}
//	defer sentry.Flush(2 * time.Second)
//
//	// Use the handler in your HTTP middleware chain
//	mux.Handle("/", handler.Handle(yourHandler))
func Init(cfg Config) (*sentryhttp.Handler, error) {
	// Set default for Repanic if not explicitly set
	repanic := cfg.Repanic
	if !cfg.Repanic && cfg.Environment == "" {
		// If Repanic is false and Environment is empty, assume default behavior
		repanic = true
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.DSN,
		Environment:      cfg.Environment,
		TracesSampleRate: cfg.TracesSampleRate,
		EnableTracing:    cfg.EnableTracing,
	})
	if err != nil {
		log.Printf("Sentry initialization failed: %v\n", err)
		return nil, err
	}

	return sentryhttp.New(sentryhttp.Options{
		Repanic: repanic,
	}), nil
}
