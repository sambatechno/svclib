package svclib

import (
	"log"
	"os"
)

// ParseMockTenant extracts mock tenant from command line args for testing.
// This is useful for local development and testing when you want to simulate
// a specific tenant without going through the full authentication flow.
//
// This function is 100% identical across integration-service, user-service,
// order-service, payment-service, and kds-management-service.
//
// Usage:
//
//	func main() {
//	    utils.ReadConfig()
//	    sentryHandler := initSentry()
//
//	    // Get mock tenant from command line
//	    mockedTenant := svclib.ParseMockTenant()
//	    if mockedTenant != "" {
//	        log.Printf("Using mock tenant: %s\n", mockedTenant)
//	    }
//
//	    // ... rest of main ...
//	}
//
// Run with:
//
//	go run main.go mock-tenant-name
func ParseMockTenant() string {
	if len(os.Args) <= 1 {
		return ""
	}
	tenant := os.Args[1]
	if tenant != "" {
		log.Printf("Using mock tenant: %s\n", tenant)
	}
	return tenant
}
