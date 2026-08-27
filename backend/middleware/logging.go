package middleware

import (
	"fmt"
	"net/http"
	"time"
)

func Logging(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapped := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next(wrapped, r)

		duration := time.Since(start)
		fmt.Printf("[%s] %s %s → %d (%v)\n",
			time.Now().Format("15:04:05"),
			r.Method,
			r.URL.Path,
			wrapped.statusCode,
			duration,
		)
	}
}

type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}
