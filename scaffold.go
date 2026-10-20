package yankit

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
)

// New returns an Echo instance wired with yankit's error handler and slog
// request logging: the startup scaffold shared by the Yan services.
func New(log *slog.Logger) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = ErrorHandler(log)
	e.Use(RequestLog(log))
	return e
}

// RequestLog writes one structured log line per request.
func RequestLog(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			err := next(c)
			_, status := echo.ResolveResponseStatus(c.Response(), err)
			log.Info("http request",
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", status,
			)
			return err
		}
	}
}

// Healthz reports liveness, optionally probing a dependency (typically the
// database: pass its Ping). A nil ping skips the probe. A failing probe
// renders 503 database_unavailable through the shared error handler.
func Healthz(ping func(ctx context.Context) error) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if ping != nil {
			if err := ping(c.Request().Context()); err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "database unavailable")
			}
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}
