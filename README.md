# yankit

Shared HTTP toolkit for the Yan services (YanAuth, YanLearn). Echo v5, Go 1.25+,
no dependencies beyond Echo and the standard library.

- **Error rendering** — uniform `{"code","error"}` JSON bodies with stable
  snake_case add-only codes; HTTP status carries the semantics. Unknown errors
  collapse to `500 internal_error` and never leak details to the client.
- **Startup scaffold** — `New(logger)` returns an Echo instance wired with the
  shared error handler and one structured slog line per request;
  `Healthz(ping)` mounts a liveness probe.

```go
log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
e := yankit.New(log)
e.GET("/healthz", yankit.Healthz(dbPing))
e.GET("/courses/:id", func(c *echo.Context) error {
    return yankit.NewAPIError(http.StatusNotFound, "course_not_found", "course not found")
})
```

Minimal runnable example: `go run ./examples/minimal`.

Error-code contract (add-only, never rename a published code; see YanLearn
ADR 0002): codes must not leak information — share one code across failure
reasons that must stay indistinguishable, and keep the precise reason in the
audit log.

yankit must stay tiny: only code both services genuinely share (ADR 0001).
