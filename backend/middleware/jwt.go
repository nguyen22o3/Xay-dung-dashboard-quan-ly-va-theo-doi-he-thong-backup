package middleware

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTSecret returns the signing secret from the environment.
// Empty khi không cấu hình. Không dùng secret mặc định để tránh giả mạo token.
func JWTSecret() []byte {
	return []byte(os.Getenv("JWT_SECRET"))
}

// GenerateToken creates a signed JWT for the admin user.
// Trả lỗi nếu chưa cấu hình JWT_SECRET.
func GenerateToken() (string, error) {
	secret := JWTSecret()
	if len(secret) == 0 {
		return "", jwt.ErrInvalidKey
	}
	claims := jwt.MapClaims{
		"sub":  "admin",
		"role": "admin",
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// JWT verifies the Authorization: Bearer <token> header.
func JWT(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret := JWTSecret()
		if len(secret) == 0 {
			http.Error(w, "Server chưa cấu hình JWT_SECRET", http.StatusInternalServerError)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Thiếu hoặc sai định dạng token", http.StatusUnauthorized)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return secret, nil
		})
		if err != nil || !token.Valid {
			http.Error(w, "Token không hợp lệ hoặc đã hết hạn", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
