package yankit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/BeaversLab/yankit"
)

// newEcho builds an Echo instance wired with yankit's error handler, mirroring
// the production scaffold.
func newEcho() (*echo.Echo, *bytes.Buffer) {
	var buf bytes.Buffer
	e := echo.New()
	e.HTTPErrorHandler = yankit.ErrorHandler(slog.New(slog.NewJSONHandler(&buf, nil)))
	return e, &buf
}

// do runs one request against e and returns status, parsed body and raw body.
func do(t *testing.T, e *echo.Echo, method, path string) (int, map[string]string, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var body map[string]string
	raw := rec.Body.String()
	if raw != "" {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body not JSON object: %v, body=%s", err, raw)
		}
	}
	return rec.Code, body, raw
}

// TestDefaultCodeForStatus verifies the status→code table through full HTTP
// round-trips: every echo.HTTPError gets a stable default snake_case code.
func TestDefaultCodeForStatus(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, "invalid_request"},
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not_found"},
		{http.StatusConflict, "conflict"},
		{http.StatusTooManyRequests, "rate_limited"},
		{http.StatusServiceUnavailable, "database_unavailable"},
		{http.StatusTeapot, "error"}, // no specific code: stable fallback
	}
	for _, tc := range cases {
		e, _ := newEcho()
		e.GET("/x", func(c *echo.Context) error {
			return echo.NewHTTPError(tc.status, "")
		})
		status, body, raw := do(t, e, http.MethodGet, "/x")
		if status != tc.status {
			t.Fatalf("status = %d, want %d (body=%s)", status, tc.status, raw)
		}
		if body["code"] != tc.code {
			t.Fatalf("status %d: code = %q, want %q", tc.status, body["code"], tc.code)
		}
		if body["error"] == "" {
			t.Fatalf("status %d: empty error message", tc.status)
		}
	}
}

// TestCustomCode verifies an *yankit.APIError renders its own status, code
// and message.
func TestCustomCode(t *testing.T) {
	e, _ := newEcho()
	e.GET("/x", func(c *echo.Context) error {
		return yankit.NewAPIError(http.StatusConflict, "course_not_distributed", "course is not distributed to your department")
	})
	status, body, _ := do(t, e, http.MethodGet, "/x")
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if body["code"] != "course_not_distributed" {
		t.Fatalf("code = %q", body["code"])
	}
	if body["error"] != "course is not distributed to your department" {
		t.Fatalf("error = %q", body["error"])
	}
}

// TestCustomCodeEmptyMessage verifies the message falls back to the HTTP
// status text.
func TestCustomCodeEmptyMessage(t *testing.T) {
	e, _ := newEcho()
	e.GET("/x", func(c *echo.Context) error {
		return yankit.NewAPIError(http.StatusNotFound, "not_found", "")
	})
	_, body, _ := do(t, e, http.MethodGet, "/x")
	if body["error"] != "Not Found" {
		t.Fatalf("error = %q, want %q", body["error"], "Not Found")
	}
}

// TestHeadRequestNoBody verifies HEAD requests carry the status but no body,
// for both APIError and echo.HTTPError paths.
func TestHeadRequestNoBody(t *testing.T) {
	cases := []struct {
		name string
		fail func(c *echo.Context) error
		code int
	}{
		{
			name: "api error",
			fail: func(c *echo.Context) error {
				return yankit.NewAPIError(http.StatusNotFound, "not_found", "gone")
			},
			code: http.StatusNotFound,
		},
		{
			name: "http error",
			fail: func(c *echo.Context) error {
				return echo.NewHTTPError(http.StatusBadRequest, "bad input")
			},
			code: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newEcho()
			e.GET("/x", tc.fail)
			e.HEAD("/x", tc.fail)
			status, _, raw := do(t, e, http.MethodHead, "/x")
			if status != tc.code {
				t.Fatalf("status = %d, want %d", status, tc.code)
			}
			if raw != "" {
				t.Fatalf("HEAD body = %q, want empty", raw)
			}
		})
	}
}

// TestRouterErrors verifies Echo v5's router-level errors (404 not found,
// 405 method not allowed are unexported *echo.httpError sentinels, not
// *echo.HTTPError) render with their real status and a default code instead
// of collapsing to 500.
func TestRouterErrors(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{"no such route", http.MethodGet, "/nope", http.StatusNotFound, "not_found"},
		{"method not allowed", http.MethodHead, "/x", http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newEcho()
			e.GET("/x", func(c *echo.Context) error { return nil })
			status, body, raw := do(t, e, tc.method, tc.path)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (body=%s)", status, tc.status, raw)
			}
			if tc.method == http.MethodHead {
				if raw != "" {
					t.Fatalf("HEAD body = %q, want empty", raw)
				}
				return
			}
			if body["code"] != tc.code {
				t.Fatalf("code = %q, want %q", body["code"], tc.code)
			}
		})
	}
}

// TestUnknownError verifies plain errors collapse to 500 internal_error and
// never leak error details to the client.
func TestUnknownError(t *testing.T) {
	e, logBuf := newEcho()
	e.GET("/x", func(c *echo.Context) error {
		return errors.New("secret db password is hunter2")
	})
	status, body, raw := do(t, e, http.MethodGet, "/x")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if body["code"] != "internal_error" {
		t.Fatalf("code = %q, want internal_error", body["code"])
	}
	if body["error"] != "Internal Server Error" {
		t.Fatalf("error = %q, want %q", body["error"], "Internal Server Error")
	}
	if bytes.Contains([]byte(raw), []byte("hunter2")) {
		t.Fatal("internal error details leaked into response body")
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("hunter2")) {
		t.Fatal("internal error details missing from server log")
	}
}

// TestHTTPError5xxMessageScrubbed verifies that a 5xx echo.HTTPError's
// handler-supplied message never reaches the client — only the log.
func TestHTTPError5xxMessageScrubbed(t *testing.T) {
	e, logBuf := newEcho()
	e.GET("/x", func(c *echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, "secret: db password is hunter2")
	})
	status, body, raw := do(t, e, http.MethodGet, "/x")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if bytes.Contains([]byte(raw), []byte("hunter2")) {
		t.Fatalf("5xx message leaked into body: %s", raw)
	}
	if body["error"] != "Internal Server Error" {
		t.Fatalf("error = %q, want status text", body["error"])
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("hunter2")) {
		t.Fatal("5xx detail missing from server log")
	}
}
