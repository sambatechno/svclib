package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout is the default timeout for external API calls (30 seconds)
const DefaultTimeout = 30 * time.Second

// ErrUnmarshalJSON is the error message when JSON unmarshaling fails
const ErrUnmarshalJSON = "failed to unmarshal JSON response: %w"

// ContentTypeHeader is the HTTP Content-Type header name
const ContentTypeHeader = "Content-Type"

// ContentTypeJSON is the JSON content type value
const ContentTypeJSON = "application/json"

// IAPI defines the interface for HTTP client operations.
// This interface enables dependency injection and makes it easy to mock for unit testing.
//
// Example usage in services:
//
//	type MyService struct {
//	    httpClient http.IAPI
//	}
//
//	func NewMyService(client http.IAPI) *MyService {
//	    return &MyService{httpClient: client}
//	}
//
//	func (s *MyService) DoSomething() error {
//	    resp, data, err := s.httpClient.SetURL("https://api.example.com/data").
//	        SetHeader("Authorization", "Bearer token").
//	        GET()
//	    if err != nil {
//	        return err
//	    }
//	    // Response body is automatically closed after reading
//	    // Access resp.StatusCode, resp.Header, etc.
//	    if resp.StatusCode != http.StatusOK {
//	        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
//	    }
//	    // Process data...
//	    return nil
//	}
//
// For testing, you can create a mock implementation of IAPI.
type IAPI interface {
	SetURL(urlStr string) IAPI
	SetHeader(key, value string) IAPI
	SetHeaders(headers map[string]string) IAPI
	SetBody(body interface{}) IAPI
	SetQuery(key, value string) IAPI
	SetQueries(queryParams map[string]string) IAPI
	SetTimeout(timeout time.Duration) IAPI
	SetInternal() IAPI
	POST() (*http.Response, []byte, error)
	GET() (*http.Response, []byte, error)
	PUT() (*http.Response, []byte, error)
	DELETE() (*http.Response, []byte, error)
}

// API is the HTTP client implementation
type API struct {
	url         string
	headers     map[string]string
	body        interface{}
	queryParams map[string]string
	timeout     time.Duration
	isInternal  bool
}

// NewAPI creates a new API instance
func NewAPI() IAPI {
	return &API{
		headers:     make(map[string]string),
		queryParams: make(map[string]string),
	}
}

// copy creates a deep copy of the API instance for immutable builder pattern
func (a *API) copy() *API {
	newHeaders := make(map[string]string, len(a.headers))
	for k, v := range a.headers {
		newHeaders[k] = v
	}
	newQueryParams := make(map[string]string, len(a.queryParams))
	for k, v := range a.queryParams {
		newQueryParams[k] = v
	}
	return &API{
		url:         a.url,
		headers:     newHeaders,
		body:        a.body,
		queryParams: newQueryParams,
		timeout:     a.timeout,
		isInternal:  a.isInternal,
	}
}

// SetURL sets the URL for the request
// Returns a new API instance to ensure immutability and thread safety.
func (a *API) SetURL(urlStr string) IAPI {
	newAPI := a.copy()
	newAPI.url = urlStr
	return newAPI
}

// SetHeader sets a single header
// Returns a new API instance to ensure immutability and thread safety.
func (a *API) SetHeader(key, value string) IAPI {
	newAPI := a.copy()
	newAPI.headers[key] = value
	return newAPI
}

// SetHeaders sets multiple headers
// Returns a new API instance to ensure immutability and thread safety.
// The input map is copied to prevent external modifications.
func (a *API) SetHeaders(headers map[string]string) IAPI {
	newAPI := a.copy()
	for key, value := range headers {
		newAPI.headers[key] = value
	}
	return newAPI
}

// SetBody sets the request body
// Returns a new API instance to ensure immutability and thread safety.
func (a *API) SetBody(body interface{}) IAPI {
	newAPI := a.copy()
	newAPI.body = body
	return newAPI
}

// SetQuery sets a single query parameter
// Returns a new API instance to ensure immutability and thread safety.
func (a *API) SetQuery(key, value string) IAPI {
	newAPI := a.copy()
	newAPI.queryParams[key] = value
	return newAPI
}

// SetQueries sets multiple query parameters
// Returns a new API instance to ensure immutability and thread safety.
// The input map is copied to prevent external modifications.
func (a *API) SetQueries(queryParams map[string]string) IAPI {
	newAPI := a.copy()
	for key, value := range queryParams {
		newAPI.queryParams[key] = value
	}
	return newAPI
}

// SetTimeout sets the timeout for the HTTP client.
// This overrides the default timeout and marks the call as external.
// To make an internal call (no timeout), use SetInternal() instead.
// Returns a new API instance to ensure immutability and thread safety.
func (a *API) SetTimeout(timeout time.Duration) IAPI {
	newAPI := a.copy()
	newAPI.timeout = timeout
	newAPI.isInternal = false
	return newAPI
}

// SetInternal marks this request as an internal API call, which will have no timeout.
// This is useful for service-to-service communication within the same infrastructure.
// Returns a new API instance to ensure immutability and thread safety.
func (a *API) SetInternal() IAPI {
	newAPI := a.copy()
	newAPI.isInternal = true
	newAPI.timeout = 0
	return newAPI
}

// POST executes a POST request and returns the response, body, and error.
// The response body is automatically closed after reading.
// The response struct (StatusCode, Header, etc.) remains accessible.
func (a *API) POST() (*http.Response, []byte, error) {
	return a.executeRequest(http.MethodPost)
}

// GET executes a GET request and returns the response, body, and error.
// The response body is automatically closed after reading.
// The response struct (StatusCode, Header, etc.) remains accessible.
func (a *API) GET() (*http.Response, []byte, error) {
	return a.executeRequest(http.MethodGet)
}

// PUT executes a PUT request and returns the response, body, and error.
// The response body is automatically closed after reading.
// The response struct (StatusCode, Header, etc.) remains accessible.
func (a *API) PUT() (*http.Response, []byte, error) {
	return a.executeRequest(http.MethodPut)
}

// DELETE executes a DELETE request and returns the response, body, and error.
// The response body is automatically closed after reading.
// The response struct (StatusCode, Header, etc.) remains accessible.
func (a *API) DELETE() (*http.Response, []byte, error) {
	return a.executeRequest(http.MethodDelete)
}

// executeRequest executes the HTTP request with the configured parameters
// Returns the response, body, and error. The response body is automatically closed after reading.
func (a *API) executeRequest(method string) (*http.Response, []byte, error) {
	payload, err := a.buildPayload(method)
	if err != nil {
		return nil, nil, err
	}

	requestURL, err := a.buildRequestURL()
	if err != nil {
		return nil, nil, err
	}

	req, err := a.buildHTTPRequest(method, requestURL, payload)
	if err != nil {
		return nil, nil, err
	}

	client := a.createHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	// Read the body first, then close it immediately
	// Once the body is read, we can close it - the response struct (headers, status, etc.)
	// remains fully accessible after closing the body stream.
	body, err := a.processResponse(resp)
	if err != nil {
		resp.Body.Close()
		return nil, nil, err
	}
	// Close the body immediately after reading - we've already consumed the stream
	// The response metadata (StatusCode, Header, etc.) is still accessible
	resp.Body.Close()

	return resp, body, nil
}

// buildPayload creates the request payload and logs it if needed
func (a *API) buildPayload(method string) (io.Reader, error) {
	if !a.shouldIncludeBody(method) {
		return nil, nil
	}

	jsonBody, err := json.Marshal(a.body)
	if err != nil {
		return nil, err
	}

	return bytes.NewBuffer(jsonBody), nil
}

// shouldIncludeBody checks if the method supports a request body
func (a *API) shouldIncludeBody(method string) bool {
	return a.body != nil && (method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch)
}

// buildRequestURL parses the URL and adds query parameters
func (a *API) buildRequestURL() (string, error) {
	parsedURL, err := url.Parse(a.url)
	if err != nil {
		return "", err
	}

	if len(a.queryParams) > 0 {
		q := parsedURL.Query()
		for key, value := range a.queryParams {
			q.Set(key, value)
		}
		parsedURL.RawQuery = q.Encode()
	}

	return parsedURL.String(), nil
}

// buildHTTPRequest creates the HTTP request with headers and content type
func (a *API) buildHTTPRequest(method, requestURL string, payload io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, requestURL, payload)
	if err != nil {
		return nil, err
	}

	a.setContentTypeIfNeeded(payload)
	a.setHeaders(req)

	return req, nil
}

// setContentTypeIfNeeded sets default content type if not specified and payload exists
func (a *API) setContentTypeIfNeeded(payload io.Reader) {
	if _, exists := a.headers[ContentTypeHeader]; !exists && payload != nil {
		a.headers[ContentTypeHeader] = ContentTypeJSON
	}
}

// setHeaders adds all headers to the request
func (a *API) setHeaders(req *http.Request) {
	for key, value := range a.headers {
		req.Header.Set(key, value)
	}
}

// createHTTPClient creates an HTTP client with appropriate timeout configuration.
// Internal calls have no timeout, external calls use default 30s timeout unless overridden.
func (a *API) createHTTPClient() *http.Client {
	client := &http.Client{}

	// Internal calls have no timeout
	if a.isInternal {
		return client
	}

	// Use explicit timeout if set, otherwise use default
	if a.timeout > 0 {
		client.Timeout = a.timeout
	} else {
		client.Timeout = DefaultTimeout
	}

	return client
}

// processResponse reads the response body.
// Note: This no longer validates status codes - the caller can check resp.StatusCode.
// This allows for custom error handling based on status codes.
func (a *API) processResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, nil
}
