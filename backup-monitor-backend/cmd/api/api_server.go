package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Drain in-flight requests on systemd stop instead of abruptly closing downloads.
// Do not set a short WriteTimeout: backup downloads may legitimately take minutes.
func serveAPI(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{
		Addr: addr, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	done := make(chan struct{})
	stopped := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			err := server.Shutdown(shutdownCtx)
			if err != nil {
				_ = server.Close()
			}
			stopped <- err
		case <-done:
			stopped <- nil
		}
	}()
	err := server.ListenAndServe()
	close(done)
	shutdownErr := <-stopped
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
