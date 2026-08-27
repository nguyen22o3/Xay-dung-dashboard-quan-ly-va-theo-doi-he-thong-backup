package middleware

import (
	"net/http"
	"os"
)

func APIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := os.Getenv("API_KEY")
		if apiKey == "" {
			http.Error(w, "Server chưa cấu hình API Key", http.StatusInternalServerError)
			return
		}

		providedKey := r.Header.Get("X-API-Key")
		if providedKey == "" {
			http.Error(w, "Thiếu header X-API-Key", http.StatusUnauthorized)
			return
		}

		if providedKey != apiKey {
			http.Error(w, "API Key không hợp lệ", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
