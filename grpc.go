package svclib

import (
	"net/textproto"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

// DefaultHeaderMatcher is the standard header matcher for grpc-gateway.
// It forwards all headers with "fwd-" prefix, which is the standard pattern
// used across all services.
//
// This function is 100% identical across integration-service, user-service,
// order-service, payment-service, and kds-management-service.
//
// Example usage:
//
//	grpcMux := runtime.NewServeMux(
//	    runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),
//	)
func DefaultHeaderMatcher() runtime.HeaderMatcherFunc {
	return func(key string) (string, bool) {
		k, ok := runtime.DefaultHeaderMatcher(key)
		if ok {
			return k, ok
		}
		key = textproto.CanonicalMIMEHeaderKey(key)
		return "fwd-" + key, true
	}
}

