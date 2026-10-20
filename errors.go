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

	// 鉴权链错误码，与 YanAuth 现行契约一致（add-only）。
	CodeInvalidToken      = "invalid_token"
	CodeScopeNotAllowed   = "scope_not_allowed"
	CodeMissingPermission = "missing_permission"
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
// {"code": <stable snake_case code>, "error": <human message>}. Details of
// any 5xx go to the log, never the client; unknown errors collapse to 500
// internal_error.
func ErrorHandler(log *slog.Logger) echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
			return
		}
		if ae, ok := err.(*APIError); ok {
			renderStatus(log, c, err, ae.status, ae.code, ae.msg)
			return
		}
		var he *echo.HTTPError
		if errors.As(err, &he) {
			renderStatus(log, c, err, he.Code, DefaultCodeForStatus(he.Code), he.Message)
			return
		}
		// Echo v5's router-level errors (404, 405, ...) are unexported
		// sentinels implementing echo.HTTPStatusCoder rather than
		// *echo.HTTPError. Honor any such status-carrying error so it
		// renders with its real status instead of collapsing to 500.
		if status := echo.StatusCode(err); status != 0 {
			renderStatus(log, c, err, status, DefaultCodeForStatus(status), http.StatusText(status))
			return
		}
		renderStatus(log, c, err, http.StatusInternalServerError, CodeInternalError, http.StatusText(http.StatusInternalServerError))
	}
}

// renderStatus logs server-side failures and writes the uniform error body.
// 5xx messages never reach the client: the real detail goes to the log and
// the body carries only the HTTP status text.
func renderStatus(log *slog.Logger, c *echo.Context, err error, status int, code, msg string) {
	if status >= 500 {
		log.Error("http error", "err", err, "path", c.Path())
		msg = http.StatusText(status)
	}
	if werr := RenderError(c, status, code, msg); werr != nil {
		log.Error("write error response", "err", werr)
	}
}
