// Command minimal demonstrates the yankit scaffold: an Echo v5 server with
// slog JSON request logging, the shared health-check handler and uniform
// {"code","error"} error rendering.
//
//	go run ./examples/minimal
//
// Then: curl -i localhost:8080/healthz localhost:8080/courses/1
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo/v5"

	"github.com/BeaversLab/yankit"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	e := yankit.New(log)
	e.GET("/healthz", yankit.Healthz(func(ctx context.Context) error {
		return nil // probe your database here
	}))
	e.GET("/courses/:id", func(c *echo.Context) error {
		return yankit.NewAPIError(http.StatusNotFound, "course_not_found", "course not found")
	})
	e.GET("/boom", func(c *echo.Context) error {
		return errors.New("something broke") // renders 500 internal_error
	})

	addr := ":8080"
	log.Info("listening", "addr", addr, "pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sc := echo.StartConfig{Address: addr}
	if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}
