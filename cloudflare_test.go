package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	// mux is the HTTP request multiplexer used with the test server.
	mux *http.ServeMux

	// client is the API client being tested.
	client *API

	// server is a test HTTP server used to provide mock API responses.
	server *httptest.Server
)

func setup(opts ...Option) {
	// test server
	mux = http.NewServeMux()
	server = httptest.NewServer(mux)

	// disable rate limits and retries in testing - prepended so any provided value overrides this
	opts = append([]Option{UsingRateLimit(100000), UsingRetryPolicy(0, 0, 0)}, opts...)

	// Cloudflare client configured to use test server
	client, _ = New("deadbeef", "cloudflare@example.org", opts...)
	client.BaseURL = server.URL
}

func teardown() {
	server.Close()
}

func TestClient_Headers(t *testing.T) {
	// it should set default headers
	setup()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "Expected method 'GET', got %s", r.Method)
		assert.Equal(t, "cloudflare@example.org", r.Header.Get("X-Auth-Email"))
		assert.Equal(t, "deadbeef", r.Header.Get("X-Auth-Key"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
	})
	client.UserDetails(context.Background()) //nolint
	teardown()

	// it should override appropriate default headers when custom headers given
	headers := make(http.Header)
	headers.Set("Content-Type", "application/xhtml+xml")
	headers.Add("X-Random", "a random header")
	setup(Headers(headers))
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "Expected method 'GET', got %s", r.Method)
		assert.Equal(t, "cloudflare@example.org", r.Header.Get("X-Auth-Email"))
		assert.Equal(t, "deadbeef", r.Header.Get("X-Auth-Key"))
		assert.Equal(t, "application/xhtml+xml", r.Header.Get("Content-Type"))
		assert.Equal(t, "a random header", r.Header.Get("X-Random"))
	})
	client.UserDetails(context.Background()) //nolint
	teardown()

	// it should set X-Auth-User-Service-Key and omit X-Auth-Email and X-Auth-Key when client.authType is AuthUserService
	setup()
	client.SetAuthType(AuthUserService)
	client.APIUserServiceKey = "userservicekey"
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "Expected method 'GET', got %s", r.Method)
		assert.Empty(t, r.Header.Get("X-Auth-Email"))
		assert.Empty(t, r.Header.Get("X-Auth-Key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Equal(t, "userservicekey", r.Header.Get("X-Auth-User-Service-Key"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
	})
	client.UserDetails(context.Background()) //nolint
	teardown()

	// it should set X-Auth-User-Service-Key and omit X-Auth-Email and X-Auth-Key when using NewWithUserServiceKey
	setup()
	client, err := NewWithUserServiceKey("userservicekey")
	assert.NoError(t, err)
	client.BaseURL = server.URL
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "Expected method 'GET', got %s", r.Method)
		assert.Empty(t, r.Header.Get("X-Auth-Email"))
		assert.Empty(t, r.Header.Get("X-Auth-Key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Equal(t, "userservicekey", r.Header.Get("X-Auth-User-Service-Key"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
	})
	client.UserDetails(context.Background()) //nolint
	teardown()

	// it should set Authorization and omit others credential headers when using NewWithAPIToken
	setup()
	client, err = NewWithAPIToken("my-api-token")
	assert.NoError(t, err)
	client.BaseURL = server.URL
	mux.HandleFunc("/zones/123456", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "Expected method 'GET', got %s", r.Method)
		assert.Empty(t, r.Header.Get("X-Auth-Email"))
		assert.Empty(t, r.Header.Get("X-Auth-Key"))
		assert.Empty(t, r.Header.Get("X-Auth-User-Service-Key"))
		assert.Equal(t, "Bearer my-api-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
	})
	client.UserDetails(context.Background()) //nolint
	teardown()
}

type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (t RoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return t(request)
}

func TestContextTimeout(t *testing.T) {
	setup()
	defer teardown()

	handler := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}

	mux.HandleFunc("/timeout", handler)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, err := client.makeRequestContext(ctx, http.MethodHead, "/timeout", nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.WithinDuration(t, start, time.Now(), 2*time.Second,
		"makeRequestContext took too much time with an expiring context")
}

func TestCheckResultInfo(t *testing.T) {
	for _, c := range [...]struct {
		TestName   string
		PerPage    int
		Page       int
		Count      int
		ResultInfo ResultInfo
		Verdict    bool
	}{
		{"per_page do not match", 20, 1, 0, ResultInfo{Page: 1, PerPage: 30, TotalPages: 0, Count: 0, Total: 0}, false},
		{"page counts do not match", 20, 2, 20, ResultInfo{Page: 1, PerPage: 20, TotalPages: 2, Count: 20, Total: 40}, false},
		{"counts do not match", 20, 1, 20, ResultInfo{Page: 1, PerPage: 20, TotalPages: 2, Count: 19, Total: 21}, false},
		{"counts do not match", 20, 1, 19, ResultInfo{Page: 1, PerPage: 20, TotalPages: 2, Count: 20, Total: 21}, false},
		{"per_page 0", 0, 1, 0, ResultInfo{Page: 1, PerPage: 0, TotalPages: 1, Count: 0, Total: 0}, false},
		{"number of items is zero", 20, 1, 0, ResultInfo{Page: 1, PerPage: 20, TotalPages: 0, Count: 0, Total: 0}, true},
		{"number of items is 0 but number of pages is greater than 0", 20, 1, 0, ResultInfo{Page: 1, PerPage: 20, TotalPages: 1, Count: 0, Total: 0}, false},
		{"total number of items greater than 0 but number of pages is 0", 20, 1, 1, ResultInfo{Page: 1, PerPage: 20, TotalPages: 0, Count: 1, Total: 1}, false},
		{"too many total number of items (one more page is needed)", 20, 1, 20, ResultInfo{Page: 1, PerPage: 20, TotalPages: 1, Count: 20, Total: 21}, false},
		{"too few total number of items (the second page would be empty)", 20, 1, 20, ResultInfo{Page: 1, PerPage: 20, TotalPages: 2, Count: 20, Total: 20}, false},
		{"page number cannot be zero", 20, 0, 20, ResultInfo{Page: 0, PerPage: 20, TotalPages: 1, Count: 20, Total: 20}, false},
		{"page number cannot go beyond number of pages", 20, 2, 20, ResultInfo{Page: 2, PerPage: 20, TotalPages: 1, Count: 20, Total: 20}, false},
		{"the last page is full of results", 20, 1, 20, ResultInfo{Page: 1, PerPage: 20, TotalPages: 1, Count: 20, Total: 20}, true},
		{"we are not on the last page so it should be full of results", 20, 1, 19, ResultInfo{Page: 1, PerPage: 20, TotalPages: 2, Count: 19, Total: 39}, false},
		{"last page only has 19 items not 20", 20, 2, 20, ResultInfo{Page: 2, PerPage: 20, TotalPages: 2, Count: 20, Total: 39}, false},
		{"fully working result info", 20, 2, 19, ResultInfo{Page: 2, PerPage: 20, TotalPages: 2, Count: 19, Total: 39}, true},
	} {
		t.Run(c.TestName, func(t *testing.T) {
			assert.Equal(t, c.Verdict, checkResultInfo(c.PerPage, c.Page, c.Count, &c.ResultInfo))
		})
	}
}
