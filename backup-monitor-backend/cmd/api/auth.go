package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// ===== AUTH - RATE LIMITER & JWT =====

type loginAttempt struct {
	firstFailure time.Time
	failures     int
}

type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string]loginAttempt
	maxFailures int
	window      time.Duration
}

func newLoginRateLimiter(maxFailures int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		attempts:    make(map[string]loginAttempt),
		maxFailures: maxFailures,
		window:      window,
	}
}

func remoteAddressKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func (limiter *loginRateLimiter) allowed(key string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	attempt, ok := limiter.attempts[key]
	if !ok {
		return true
	}
	if now.Sub(attempt.firstFailure) >= limiter.window {
		delete(limiter.attempts, key)
		return true
	}
	return attempt.failures < limiter.maxFailures
}

func (limiter *loginRateLimiter) recordFailure(key string, now time.Time) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	attempt, ok := limiter.attempts[key]
	if !ok || now.Sub(attempt.firstFailure) >= limiter.window {
		limiter.attempts[key] = loginAttempt{firstFailure: now, failures: 1}
		return
	}
	attempt.failures++
	limiter.attempts[key] = attempt
}

func (limiter *loginRateLimiter) reset(key string) {
	limiter.mu.Lock()
	delete(limiter.attempts, key)
	limiter.mu.Unlock()
}

func generateToken(username string, secret []byte) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(8 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func authMiddleware(secret []byte, account *adminAccountStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if account.username() == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Chưa tạo quản trị viên"})
			return
		}
		authHeader := c.GetHeader("Authorization")
		if ticket := strings.TrimSpace(c.Query("ticket")); ticket != "" && c.Request.Method == http.MethodGet && c.Request.URL.Path == "/api/download-snapshot" {
			path, ok := consumeDownloadTicket(ticket)
			if ok {
				c.Set("download_ticket_path", path)
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Link tải đã hết hạn hoặc đã được sử dụng"})
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") || len(authHeader) <= len("Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Chưa đăng nhập"})
			return
		}
		tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return secret, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token không hợp lệ hoặc đã hết hạn"})
			return
		}
		subject, subjectErr := token.Claims.GetSubject()
		if subjectErr != nil || !constantTimeEqual(subject, account.username()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token không thuộc quản trị viên của bản cài này"})
			return
		}
		c.Set("actor", subject)
		c.Next()
	}
}

func constantTimeEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}
