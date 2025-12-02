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
//	    data, err := s.httpClient.SetURL("https://api.example.com/data").
//	        SetHeader("Authorization", "Bearer token").
//	        GET()
//	    // ...
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
	POST() ([]byte, error)
	GET() ([]byte, error)
	PUT() ([]byte, error)
	DELETE() ([]byte, error)
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

// SetURL sets the URL for the request
func (a *API) SetURL(urlStr string) IAPI {
	a.url = urlStr
	return a
}

// SetHeader sets a single header
func (a *API) SetHeader(key, value string) IAPI {
	a.headers[key] = value
	return a
}

// SetHeaders sets multiple headers
func (a *API) SetHeaders(headers map[string]string) IAPI {
	for key, value := range headers {
		a.headers[key] = value
	}
	return a
}

// SetBody sets the request body
func (a *API) SetBody(body interface{}) IAPI {
	a.body = body
	return a
}

// SetQuery sets a single query parameter
func (a *API) SetQuery(key, value string) IAPI {
	a.queryParams[key] = value
	return a
}

// SetQueries sets multiple query parameters
func (a *API) SetQueries(queryParams map[string]string) IAPI {
	for key, value := range queryParams {
		a.queryParams[key] = value
	}
	return a
}

// SetTimeout sets the timeout for the HTTP client.
// This overrides the default timeout and marks the call as external.
// To make an internal call (no timeout), use SetInternal() instead.
func (a *API) SetTimeout(timeout time.Duration) IAPI {
	a.timeout = timeout
	a.isInternal = false
	return a
}

// SetInternal marks this request as an internal API call, which will have no timeout.
// This is useful for service-to-service communication within the same infrastructure.
func (a *API) SetInternal() IAPI {
	a.isInternal = true
	a.timeout = 0
	return a
}

// POST executes a POST request
func (a *API) POST() ([]byte, error) {
	return a.executeRequest(http.MethodPost)
}

// GET executes a GET request
func (a *API) GET() ([]byte, error) {
	return a.executeRequest(http.MethodGet)
}

// PUT executes a PUT request
func (a *API) PUT() ([]byte, error) {
	return a.executeRequest(http.MethodPut)
}

// DELETE executes a DELETE request
func (a *API) DELETE() ([]byte, error) {
	return a.executeRequest(http.MethodDelete)
}

// executeRequest executes the HTTP request with the configured parameters
func (a *API) executeRequest(method string) ([]byte, error) {
	payload, err := a.buildPayload(method)
	if err != nil {
		return nil, err
	}

	requestURL, err := a.buildRequestURL()
	if err != nil {
		return nil, err
	}

	req, err := a.buildHTTPRequest(method, requestURL, payload)
	if err != nil {
		return nil, err
	}

	client := a.createHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return a.processResponse(resp)
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
	if _, exists := a.headers["Content-Type"]; !exists && payload != nil {
		a.headers["Content-Type"] = "application/json"
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

// processResponse reads the response body, logs it if needed, and validates status code
func (a *API) processResponse(resp *http.Response) ([]byte, error) {
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected http status: %v", resp.StatusCode)
	}

	return response, nil
}
