package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Test constants to avoid duplication
const (
	testURL             = "https://api.example.com/test"
	testBearerToken     = "Bearer token123"
	headerContentType   = "Content-Type"
	headerAuthorization = "Authorization"
	contentTypeJSON     = "application/json"
	contentTypeXML      = "application/xml"
	testQueryPage       = "page"
	testQueryLimit      = "limit"
	testQueryPageValue  = "1"
	testQueryLimitValue = "10"
	testBodyName        = "name"
	testBodyValue       = "test"
	testStatusSuccess   = "success"
	testStatusCreated   = "created"
	testPath            = "/test"
)

// Error message constants
const (
	errMsgExpectedPageQuery   = "Expected page=1 query param, got %s"
	errMsgExpectedBodyName    = "Expected body with name=test, got %v"
	errMsgPOSTRequestFailed   = "POST request failed: %v"
	errMsgGETRequestFailed    = "GET request failed: %v"
	errMsgPUTRequestFailed    = "PUT request failed: %v"
	errMsgDELETERequestFailed = "DELETE request failed: %v"
	errMsgExpectedSuccess     = "Expected success status, got %s"
	errMsgExpectedCreated     = "Expected created status, got %s"
)

func TestNewAPI(t *testing.T) {
	api := NewAPI()

	if api == nil {
		t.Fatal("NewAPI() should not return nil")
	}

	// Test that it returns an IAPI interface
	var _ IAPI = api
}

func TestSetURL(t *testing.T) {
	api := NewAPI()
	url := testURL

	result := api.SetURL(url)

	// Test immutable builder pattern - should return a new instance
	if result == api {
		t.Error("SetURL should return a new instance (immutable builder pattern)")
	}

	// Test that method chaining works (returns IAPI interface)
	var _ IAPI = result

	// Test that URL is set (we can't directly access the field, but we can test via executeRequest)
	// This will be tested indirectly through other tests
}

func TestSetHeader(t *testing.T) {
	api := NewAPI()
	key := headerAuthorization
	value := testBearerToken

	result := api.SetHeader(key, value)

	// Test immutable builder pattern - should return a new instance
	if result == api {
		t.Error("SetHeader should return a new instance (immutable builder pattern)")
	}

	// Test that method chaining works
	var _ IAPI = result
}

func TestSetHeaders(t *testing.T) {
	api := NewAPI()
	headers := map[string]string{
		headerAuthorization: testBearerToken,
		headerContentType:   contentTypeJSON,
	}

	result := api.SetHeaders(headers)

	// Test immutable builder pattern - should return a new instance
	if result == api {
		t.Error("SetHeaders should return a new instance (immutable builder pattern)")
	}

	// Test that method chaining works
	var _ IAPI = result
}

func TestSetBody(t *testing.T) {
	api := NewAPI()
	body := map[string]string{testBodyName: testBodyValue}

	result := api.SetBody(body)

	// Test immutable builder pattern - should return a new instance
	if result == api {
		t.Error("SetBody should return a new instance (immutable builder pattern)")
	}

	// Test that method chaining works
	var _ IAPI = result
}

func TestSetQuery(t *testing.T) {
	api := NewAPI()
	key := testQueryPage
	value := testQueryPageValue

	result := api.SetQuery(key, value)

	// Test immutable builder pattern - should return a new instance
	if result == api {
		t.Error("SetQuery should return a new instance (immutable builder pattern)")
	}

	// Test that method chaining works
	var _ IAPI = result
}

func TestSetQueries(t *testing.T) {
	api := NewAPI()
	queries := map[string]string{
		testQueryPage:  testQueryPageValue,
		testQueryLimit: testQueryLimitValue,
	}

	result := api.SetQueries(queries)

	// Test immutable builder pattern - should return a new instance
	if result == api {
		t.Error("SetQueries should return a new instance (immutable builder pattern)")
	}

	// Test that method chaining works
	var _ IAPI = result
}

func TestPOSTSuccess(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		// Verify URL
		if r.URL.Path != testPath {
			t.Errorf("Expected %s path, got %s", testPath, r.URL.Path)
		}

		// Verify headers
		if r.Header.Get(headerAuthorization) != testBearerToken {
			t.Errorf("Expected Authorization header, got %s", r.Header.Get(headerAuthorization))
		}

		// Verify query parameters
		if r.URL.Query().Get(testQueryPage) != testQueryPageValue {
			t.Errorf(errMsgExpectedPageQuery, r.URL.Query().Get(testQueryPage))
		}

		// Verify body
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body[testBodyName] != testBodyValue {
			t.Errorf(errMsgExpectedBodyName, body)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test API call
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL+testPath).
		SetHeader(headerAuthorization, testBearerToken).
		SetBody(map[string]string{testBodyName: testBodyValue}).
		SetQuery(testQueryPage, testQueryPageValue).
		POST()

	if err != nil {
		t.Fatalf(errMsgPOSTRequestFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestGETSuccess(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}

		// Verify URL
		if r.URL.Path != testPath {
			t.Errorf("Expected %s path, got %s", testPath, r.URL.Path)
		}

		// Verify query parameters
		if r.URL.Query().Get(testQueryPage) != testQueryPageValue {
			t.Errorf(errMsgExpectedPageQuery, r.URL.Query().Get(testQueryPage))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test API call
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL+"/test").
		SetQuery(testQueryPage, testQueryPageValue).
		GET()

	if err != nil {
		t.Fatalf(errMsgGETRequestFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestPUTSuccess(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method
		if r.Method != http.MethodPut {
			t.Errorf("Expected PUT method, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test API call
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + "/test").
		SetBody(map[string]string{testBodyName: testBodyValue}).
		PUT()

	if err != nil {
		t.Fatalf(errMsgPUTRequestFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestDELETESuccess(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method
		if r.Method != http.MethodDelete {
			t.Errorf("Expected DELETE method, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test API call
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + "/test").
		DELETE()

	if err != nil {
		t.Fatalf(errMsgDELETERequestFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestPOSTHTTPError(t *testing.T) {
	// Create a test server that returns an error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	// Test API call - status code validation is now caller's responsibility
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + "/test").POST()

	if err != nil {
		t.Fatalf("POST should succeed even with 500 status, got error: %v", err)
	}

	// Verify we got the error status code
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, resp.StatusCode)
	}

	// Verify we got the error body
	var errorBody map[string]string
	json.Unmarshal(body, &errorBody)
	if errorBody["error"] != "internal server error" {
		t.Errorf("Expected error body, got %v", errorBody)
	}
}

func TestPOSTInvalidURL(t *testing.T) {
	// Test with invalid URL
	api := NewAPI()
	_, _, err := api.SetURL("invalid-url").POST()

	if err == nil {
		t.Fatal("Expected error for invalid URL, got nil")
	}
}

func TestPOSTJSONMarshalError(t *testing.T) {
	// Test with body that can't be marshaled to JSON
	api := NewAPI()
	_, _, err := api.SetURL(testURL).
		SetBody(make(chan int)). // channels can't be marshaled to JSON
		POST()

	if err == nil {
		t.Fatal("Expected error for unmarshalable body, got nil")
	}
}

func TestPOSTNetworkError(t *testing.T) {
	// Test with unreachable URL
	api := NewAPI()
	_, _, err := api.SetURL("http://unreachable-url-that-does-not-exist.com/test").POST()

	if err == nil {
		t.Fatal("Expected error for unreachable URL, got nil")
	}
}

func TestPOSTWithCreatedStatus(t *testing.T) {
	// Create a test server that returns 201 Created
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status": "created"}`))
	}))
	defer server.Close()

	// Test API call
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + "/test").POST()

	if err != nil {
		t.Fatalf(errMsgPOSTRequestFailed, err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status code %d, got %d", http.StatusCreated, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusCreated {
		t.Errorf(errMsgExpectedCreated, result["status"])
	}
}

func TestPOSTWithCustomContentType(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom content type
		if r.Header.Get(headerContentType) != contentTypeXML {
			t.Errorf("Expected Content-Type application/xml, got %s", r.Header.Get(headerContentType))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test API call with custom content type
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL+"/test").
		SetHeader(headerContentType, contentTypeXML).
		SetBody(map[string]string{testBodyName: testBodyValue}).
		POST()

	if err != nil {
		t.Fatalf(errMsgPOSTRequestFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestPOSTWithoutBody(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no content type is set when no body
		if r.Header.Get(headerContentType) != "" {
			t.Errorf("Expected no Content-Type header, got %s", r.Header.Get(headerContentType))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test API call without body
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + "/test").POST()

	if err != nil {
		t.Fatalf(errMsgPOSTRequestFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestPATCHMethod(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method
		if r.Method != http.MethodPatch {
			t.Errorf("Expected PATCH method, got %s", r.Method)
		}

		// Verify body
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body[testBodyName] != testBodyValue {
			t.Errorf(errMsgExpectedBodyName, body)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test PATCH method using executeRequest directly
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL + "/test").
		SetBody(map[string]string{testBodyName: testBodyValue}).(*API).executeRequest(http.MethodPatch)

	if err != nil {
		t.Fatalf("PATCH request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithEmptyQueryParams(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with empty query params map
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL + "/test").(*API).executeRequest(http.MethodGet)

	if err != nil {
		t.Fatalf("Request with empty query params failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithNilBody(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with nil body
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL + "/test").(*API).executeRequest(http.MethodPost)

	if err != nil {
		t.Fatalf("Request with nil body failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithEmptyHeaders(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with empty headers
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL + "/test").(*API).executeRequest(http.MethodGet)

	if err != nil {
		t.Fatalf("Request with empty headers failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithQueryParams(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameters
		if r.URL.Query().Get(testQueryPage) != testQueryPageValue {
			t.Errorf(errMsgExpectedPageQuery, r.URL.Query().Get(testQueryPage))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("Expected limit=10 query param, got %s", r.URL.Query().Get("limit"))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with query params
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL+"/test").
		SetQuery(testQueryPage, testQueryPageValue).
		SetQuery(testQueryLimit, testQueryLimitValue).(*API).executeRequest(http.MethodGet)

	if err != nil {
		t.Fatalf("Request with query params failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithBodyAndCustomContentType(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom content type is not overridden
		if r.Header.Get(headerContentType) != contentTypeXML {
			t.Errorf("Expected Content-Type application/xml, got %s", r.Header.Get(headerContentType))
		}

		// Verify body
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body[testBodyName] != testBodyValue {
			t.Errorf(errMsgExpectedBodyName, body)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with body and custom content type
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL+"/test").
		SetHeader(headerContentType, contentTypeXML).
		SetBody(map[string]string{testBodyName: testBodyValue}).(*API).executeRequest(http.MethodPost)

	if err != nil {
		t.Fatalf("Request with body and custom content type failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithNilQueryParams(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with nil query params (this should not happen with NewAPI, but let's test the edge case)
	api := &API{
		url:         server.URL + "/test",
		headers:     make(map[string]string),
		body:        nil,
		queryParams: nil, // This is the key - nil instead of empty map
	}

	resp, body, err := api.executeRequest(http.MethodGet)

	if err != nil {
		t.Fatalf("Request with nil query params failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithBodyButUnsupportedMethod(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no body is sent for GET request
		if r.Body != nil {
			body := make([]byte, 100)
			n, _ := r.Body.Read(body)
			if n > 0 {
				t.Errorf("Expected no body for GET request, got %s", string(body[:n]))
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with body but GET method (which doesn't support body)
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL + "/test").
		SetBody(map[string]string{testBodyName: testBodyValue}).(*API).executeRequest(http.MethodGet)

	if err != nil {
		t.Fatalf("Request with body but unsupported method failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithBodyButDELETE(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no body is sent for DELETE request
		if r.Body != nil {
			body := make([]byte, 100)
			n, _ := r.Body.Read(body)
			if n > 0 {
				t.Errorf("Expected no body for DELETE request, got %s", string(body[:n]))
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test with body but DELETE method (which doesn't support body)
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	resp, body, err := api.SetURL(server.URL + "/test").
		SetBody(map[string]string{testBodyName: testBodyValue}).(*API).executeRequest(http.MethodDelete)

	if err != nil {
		t.Fatalf("Request with body but DELETE method failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestExecuteRequestWithEmptyURL(t *testing.T) {
	// Test with empty URL
	api := NewAPI().(*API) // Cast to concrete type to access executeRequest
	_, _, err := api.executeRequest(http.MethodGet)

	if err == nil {
		t.Fatal("Expected error for empty URL, got nil")
	}
}

func TestDefaultTimeout(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test that default timeout is applied (30 seconds)
	api := NewAPI().(*API)
	client := api.createHTTPClient()

	if client.Timeout != DefaultTimeout {
		t.Errorf("Expected default timeout of %v, got %v", DefaultTimeout, client.Timeout)
	}
}

func TestCustomTimeout(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test that custom timeout is applied
	customTimeout := 10 * time.Second
	api := NewAPI().(*API)
	apiWithTimeout := api.SetTimeout(customTimeout).(*API)
	client := apiWithTimeout.createHTTPClient()

	if client.Timeout != customTimeout {
		t.Errorf("Expected custom timeout of %v, got %v", customTimeout, client.Timeout)
	}
}

func TestInternalCallNoTimeout(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test that internal calls have no timeout
	api := NewAPI().(*API)
	apiInternal := api.SetInternal().(*API)
	client := apiInternal.createHTTPClient()

	if client.Timeout != 0 {
		t.Errorf("Expected no timeout (0) for internal calls, got %v", client.Timeout)
	}
}

func TestSetTimeoutOverridesInternal(t *testing.T) {
	// Test that SetTimeout overrides SetInternal
	api := NewAPI().(*API)
	apiWithTimeout := api.SetInternal().SetTimeout(5 * time.Second).(*API)
	client := apiWithTimeout.createHTTPClient()

	if client.Timeout != 5*time.Second {
		t.Errorf("Expected timeout of 5s after SetTimeout, got %v", client.Timeout)
	}

	// Verify isInternal is false
	if apiWithTimeout.isInternal {
		t.Error("Expected isInternal to be false after SetTimeout")
	}
}

func TestSetInternalOverridesTimeout(t *testing.T) {
	// Test that SetInternal overrides custom timeout
	api := NewAPI().(*API)
	apiInternal := api.SetTimeout(10 * time.Second).SetInternal().(*API)
	client := apiInternal.createHTTPClient()

	if client.Timeout != 0 {
		t.Errorf("Expected no timeout (0) after SetInternal, got %v", client.Timeout)
	}

	// Verify isInternal is true
	if !apiInternal.isInternal {
		t.Error("Expected isInternal to be true after SetInternal")
	}
}

func TestTimeoutWithPOSTRequest(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test that timeout works with actual request
	customTimeout := 5 * time.Second
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + testPath).
		SetTimeout(customTimeout).
		POST()

	if err != nil {
		t.Fatalf("POST request with custom timeout failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}

func TestInternalCallWithPOSTRequest(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Test that internal calls work without timeout
	api := NewAPI()
	resp, body, err := api.SetURL(server.URL + testPath).
		SetInternal().
		POST()

	if err != nil {
		t.Fatalf("POST request with internal flag failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["status"] != testStatusSuccess {
		t.Errorf(errMsgExpectedSuccess, result["status"])
	}
}
