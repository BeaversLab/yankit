// Package yankit is the shared HTTP toolkit for the Yan services (YanAuth,
// YanLearn): uniform {"code","error"} JSON error rendering and an Echo v5
// startup scaffold (slog request logging, health-check helper).
//
// It must stay tiny: only code both services genuinely share (ADR 0001).
package yankit

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
)

// Stable machine-readable error codes (snake_case, add-only contract: never
// rename a published code). Rendered alongside the human-readable message:
//
//	{"code": "not_found", "error": "Not Found"}
//
// SECURITY: codes must not leak information. Where failure reasons must stay
// indistinguishable to callers, share one code and keep the precise reason in
// the audit log (e.g. "invalid_credentials").
//
// These constants cover what the framework itself renders; each service adds
// its own domain codes under its namespace.
const (
	CodeInvalidRequest      = "invalid_request"
	CodeUnauthorized        = "unauthorized"
	CodeForbidden           = "forbidden"
	CodeNotFound            = "not_found"
	CodeMethodNotAllowed    = "method_not_allowed"
	CodeConflict            = "conflict"
	CodeRateLimited         = "rate_limited"
	CodeDatabaseUnavailable = "database_unavailable"
	CodeInternalError       = "internal_error"
)

// DefaultCodeForStatus gives every response a code even where no specific
// business code applies.
func DefaultCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusServiceUnavailable:
		return CodeDatabaseUnavailable
	default:
		return "error"
	}
}

// APIError carries a stable snake_case code plus an HTTP status and a
// human-readable message. Handlers return it; ErrorHandler renders it.
type APIError struct {
	status int
	code   string
	msg    string
}

// NewAPIError builds an *APIError.
func NewAPIError(status int, code, msg string) *APIError {
	return &APIError{status: status, code: code, msg: msg}
}

func (e *APIError) Error() string { return e.msg }

// StatusCode implements echo.HTTPStatusCoder so middleware using
// echo.ResolveResponseStatus (e.g. RequestLog) sees the real status.
func (e *APIError) StatusCode() int { return e.status }

// RenderError writes the uniform JSON error body. An empty message falls back
// to the HTTP status text; HEAD requests get no body.
func RenderError(c *echo.Context, status int, code, msg string) error {
	if msg == "" {
		msg = http.StatusText(status)
	}
	if c.Request().Method == http.MethodHead {
		return c.NoContent(status)
	}
	return c.JSON(status, map[string]string{"code": code, "error": msg})
}

// ErrorHandler returns uniform JSON error responses: every error body is
// {"code": <stable snake_case code>, "error": <human message>}. Unknown
// errors collapse to 500 internal_error — details go to the log, never the
// client.
func ErrorHandler(log *slog.Logger) echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
			return
		}
		if ae, ok := err.(*APIError); ok {
			if ae.status >= 500 {
				log.Error("http error", "err", err, "path", c.Path())
			}
			if werr := RenderError(c, ae.status, ae.code, ae.msg); werr != nil {
				log.Error("write error response", "err", werr)
			}
			return
		}
		var he *echo.HTTPError
		if errors.As(err, &he) {
			if he.Code >= 500 {
				log.Error("http error", "err", err, "path", c.Path())
			}
			if werr := RenderError(c, he.Code, DefaultCodeForStatus(he.Code), he.Message); werr != nil {
				log.Error("write error response", "err", werr)
			}
			return
		}
		// Echo v5's router-level errors (404, 405, ...) are unexported
		// sentinels implementing echo.HTTPStatusCoder rather than
		// *echo.HTTPError. Honor any such status-carrying error so it
		// renders with its real status instead of collapsing to 500.
		if status := echo.StatusCode(err); status != 0 {
			if status >= 500 {
				log.Error("http error", "err", err, "path", c.Path())
			}
			if werr := RenderError(c, status, DefaultCodeForStatus(status), http.StatusText(status)); werr != nil {
				log.Error("write error response", "err", werr)
			}
			return
		}
		log.Error("http error", "err", err, "path", c.Path())
		if werr := RenderError(c, http.StatusInternalServerError, CodeInternalError, "internal error"); werr != nil {
			log.Error("write error response", "err", werr)
		}
	}
}
