package yankit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/BeaversLab/yankit"
)

// TestRequestLog verifies the scaffold emits one parseable JSON log line per
// request with method, path and status.
func TestRequestLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	e := yankit.New(log)
	e.GET("/ping", func(c *echo.Context) error { return nil })

	status, _, _ := do(t, e, http.MethodGet, "/ping")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no log output captured")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("log line not valid JSON: %v, line=%s", err, lines[len(lines)-1])
	}
	if entry["msg"] != "http request" {
		t.Fatalf("msg = %v, want %q", entry["msg"], "http request")
	}
	if entry["method"] != "GET" || entry["path"] != "/ping" || entry["status"] != float64(200) {
		t.Fatalf("log entry = %v", entry)
	}
}

// TestRequestLogAPIErrorStatus verifies the request log records the APIError's
// real status, not a fallback 500.
func TestRequestLogAPIErrorStatus(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	e := yankit.New(log)
	e.GET("/gone", func(c *echo.Context) error {
		return yankit.NewAPIError(http.StatusNotFound, "not_found", "gone")
	})

	do(t, e, http.MethodGet, "/gone")

	var entry map[string]any
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("log line not valid JSON: %v", err)
	}
	if entry["status"] != float64(http.StatusNotFound) {
		t.Fatalf("logged status = %v, want 404", entry["status"])
	}
}

// TestHealthz verifies liveness with and without a dependency probe.
func TestHealthz(t *testing.T) {
	t.Run("ok without ping", func(t *testing.T) {
		e, _ := newEcho()
		e.GET("/healthz", yankit.Healthz(nil))
		status, body, _ := do(t, e, http.MethodGet, "/healthz")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if body["status"] != "ok" {
			t.Fatalf("status field = %q", body["status"])
		}
	})
	t.Run("ok with healthy ping", func(t *testing.T) {
		e, _ := newEcho()
		e.GET("/healthz", yankit.Healthz(func(ctx context.Context) error { return nil }))
		status, body, _ := do(t, e, http.MethodGet, "/healthz")
		if status != http.StatusOK || body["status"] != "ok" {
			t.Fatalf("status = %d, body = %v", status, body)
		}
	})
	t.Run("503 when ping fails", func(t *testing.T) {
		e, _ := newEcho()
		e.GET("/healthz", yankit.Healthz(func(ctx context.Context) error { return errors.New("db down") }))
		status, body, _ := do(t, e, http.MethodGet, "/healthz")
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", status)
		}
		if body["code"] != "database_unavailable" {
			t.Fatalf("code = %q, want database_unavailable", body["code"])
		}
	})
}
