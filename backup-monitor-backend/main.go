package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ===== CONFIGURATION =====

type SSHSettings struct {
	Host                 string
	User                 string
	Password             string
	PrivateKeyPath       string
	PrivateKeyPassphrase string
	KnownHostsPath       string
	InsecureHostKey      bool
}

type AppConfig struct {
	AdminUser        string
	AdminPassword    string
	JWTSecret        []byte
	APIBindAddr      string
	CORSAllowOrigins []string
	SSH              SSHSettings
}

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

type downloadTicket struct {
	path      string
	expiresAt time.Time
}

type snapshotFile struct {
	Path     string `json:"path"`
	Date     string `json:"date"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

type localSnapshot struct {
	Date     string `json:"date"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

var backupDateInName = regexp.MustCompile(`(?:^|[^0-9])(20[0-9]{2})-?([0-9]{2})-?([0-9]{2})(?:[_-]([0-9]{2})([0-9]{2})([0-9]{2}))?`)

func backupTimestamp(name, modified string) string {
	match := backupDateInName.FindStringSubmatch(name)
	if match == nil {
		return modified
	}
	day := match[1] + "-" + match[2] + "-" + match[3]
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return modified
	}
	if match[4] != "" {
		stamp := day + " " + match[4] + ":" + match[5] + ":" + match[6]
		if _, err := time.Parse("2006-01-02 15:04:05", stamp); err == nil {
			return stamp
		}
	}
	return day + " 00:00:00"
}

func parseLocalSnapshots(output string) []localSnapshot {
	snapshots := make([]localSnapshot, 0)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		prefix := backupSystemSettings().BackupRoot + "/"
		if !strings.HasPrefix(parts[2], prefix) {
			continue
		}
		path := strings.TrimPrefix(parts[2], prefix)
		segments := strings.Split(path, "/")
		if len(segments) < 2 {
			continue
		}
		category := segments[0]
		if len(segments) >= 3 {
			if _, err := time.Parse("2006-01-02", segments[0]); err == nil {
				category = segments[1]
			}
		}
		if category != "site" && category != "database" && category != "panel" {
			continue
		}
		name := segments[len(segments)-1]
		modified := parts[0]
		if len(modified) > 19 {
			modified = modified[:19]
		}
		snapshots = append(snapshots, localSnapshot{
			Date: backupTimestamp(name, modified), Category: category,
			Name: name, Size: size, Modified: modified,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Date != snapshots[j].Date {
			return snapshots[i].Date > snapshots[j].Date
		}
		return snapshots[i].Modified > snapshots[j].Modified
	})
	if len(snapshots) > 500 {
		return snapshots[:500]
	}
	return snapshots
}

func parseSnapshotList(output string) []snapshotFile {
	snapshots := []snapshotFile{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ";", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "-1" {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		path := strings.TrimSuffix(parts[0], "/")
		segments := strings.Split(path, "/")
		category := "root"
		if len(segments) >= 2 {
			category = segments[1]
		}
		snapshots = append(snapshots, snapshotFile{
			Path: path, Date: segments[0], Category: category,
			Name: segments[len(segments)-1], Size: size, Modified: parts[2],
		})
	}
	return snapshots
}

var downloadTickets = struct {
	sync.Mutex
	items map[string]downloadTicket
}{items: make(map[string]downloadTicket)}

func issueDownloadTicket(path string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	downloadTickets.Lock()
	downloadTickets.items[ticket] = downloadTicket{path: path, expiresAt: time.Now().Add(2 * time.Minute)}
	downloadTickets.Unlock()
	return ticket, nil
}

func consumeDownloadTicket(ticket string) (string, bool) {
	downloadTickets.Lock()
	defer downloadTickets.Unlock()
	entry, ok := downloadTickets.items[ticket]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(downloadTickets.items, ticket)
		return "", false
	}
	delete(downloadTickets.items, ticket)
	return entry.path, true
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

var runtimeConfig AppConfig

func requiredEnv(key string) (string, error) {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func explicitBoolEnv(key string) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return false, nil
	}
	if value == "true" {
		return true, nil
	}
	if value != "false" {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return false, nil
}

func parseCORSOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS is required")
	}

	seen := make(map[string]struct{})
	origins := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSuffix(strings.TrimSpace(item), "/")
		if origin == "" || origin == "*" {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS must contain explicit http(s) origins, not wildcards")
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("invalid CORS origin %q", item)
		}
		if _, ok := seen[origin]; !ok {
			seen[origin] = struct{}{}
			origins = append(origins, origin)
		}
	}
	return origins, nil
}

func loadConfigFromEnv() (AppConfig, error) {
	adminUser, err := requiredEnv("ADMIN_USER")
	if err != nil {
		return AppConfig{}, err
	}
	adminPassword, err := requiredEnv("ADMIN_PASSWORD")
	if err != nil {
		return AppConfig{}, err
	}
	if len(adminPassword) < 12 {
		return AppConfig{}, fmt.Errorf("ADMIN_PASSWORD must be at least 12 characters")
	}
	jwtSecret, err := requiredEnv("JWT_SECRET")
	if err != nil {
		return AppConfig{}, err
	}
	if len(jwtSecret) < 32 {
		return AppConfig{}, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}

	sshHost, err := requiredEnv("SSH_HOST")
	if err != nil {
		return AppConfig{}, err
	}
	if _, _, err := net.SplitHostPort(sshHost); err != nil {
		return AppConfig{}, fmt.Errorf("SSH_HOST must use host:port format: %w", err)
	}
	sshUser, err := requiredEnv("SSH_USER")
	if err != nil {
		return AppConfig{}, err
	}
	allowRoot, err := explicitBoolEnv("ALLOW_ROOT_SSH")
	if err != nil {
		return AppConfig{}, err
	}
	if sshUser == "root" && !allowRoot {
		return AppConfig{}, fmt.Errorf("SSH_USER=root requires explicit ALLOW_ROOT_SSH=true")
	}

	insecureHostKey, err := explicitBoolEnv("SSH_INSECURE_IGNORE_HOST_KEY")
	if err != nil {
		return AppConfig{}, err
	}
	knownHostsPath := strings.TrimSpace(os.Getenv("SSH_KNOWN_HOSTS"))
	if !insecureHostKey && knownHostsPath == "" {
		return AppConfig{}, fmt.Errorf("SSH_KNOWN_HOSTS is required unless SSH_INSECURE_IGNORE_HOST_KEY=true")
	}

	sshPassword := os.Getenv("SSH_PASSWORD")
	privateKeyPath := strings.TrimSpace(os.Getenv("SSH_PRIVATE_KEY_PATH"))
	if sshPassword == "" && privateKeyPath == "" {
		return AppConfig{}, fmt.Errorf("set SSH_PASSWORD or SSH_PRIVATE_KEY_PATH")
	}

	bindAddr := strings.TrimSpace(os.Getenv("API_BIND_ADDR"))
	if bindAddr == "" {
		bindAddr = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(bindAddr); err != nil {
		return AppConfig{}, fmt.Errorf("API_BIND_ADDR must use host:port format: %w", err)
	}

	corsOrigins, err := parseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if err != nil {
		return AppConfig{}, err
	}

	cfg := AppConfig{
		AdminUser:        adminUser,
		AdminPassword:    adminPassword,
		JWTSecret:        []byte(jwtSecret),
		APIBindAddr:      bindAddr,
		CORSAllowOrigins: corsOrigins,
		SSH: SSHSettings{
			Host:                 sshHost,
			User:                 sshUser,
			Password:             sshPassword,
			PrivateKeyPath:       privateKeyPath,
			PrivateKeyPassphrase: os.Getenv("SSH_PRIVATE_KEY_PASSPHRASE"),
			KnownHostsPath:       knownHostsPath,
			InsecureHostKey:      insecureHostKey,
		},
	}
	if _, err := buildSSHClientConfig(cfg.SSH); err != nil {
		return AppConfig{}, fmt.Errorf("invalid SSH configuration: %w", err)
	}
	return cfg, nil
}

// ========================
// AUTH - JWT
// ========================
func generateToken(username string, secret []byte) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(8 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func authMiddleware(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		c.Next()
	}
}

func constantTimeEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

// ===== CACHE NẰM TRONG RAM CHO CÁC API ĐỌC (Giảm tải SSH & Chống nghẽn) =====
type cacheEntry struct {
	value     []byte
	expiresAt time.Time
}

type flightCall struct {
	done chan struct{}
	val  []byte
	err  error
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]cacheEntry{}
	flightMu sync.Mutex
	inFlight = map[string]*flightCall{}
)

func cachedGet(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	return cachedGetWithPolicy(key, ttl, producer, nil, nil)
}

func cachedGetWithPolicy(key string, ttl time.Duration, producer func() ([]byte, error), ttlForValue func([]byte) time.Duration, staleValue func([]byte) []byte) ([]byte, error) {
	showStale := func(value []byte) []byte {
		if staleValue != nil {
			if marked := staleValue(value); len(marked) > 0 {
				return marked
			}
		}
		return value
	}
	cacheMu.Lock()
	entry, hasEntry := cache[key]
	isFresh := hasEntry && time.Now().Before(entry.expiresAt)
	cacheMu.Unlock()

	if isFresh {
		return entry.value, nil
	}

	flightMu.Lock()
	call, isRunning := inFlight[key]
	if isRunning {
		flightMu.Unlock()
		if hasEntry && len(entry.value) > 0 {
			return showStale(entry.value), nil
		}
		<-call.done
		return call.val, call.err
	}

	call = &flightCall{done: make(chan struct{})}
	inFlight[key] = call
	flightMu.Unlock()

	doWork := func() ([]byte, error) {
		b, err := producer()
		if err == nil {
			valueTTL := ttl
			if ttlForValue != nil {
				valueTTL = ttlForValue(b)
			}
			cacheMu.Lock()
			cache[key] = cacheEntry{value: b, expiresAt: time.Now().Add(valueTTL)}
			cacheMu.Unlock()
		}

		call.val = b
		call.err = err

		flightMu.Lock()
		delete(inFlight, key)
		close(call.done)
		flightMu.Unlock()

		return b, err
	}

	// Nếu đã có cache cũ, trả về luôn và làm mới ngầm (Stale-While-Revalidate)
	if hasEntry && len(entry.value) > 0 {
		go doWork()
		return showStale(entry.value), nil
	}

	b, err := doWork()
	if err != nil && hasEntry && len(entry.value) > 0 {
		return showStale(entry.value), nil
	}
	return b, err
}

func invalidate(keys ...string) {
	cacheMu.Lock()
	for _, k := range keys {
		delete(cache, k)
	}
	cacheMu.Unlock()
}

// ===== SSH CONNECTION POOL =====
// Tái sử dụng kết nối SSH thay vì tạo mới mỗi lần gọi (tiết kiệm ~300ms/request)
var (
	sshClient   *ssh.Client
	sshClientMu sync.Mutex
)

func buildSSHClientConfig(settings SSHSettings) (*ssh.ClientConfig, error) {
	authMethods := make([]ssh.AuthMethod, 0, 2)
	if settings.PrivateKeyPath != "" {
		keyData, err := os.ReadFile(settings.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH private key: %w", err)
		}
		var signer ssh.Signer
		if settings.PrivateKeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(settings.PrivateKeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyData)
		}
		if err != nil {
			return nil, fmt.Errorf("parse SSH private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if settings.Password != "" {
		authMethods = append(authMethods, ssh.Password(settings.Password))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication method configured")
	}

	var hostKeyCallback ssh.HostKeyCallback
	if settings.InsecureHostKey {
		hostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // explicit opt-in for temporary development only
	} else {
		if settings.KnownHostsPath == "" {
			return nil, fmt.Errorf("known_hosts path is required")
		}
		callback, err := knownhosts.New(settings.KnownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts: %w", err)
		}
		hostKeyCallback = callback
	}

	return &ssh.ClientConfig{
		User:            settings.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}, nil
}

func dialSSH() (*ssh.Client, error) {
	config, err := buildSSHClientConfig(runtimeConfig.SSH)
	if err != nil {
		return nil, err
	}
	client, err := ssh.Dial("tcp", runtimeConfig.SSH.Host, config)
	if err != nil {
		return nil, err
	}

	// Gửi keepalive mỗi 30 giây để server không đóng kết nối idle
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				// Kết nối đã chết, xóa khỏi pool
				sshClientMu.Lock()
				if sshClient == client {
					sshClient = nil
				}
				sshClientMu.Unlock()
				client.Close()
				return
			}
		}
	}()

	return client, nil
}

func getSSHClient() (*ssh.Client, error) {
	sshClientMu.Lock()
	defer sshClientMu.Unlock()

	// Kiểm tra kết nối hiện tại còn sống không
	if sshClient != nil {
		sess, err := sshClient.NewSession()
		if err == nil {
			sess.Close()
			return sshClient, nil
		}
		// Kết nối đã chết, đóng và tạo mới
		sshClient.Close()
		sshClient = nil
	}

	client, err := dialSSH()
	if err != nil {
		return nil, err
	}
	sshClient = client
	return sshClient, nil
}

func newSSHSession() (*ssh.Session, error) {
	client, err := getSSHClient()
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err == nil {
		return session, nil
	}

	// The pooled connection may have closed between the health check and NewSession.
	sshClientMu.Lock()
	if sshClient == client {
		sshClient = nil
	}
	sshClientMu.Unlock()
	client, err = getSSHClient()
	if err != nil {
		return nil, err
	}
	return client.NewSession()
}

// Hàm thực thi lệnh SSH trên máy ảo (tái sử dụng kết nối pool, timeout 60s)
func executeSSHCommand(command string) (string, error) {
	return executeSSHCommandRaw(configuredBackupCommand(command))
}

func executeSSHCommandRaw(command string) (string, error) {
	session, err := newSSHSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	type runResult struct {
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		done <- runResult{err: session.Run(command)}
	}()

	select {
	case <-time.After(60 * time.Second):
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("SSH command timed out after 60s")
	case res := <-done:
		if res.err != nil {
			return "", fmt.Errorf("Lệnh lỗi: %v, Chi tiết: %s", res.err, stderrBuf.String())
		}
		return stdoutBuf.String(), nil
	}
}

// ===== HELPERS =====
type flushWriter struct {
	w io.Writer
	f func()
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil && fw.f != nil {
		fw.f()
	}
	return n, err
}

var snapshotPathRe = regexp.MustCompile(`^[A-Za-z0-9._\-\/]+$`)

func validSnapshotPath(p string) bool {
	return p != "" && !strings.Contains(p, "..") && snapshotPathRe.MatchString(p)
}

// Alert settings are stored locally. Secret values are accepted on writes but are
// never serialized in API responses.
type AlertSettings struct {
	TelegramToken   string `json:"telegramToken"`
	TelegramChat    string `json:"telegramChat"`
	DiscordWebhook  string `json:"discordWebhook"`
	SmtpEmail       string `json:"smtpEmail"`
	SmtpPassword    string `json:"smtpPassword"`
	TargetEmail     string `json:"targetEmail"`
	Threshold       int    `json:"threshold"`
	TelegramEnabled *bool  `json:"telegramEnabled,omitempty"`
	DiscordEnabled  *bool  `json:"discordEnabled,omitempty"`
	SmtpEnabled     *bool  `json:"smtpEnabled,omitempty"`
}

type AlertSettingsResponse struct {
	TelegramChat       string `json:"telegramChat"`
	SmtpEmail          string `json:"smtpEmail"`
	TargetEmail        string `json:"targetEmail"`
	Threshold          int    `json:"threshold"`
	TelegramConfigured bool   `json:"telegramConfigured"`
	DiscordConfigured  bool   `json:"discordConfigured"`
	SmtpConfigured     bool   `json:"smtpConfigured"`
	TelegramEnabled    bool   `json:"telegramEnabled"`
	DiscordEnabled     bool   `json:"discordEnabled"`
	SmtpEnabled        bool   `json:"smtpEnabled"`
}

func alertChannelEnabled(value *bool, configured bool) bool {
	if value == nil {
		return configured
	}
	return *value && configured
}

var (
	telegramTokenRe = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)
	telegramChatRe  = regexp.MustCompile(`^(?:-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)
)

func safeAlertSettings(settings AlertSettings) AlertSettingsResponse {
	return AlertSettingsResponse{
		TelegramChat:       settings.TelegramChat,
		SmtpEmail:          settings.SmtpEmail,
		TargetEmail:        settings.TargetEmail,
		Threshold:          settings.Threshold,
		TelegramConfigured: settings.TelegramToken != "",
		DiscordConfigured:  settings.DiscordWebhook != "",
		SmtpConfigured:     settings.SmtpPassword != "",
		TelegramEnabled:    alertChannelEnabled(settings.TelegramEnabled, settings.TelegramToken != ""),
		DiscordEnabled:     alertChannelEnabled(settings.DiscordEnabled, settings.DiscordWebhook != ""),
		SmtpEnabled:        alertChannelEnabled(settings.SmtpEnabled, settings.SmtpPassword != ""),
	}
}

func validateEmailAddress(value string) error {
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func validateDiscordWebhook(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid Discord webhook URL")
	}
	host := strings.ToLower(u.Hostname())
	if host != "discord.com" && host != "discordapp.com" {
		return fmt.Errorf("Discord webhook must use discord.com")
	}
	if !strings.HasPrefix(u.EscapedPath(), "/api/webhooks/") {
		return fmt.Errorf("invalid Discord webhook path")
	}
	return nil
}

func validateAlertSettings(settings AlertSettings) error {
	if settings.Threshold < 1 || settings.Threshold > 365 {
		return fmt.Errorf("threshold must be between 1 and 365")
	}
	if len(settings.TelegramToken) > 256 || (settings.TelegramToken != "" && !telegramTokenRe.MatchString(settings.TelegramToken)) {
		return fmt.Errorf("invalid Telegram token")
	}
	if len(settings.TelegramChat) > 64 || (settings.TelegramChat != "" && !telegramChatRe.MatchString(settings.TelegramChat)) {
		return fmt.Errorf("invalid Telegram chat")
	}
	if (settings.TelegramToken == "") != (settings.TelegramChat == "") {
		return fmt.Errorf("Telegram token and chat must be configured together")
	}
	if len(settings.DiscordWebhook) > 2048 {
		return fmt.Errorf("Discord webhook is too long")
	}
	if settings.DiscordWebhook != "" {
		if err := validateDiscordWebhook(settings.DiscordWebhook); err != nil {
			return err
		}
	}
	if strings.ContainsAny(settings.SmtpPassword, "\x00\r\n") || len(settings.SmtpPassword) > 1024 {
		return fmt.Errorf("invalid SMTP password")
	}
	if settings.SmtpEmail != "" {
		if err := validateEmailAddress(settings.SmtpEmail); err != nil {
			return fmt.Errorf("invalid SMTP sender email")
		}
	}
	if settings.TargetEmail != "" {
		if err := validateEmailAddress(settings.TargetEmail); err != nil {
			return fmt.Errorf("invalid target email")
		}
	}
	if settings.SmtpPassword != "" && (settings.SmtpEmail == "" || settings.TargetEmail == "") {
		return fmt.Errorf("SMTP sender and target email are required when SMTP is configured")
	}
	return nil
}

func readAlertSettings(path string) (AlertSettings, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return AlertSettings{Threshold: 14}, nil
	}
	if err != nil {
		return AlertSettings{}, err
	}
	var settings AlertSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return AlertSettings{}, fmt.Errorf("decode alert settings: %w", err)
	}
	return settings, nil
}

func mergeAlertSecrets(current, incoming AlertSettings) AlertSettings {
	if incoming.TelegramEnabled == nil {
		incoming.TelegramEnabled = current.TelegramEnabled
	}
	if incoming.DiscordEnabled == nil {
		incoming.DiscordEnabled = current.DiscordEnabled
	}
	if incoming.SmtpEnabled == nil {
		incoming.SmtpEnabled = current.SmtpEnabled
	}
	if strings.TrimSpace(incoming.TelegramToken) == "" {
		incoming.TelegramToken = current.TelegramToken
	}
	if strings.TrimSpace(incoming.DiscordWebhook) == "" {
		incoming.DiscordWebhook = current.DiscordWebhook
	}
	if incoming.SmtpPassword == "" {
		incoming.SmtpPassword = current.SmtpPassword
	}
	incoming.TelegramToken = strings.TrimSpace(incoming.TelegramToken)
	incoming.TelegramChat = strings.TrimSpace(incoming.TelegramChat)
	incoming.DiscordWebhook = strings.TrimSpace(incoming.DiscordWebhook)
	incoming.SmtpEmail = strings.TrimSpace(incoming.SmtpEmail)
	incoming.TargetEmail = strings.TrimSpace(incoming.TargetEmail)
	return incoming
}

func writeFileAtomic0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".alert-settings-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func encodeForShell(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

const localRetentionCopies = 14

// A deletion is expected only when at least keep newer backup artifacts for
// the same target still exist. The timestamp comes from the backup filename,
// not mtime, so copying an old backup back cannot make it look new.
const rotationDecisionShell = `backup_key() {
    local name="${1##*/}"
    if [[ "$name" =~ ^(.*)(20[0-9]{2}-?[0-9]{2}-?[0-9]{2})([_-]([0-9]{6}))? ]]; then
        local prefix="${BASH_REMATCH[1]}"
        local day="${BASH_REMATCH[2]//-/}"
        local backup_time="${BASH_REMATCH[4]:-000000}"
        printf '%s|%s%s\n' "$prefix" "$day" "$backup_time"
        return 0
    fi
    return 1
}

is_expected_rotation() {
    local deleted="$1" keep="$2"
    local dir="${deleted%/*}" name="${deleted##*/}"
    local key prefix stamp candidate other other_prefix other_stamp
    local newer=0
    key=$(backup_key "$name") || return 1
    IFS='|' read -r prefix stamp <<< "$key"

    for candidate in "$dir"/*; do
        [[ -f "$candidate" && -s "$candidate" && ! -L "$candidate" ]] || continue
        case "$candidate" in
            *.zip|*.tar.gz|*.sql.gz|*.gz|*.sql) ;;
            *) continue ;;
        esac
        other=$(backup_key "${candidate##*/}") || continue
        IFS='|' read -r other_prefix other_stamp <<< "$other"
        if [[ "$other_prefix" == "$prefix" && "$other_stamp" > "$stamp" ]]; then
            ((newer += 1))
            if ((newer >= keep)); then
                return 0
            fi
        fi
    done
    return 1
}

# Only the local backup script writes these path-specific, short-lived markers.
# Consume the marker once so an unrelated later deletion still raises an alert.
is_script_rotation() {
    local deleted="$1" digest marker issued now
    digest=$(printf '%s' "$deleted" | sha256sum)
    digest="${digest%% *}"
    marker="${BACKUP_MONITOR_ROTATION_DIR:-/run/backup-monitor-rotations}/$digest"
    [[ -f "$marker" && ! -L "$marker" ]] || return 1
    issued=$(<"$marker")
    rm -f -- "$marker"
    [[ "$issued" =~ ^[0-9]+$ ]] || return 1
    now=$(date +%s)
    (( now >= issued && now - issued <= 120 ))
}`

func buildAlertScripts(settings AlertSettings) (string, string) {
	variables := fmt.Sprintf(`decode_value() { printf '%%s' "$1" | base64 -d; }
DISCORD_WEBHOOK=$(decode_value '%s')
TELEGRAM_TOKEN=$(decode_value '%s')
TELEGRAM_CHAT=$(decode_value '%s')
SMTP_EMAIL=$(decode_value '%s')
SMTP_PASSWORD=$(decode_value '%s')
TARGET_EMAIL=$(decode_value '%s')`,
		encodeForShell(settings.DiscordWebhook),
		encodeForShell(settings.TelegramToken),
		encodeForShell(settings.TelegramChat),
		encodeForShell(settings.SmtpEmail),
		encodeForShell(settings.SmtpPassword),
		encodeForShell(settings.TargetEmail),
	)
	if alertChannelEnabled(settings.DiscordEnabled, settings.DiscordWebhook != "") {
		variables += "\nDISCORD_ENABLED=1"
	} else {
		variables += "\nDISCORD_ENABLED=0"
	}
	if alertChannelEnabled(settings.TelegramEnabled, settings.TelegramToken != "") {
		variables += "\nTELEGRAM_ENABLED=1"
	} else {
		variables += "\nTELEGRAM_ENABLED=0"
	}
	if alertChannelEnabled(settings.SmtpEnabled, settings.SmtpPassword != "") {
		variables += "\nSMTP_ENABLED=1"
	} else {
		variables += "\nSMTP_ENABLED=0"
	}

	integrityScript := fmt.Sprintf(`#!/bin/bash
set -u
%s
if ! BACKUP_DIRS=$(/usr/bin/rclone lsd gdrive:Backup/ --config /root/.config/rclone/rclone.conf); then
    echo "[$(date '+%%F %%T')] Không thể đọc Google Drive; chưa xác nhận số thư mục" >&2
    exit 1
fi
BACKUP_COUNT=$(printf '%%s\n' "$BACKUP_DIRS" | sed '/^$/d' | wc -l)
echo "[$(date '+%%F %%T')] Da kiem tra Drive: $BACKUP_COUNT thu muc Backup"
if [ "$BACKUP_COUNT" -lt %d ]; then
    MSG="CANH BAO THIEU THU MUC BACKUP: Google Drive hien co $BACKUP_COUNT/%d thu muc. Vui long kiem tra!"
	echo "[$(date '+%%F %%T')] CẢNH BÁO: $MSG"
    if [ "$DISCORD_ENABLED" = 1 ] && [ -n "$DISCORD_WEBHOOK" ]; then curl -fsS -H "Content-Type: application/json" --data "{\"content\":\"$MSG\"}" "$DISCORD_WEBHOOK" >/dev/null; fi
    if [ "$TELEGRAM_ENABLED" = 1 ] && [ -n "$TELEGRAM_TOKEN" ]; then curl -fsS -X POST "https://api.telegram.org/bot${TELEGRAM_TOKEN}/sendMessage" --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$MSG" >/dev/null; fi
    if [ "$SMTP_ENABLED" = 1 ] && [ -n "$SMTP_PASSWORD" ]; then printf 'From: %%s\nTo: %%s\nSubject: [ALERT] Backup Monitor\n\n%%s\n' "$SMTP_EMAIL" "$TARGET_EMAIL" "$MSG" | curl -fsS --url 'smtps://smtp.gmail.com:465' --ssl-reqd --mail-from "$SMTP_EMAIL" --mail-rcpt "$TARGET_EMAIL" --user "$SMTP_EMAIL:$SMTP_PASSWORD" -T - >/dev/null; fi
    exit 1
fi
exit 0`, variables, settings.Threshold, settings.Threshold)

	realtimeScript := fmt.Sprintf(`#!/bin/bash
set -u
%s
%s
send_delete_alert() {
    local file="$1" msg
    msg="BAO DONG KHAN CAP: File backup [$file] vua bi XOA khoi may chu! Thoi gian: $(date)"
    if [ "$DISCORD_ENABLED" = 1 ] && [ -n "$DISCORD_WEBHOOK" ]; then curl -fsS -H "Content-Type: application/json" --data "{\"content\":\"$msg\"}" "$DISCORD_WEBHOOK" >/dev/null; fi
    if [ "$TELEGRAM_ENABLED" = 1 ] && [ -n "$TELEGRAM_TOKEN" ]; then curl -fsS -X POST "https://api.telegram.org/bot${TELEGRAM_TOKEN}/sendMessage" --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$msg" >/dev/null; fi
    if [ "$SMTP_ENABLED" = 1 ] && [ -n "$SMTP_PASSWORD" ]; then printf 'From: %%s\nTo: %%s\nSubject: [URGENT] File Deleted\n\n%%s\n' "$SMTP_EMAIL" "$TARGET_EMAIL" "$msg" | curl -fsS --url 'smtps://smtp.gmail.com:465' --ssl-reqd --mail-from "$SMTP_EMAIL" --mail-rcpt "$TARGET_EMAIL" --user "$SMTP_EMAIL:$SMTP_PASSWORD" -T - >/dev/null; fi
}

check_panel_zip_replacement() {
    local file="$1" deleted_at="$2" attempt modified
    # Do not keep the main monitor lock or inotify pipe open while checking.
    exec 9>&-
    for ((attempt = 0; attempt < 12; attempt++)); do
        sleep 5
        if [[ -f "$file" && -s "$file" && ! -L "$file" ]]; then
            modified=$(stat -c %%Y -- "$file" 2>/dev/null || true)
            if [[ "$modified" =~ ^[0-9]+$ ]] && (( modified >= deleted_at )) && unzip -tqq "$file" >/dev/null 2>&1; then
                return 0
            fi
        fi
    done
    send_delete_alert "$file"
}

exec 9>/run/backup-monitor-inotify.lock
flock -n 9 || exit 0
inotifywait -m -r -e delete --format '%%w%%f' /www/backup/ 2>/dev/null | while IFS= read -r FILE
do
    if [[ ! "$FILE" =~ \.(zip|tar\.gz|gz|sql)$ ]]; then
        continue
    fi

    # aaPanel removes SQL work files after packing its dated ZIP.
    if [[ "$FILE" =~ ^/www/backup/panel/[0-9]{4}-[0-9]{2}-[0-9]{2}/data/ ]]; then
        continue
    fi

    # Script-controlled same-day replacement or aaPanel's count-based rotation.
    if is_script_rotation "$FILE" || is_expected_rotation "$FILE" %d; then
        continue
    fi

    if [[ "$FILE" == "/www/backup/panel/$(date +%%F).zip" ]]; then
        check_panel_zip_replacement "$FILE" "$(date +%%s)" </dev/null >/dev/null 2>&1 &
        continue
    fi

    send_delete_alert "$FILE"
 done`, variables, rotationDecisionShell, localRetentionCopies)

	return configuredBackupCommand(integrityScript), configuredBackupCommand(realtimeScript)
}

func buildAlertInstallCommand(integrityScript, realtimeScript string) string {
	return fmt.Sprintf(`set -eu
umask 077
command -v inotifywait >/dev/null 2>&1 || { dnf install epel-release -y >/dev/null 2>&1 && dnf install inotify-tools -y >/dev/null 2>&1; }
SCRIPTS_DIR=/root/scripts
[ ! -L "$SCRIPTS_DIR" ] || { printf 'Scripts directory must not be a symlink\n' >&2; exit 1; }
install -d -m 700 "$SCRIPTS_DIR"
[ "$(stat -c '%%u' "$SCRIPTS_DIR")" = 0 ] || { printf 'Scripts directory must be root-owned\n' >&2; exit 1; }
INTEGRITY_TMP=$(mktemp "$SCRIPTS_DIR/.check_integrity.XXXXXXXX")
REALTIME_TMP=$(mktemp "$SCRIPTS_DIR/.realtime_monitor.XXXXXXXX")
trap 'rm -f -- "$INTEGRITY_TMP" "$REALTIME_TMP"' EXIT
printf '%%s' '%s' | base64 -d > "$INTEGRITY_TMP"
printf '%%s' '%s' | base64 -d > "$REALTIME_TMP"
chmod 600 "$INTEGRITY_TMP" "$REALTIME_TMP"
mv -f -- "$INTEGRITY_TMP" "$SCRIPTS_DIR/check_integrity.sh"
mv -f -- "$REALTIME_TMP" "$SCRIPTS_DIR/realtime_monitor.sh"
exec 8>/root/.backup-monitor-crontab.lock
flock -x 8
CURRENT_CRON=$(crontab -l 2>/dev/null || true)
UPDATED_CRON=$(printf '%%s\n' "$CURRENT_CRON" | grep -v 'realtime_monitor.sh' || true)
if ! printf '%%s\n' "$CURRENT_CRON" | awk 'NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && $7 == "/root/scripts/check_integrity.sh" {found=1} END {exit !found}'; then
  UPDATED_CRON="$UPDATED_CRON
0 12 * * * /bin/bash /root/scripts/check_integrity.sh >> /root/check_integrity.log 2>&1"
fi
printf '%%s\n' "$UPDATED_CRON" '@reboot nohup /bin/bash /root/scripts/realtime_monitor.sh >/dev/null 2>&1 &' | crontab -
flock -u 8
exec 8>&-
OLD_MONITORS=$(pgrep -f '^(/bin/)?bash /root/(scripts/)?realtime_monitor[.]sh$' || true)
for PID in $OLD_MONITORS; do
  pkill -TERM -P "$PID" 2>/dev/null || true
  kill -TERM "$PID" 2>/dev/null || true
done
for attempt in 1 2 3 4 5; do
  if flock -n /run/backup-monitor-inotify.lock -c true; then
    nohup /bin/bash /root/scripts/realtime_monitor.sh >/dev/null 2>&1 &
    exit 0
  fi
  sleep 1
done
printf 'Previous backup monitor still holds the lock\n' >&2
exit 1
`, encodeForShell(integrityScript), encodeForShell(realtimeScript))
}

var runJobSSHCommand = executeSSHCommand

// Only allowlisted scripts still scheduled in root's crontab may be started.
// The client never supplies a command or path.
func commandForCronJob(jobID string) (string, bool) {
	if task, ok := managedCronTaskByID(jobID); ok {
		return commandForManagedCronJob(task), true
	}
	return "", false
}

func commandForJob(jobID string) (string, bool) {
	switch jobID {
	case "drive-sync":
		return commandForCronJob("drive-sync")
	case "site-backup":
		return commandForCronJob("backup-site")
	case "database-backup":
		return commandForCronJob("backup-database")
	default:
		return "", false
	}
}

func setupRouter(cfg AppConfig) *gin.Engine {
	runtimeConfig = cfg

	r := gin.Default()
	_ = r.SetTrustedProxies(nil)
	loginLimiter := newLoginRateLimiter(5, 5*time.Minute)

	// Cấu hình CORS để Frontend gọi được API
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSAllowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	// LOGIN - không cần auth
	r.POST("/api/login", func(c *gin.Context) {
		clientKey := remoteAddressKey(c.Request.RemoteAddr)
		now := time.Now()
		if !loginLimiter.allowed(clientKey, now) {
			c.Header("Retry-After", "300")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Đăng nhập thất bại quá nhiều lần, vui lòng thử lại sau"})
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		userMatches := constantTimeEqual(req.Username, cfg.AdminUser)
		passwordMatches := constantTimeEqual(req.Password, cfg.AdminPassword)
		if !userMatches || !passwordMatches {
			loginLimiter.recordFailure(clientKey, now)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Tên đăng nhập hoặc mật khẩu không đúng"})
			return
		}
		loginLimiter.reset(clientKey)
		token, err := generateToken(req.Username, cfg.JWTSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token})
	})

	// Tất cả route bên dưới đều yêu cầu đăng nhập
	auth := r.Group("/api")
	auth.Use(authMiddleware(cfg.JWTSecret))
	auth.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/api/backup-system/apply" {
			c.Next()
			return
		}
		backupSystemMutation.RLock()
		defer backupSystemMutation.RUnlock()
		if c.Request.Method != http.MethodGet && !strings.HasPrefix(c.Request.URL.Path, "/api/backup-system/") && !backupSystemLoaded.Load() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Chưa tải được cấu hình sao lưu từ máy chủ; hãy tải lại mục Hệ thống sao lưu trước khi thực hiện thay đổi"})
			return
		}
		c.Next()
	})
	registerBackupSystemRoutes(auth)
	{
		auth.POST("/download-ticket", func(c *gin.Context) {
			var req struct {
				Path string `json:"path"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || !validSnapshotPath(req.Path) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Đường dẫn không hợp lệ"})
				return
			}
			ticket, err := issueDownloadTicket(req.Path)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo link tải"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"ticket": ticket})
		})

		auth.GET("/server-status", func(c *gin.Context) {
			cmds := `
DISK=$(df -h / | awk 'NR==2 {printf "{\"total\":\"%s\", \"used\":\"%s\", \"free\":\"%s\", \"usage\":\"%s\"}", $2, $3, $4, $5}')
RAM=$(free -m | awk 'NR==2{printf "{\"total\":\"%sMB\", \"used\":\"%sMB\", \"free\":\"%sMB\", \"usage\":\"%.1f%%\"}", $2, $3, $4, $3*100/$2}')
CPU=$(vmstat 1 2 | tail -1 | awk '{printf "%.1f%%", 100 - $15}')
UPTIME=$(uptime -p | sed 's/up //')
SERVER_TIME=$(date -Iseconds)
WEBSITES=$(find /www/wwwroot -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -v "/default$" | wc -l)
DATABASES=$(find /www/server/data -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -vE "/(mysql|performance_schema|sys|phpmyadmin)$" | wc -l)
CRON_SCHEDULES=$(crontab -l 2>/dev/null | awk '
  NF >= 7 && $1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ && $6 == "/bin/bash" {
    if ($7 == "/root/scripts/backup-site.sh" || $7 == "/root/scripts/backup-database.sh" || $7 == "/root/scripts/backup-panel.sh" || ($7 == "/root/scripts/backup-monitor-run.sh" && ($8 == "backup-site" || $8 == "backup-database" || $8 == "backup-panel"))) type = "local"
    else if ($7 == "/root/scripts/auto_backup.sh") type = "drive"
    else if ($7 == "/root/scripts/cleanup-panel-backups.sh" || $7 == "/root/scripts/check_integrity.sh") type = "other"
    else next
    printf "%s %02d:%02d\n", type, $2, $1
  }
')
CRONJOBS=$(printf '%s\n' "$CRON_SCHEDULES" | sed '/^$/d' | wc -l)
CRON_TIMES=$(printf '%s\n' "$CRON_SCHEDULES" | awk 'NF == 2 {print $2}' | sort | paste -sd, - | sed 's/,/, /g')
DRIVE_CRONJOBS=$(printf '%s\n' "$CRON_SCHEDULES" | awk '$1 == "drive" {print 1}' | wc -l)
DRIVE_CRON_TIMES=$(printf '%s\n' "$CRON_SCHEDULES" | awk '$1 == "drive" {print $2}' | sort | paste -sd, - | sed 's/,/, /g')
LOCAL_CRONJOBS=$(printf '%s\n' "$CRON_SCHEDULES" | awk '$1 == "local" {print 1}' | wc -l)
LOCAL_CRON_TIMES=$(printf '%s\n' "$CRON_SCHEDULES" | awk '$1 == "local" {print $2}' | sort | paste -sd, - | sed 's/,/, /g')

LOCAL_BACKUP_DIR="/www/backup"
if [ -d "$LOCAL_BACKUP_DIR" ]; then
  # Only count backup artifacts belonging to websites, databases, or aaPanel.
  # Ignore temporary files and unrelated content kept in /www/backup.
  LOCAL_FILES=$(` + localBackupFindCommand + ` -printf '%p\n' 2>/dev/null || true)
  LOCAL_COUNT=$(printf '%s\n' "$LOCAL_FILES" | sed '/^$/d' | wc -l)
  LOCAL_SIZE=$(printf '%s\n' "$LOCAL_FILES" | sed '/^$/d' | xargs -r du -ch 2>/dev/null | tail -1 | awk '{print $1}')
  LOCAL_SIZE=${LOCAL_SIZE:-0}
  LOCAL_LATEST=$(printf '%s\n' "$LOCAL_FILES" | sed '/^$/d' | xargs -r -n1 stat -c '%Y %n' 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2-)
  LOCAL_LATEST_DATE=$(stat -c '%y' "$LOCAL_LATEST" 2>/dev/null | cut -d' ' -f1)
  LOCAL_LATEST_NAME=$(basename "$LOCAL_LATEST" 2>/dev/null)
else
  LOCAL_SIZE="0"
  LOCAL_COUNT=0
  LOCAL_LATEST_DATE=""
  LOCAL_LATEST_NAME=""
fi

LOCAL_BACKUP="{\"size\":\"$LOCAL_SIZE\", \"count\":$LOCAL_COUNT, \"latest_date\":\"$LOCAL_LATEST_DATE\", \"latest_name\":\"$LOCAL_LATEST_NAME\"}"

echo "{\"server_time\":\"$SERVER_TIME\", \"disk\": $DISK, \"ram\": $RAM, \"cpu\": \"$CPU\", \"uptime\": \"$UPTIME\", \"websites\": $WEBSITES, \"databases\": $DATABASES, \"drive_crons\": $DRIVE_CRONJOBS, \"drive_cron_times\": \"$DRIVE_CRON_TIMES\", \"local_crons\": $LOCAL_CRONJOBS, \"local_cron_times\": \"$LOCAL_CRON_TIMES\", \"crons\": $CRONJOBS, \"cron_times\": \"$CRON_TIMES\", \"local_backup\": $LOCAL_BACKUP}"
`
			output, err := cachedGet("server-status", 15*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kết nối SSH lấy thông số", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 2: Kiểm tra các file Backup trên Google Drive và Dung lượng
		auth.GET("/backup-status", func(c *gin.Context) {
			cmds := `
TODAY=$(date +"%Y-%m-%d")

LSF_OUT=$(timeout -k 5s 40s /usr/bin/rclone lsf -R --format "ps" gdrive:Backup --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list 2>&1)
LSF_STATUS=$?
DRIVE_ERROR=""
if [ "$LSF_STATUS" -ne 0 ]; then
  LSF_ERROR_LOWER=$(printf '%s' "$LSF_OUT" | tr '[:upper:]' '[:lower:]')
  case "$LSF_ERROR_LOWER" in
    *ratelimitexceeded*|*rate*limit*exceeded*|*quota*exceeded*) DRIVE_ERROR="Google Drive API đang giới hạn truy vấn; cần kiểm tra quota và cấu hình client ID riêng cho rclone" ;;
    *) DRIVE_ERROR="rclone mã lỗi $LSF_STATUS; kiểm tra DNS, mạng và cấu hình rclone trên máy chủ" ;;
  esac
  LSF_OUT=""
fi
ABOUT=""
ABOUT_CACHE=/tmp/rclone_about.json
if [ -f "$ABOUT_CACHE" ] && [ ! -L "$ABOUT_CACHE" ]; then
  CACHE_TIME=$(stat -c '%Y' "$ABOUT_CACHE" 2>/dev/null || printf '0')
  CACHE_AGE=$(( $(date +%s) - CACHE_TIME ))
  if [ "$CACHE_AGE" -ge 0 ] && [ "$CACHE_AGE" -lt 86400 ]; then
    ABOUT=$(cat "$ABOUT_CACHE")
  fi
fi
if [ -z "$DRIVE_ERROR" ] && [ -z "$ABOUT" ]; then
  ABOUT=$(timeout -k 5s 10s /usr/bin/rclone about gdrive: --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --json 2>/dev/null)
  if [ -n "$ABOUT" ]; then
    ABOUT_TMP=$(mktemp /tmp/rclone_about.XXXXXXXX)
    printf '%s\n' "$ABOUT" > "$ABOUT_TMP"
    mv -f -- "$ABOUT_TMP" "$ABOUT_CACHE"
  fi
fi
if [ -z "$ABOUT" ] && [ -f "$ABOUT_CACHE" ] && [ ! -L "$ABOUT_CACHE" ]; then
  ABOUT=$(cat "$ABOUT_CACHE")
fi
EVAL_OUT=$(printf '%s\n' "$LSF_OUT" | awk -F';' -v today="$TODAY" '
$2 != "-1" && $2 != "" {
    total_bytes += $2
    total_files++
    split($1, a, "/")
    day = a[1]
    if (day ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/) {
        days[day] = 1
        size[day] += $2
        count[day]++
        if (day == today && length(a[2]) > 0) {
            today_dirs[a[2]] = 1
            if (a[2] == "site") { today_site++; today_site_bytes += $2 }
            else if (a[2] == "database") { today_db++; today_db_bytes += $2 }
            else if (a[2] == "panel") { today_panel++; today_panel_bytes += $2 }
        }
    }
}
END {
    folders = 0
    for (d in days) folders++

    td = ""
    for (subd in today_dirs) td = td subd ","

    n = asorti(size, sorted_days)
    hist = "["
    first = 1
    for (i = 1; i <= n; i++) {
        d = sorted_days[i]
        if (!first) hist = hist ","
        hist = hist "{\"date\":\"" d "\", \"bytes\":" size[d] ", \"files\":" count[d] "}"
        first = 0
    }
    hist = hist "]"

    printf "%d|%d|%d|%s|%s|%d|%d|%d|%d|%d|%d", total_bytes, total_files, folders, td, hist, today_site+0, today_db+0, today_panel+0, today_site_bytes+0, today_db_bytes+0, today_panel_bytes+0
}')

TOTAL_BYTES=$(echo "$EVAL_OUT" | cut -d'|' -f1)
TOTAL_FILES=$(echo "$EVAL_OUT" | cut -d'|' -f2)
TOTAL_FOLDERS=$(echo "$EVAL_OUT" | cut -d'|' -f3)
DIRS=$(echo "$EVAL_OUT" | cut -d'|' -f4)
RAW_TAIL=$(echo "$EVAL_OUT" | cut -d'|' -f5-)
TODAY_SITE=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-5)}')
TODAY_DB=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-4)}')
TODAY_PANEL=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-3)}')
TODAY_SITE_BYTES=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-2)}')
TODAY_DB_BYTES=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-1)}')
TODAY_PANEL_BYTES=$(echo "$EVAL_OUT" | awk -F'|' '{print $NF}')
HISTORY=$(echo "$RAW_TAIL" | sed 's/|[0-9]*|[0-9]*|[0-9]*|[0-9]*|[0-9]*|[0-9]*$//')

if [ -z "$TOTAL_BYTES" ]; then TOTAL_BYTES="0"; fi
if [ -z "$TOTAL_FILES" ]; then TOTAL_FILES="0"; fi
if [ -z "$TOTAL_FOLDERS" ]; then TOTAL_FOLDERS="0"; fi
if [ -z "$HISTORY" ] || [ "$HISTORY" = "]" ]; then HISTORY="[]"; fi
if [ -z "$ABOUT" ]; then ABOUT="{}"; fi

SIZE="{\"bytes\": $TOTAL_BYTES, \"count\": $TOTAL_FILES}"
if [ -n "$DRIVE_ERROR" ]; then
  SIZE="null"
  HISTORY="[]"
fi

ACTIVITY="["
FIRST=1
if [ -f /var/log/aapanel_backup.log ]; then
  while IFS='|' read -r ad at adur ast; do
    [ -z "$ad" ] && continue
    if [ "$FIRST" -ne 1 ]; then ACTIVITY="$ACTIVITY,"; fi
    FIRST=0
    ACTIVITY="$ACTIVITY{\"name\":\"Lưu trữ bản backup lên Google Drive\",\"date\":\"$ad\",\"time\":\"$at\",\"duration\":\"$adur\",\"status\":\"$ast\"}"
  done < /var/log/aapanel_backup.log
fi
ACTIVITY="$ACTIVITY]"

LOCAL_LOGS=$(awk '
    /start backup\[/ {
        match($0, /\[(.*)\]/, arr)
        dt = arr[1]
        split(dt, d, " ")
        date = d[1]
        time = d[2]
    }
    /Backup site: / {
        match($0, /site: (.*)/, arr)
        name = "Backup Website: " arr[1]
    }
    /Backup .*database: / {
        match($0, /database: (.*)/, arr)
        name = "Backup Database: " arr[1]
    }
    /Compression completed, took / {
        match($0, /took ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    /Database backup completed, taking / {
        match($0, /taking ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    ' /www/server/cron/*.log 2>/dev/null | sed 's/,$//')
CUSTOM_LOCAL_LOGS=$(` + customLocalActivityCommand() + `)
RUN_LOGS=$(` + backupRunActivityCommand() + `)
PANEL_LOGS=$(` + panelActivityCommand() + `)
PANEL_ARCHIVES=$(` + panelArchiveActivityCommand() + `)
LOCAL_ACTIVITY="["
SEP=""
for ENTRIES in "$LOCAL_LOGS" "$CUSTOM_LOCAL_LOGS" "$RUN_LOGS" "$PANEL_LOGS" "$PANEL_ARCHIVES"; do
  if [ -n "$ENTRIES" ]; then LOCAL_ACTIVITY="$LOCAL_ACTIVITY$SEP$ENTRIES"; SEP=","; fi
done
LOCAL_ACTIVITY="$LOCAL_ACTIVITY]"

echo "{\"about\": $ABOUT, \"size\": $SIZE, \"dirs\": \"$DIRS\", \"totalFolders\": $TOTAL_FOLDERS, \"history\": $HISTORY, \"activity\": $ACTIVITY, \"localActivity\": $LOCAL_ACTIVITY, \"driveError\": \"$DRIVE_ERROR\", \"todayBreakdown\": {\"site\": ${TODAY_SITE:-0}, \"database\": ${TODAY_DB:-0}, \"panel\": ${TODAY_PANEL:-0}}, \"todayBreakdownBytes\": {\"site\": ${TODAY_SITE_BYTES:-0}, \"database\": ${TODAY_DB_BYTES:-0}, \"panel\": ${TODAY_PANEL_BYTES:-0}}}"
`
			output, err := cachedGetWithPolicy("backup-status", 120*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				if e != nil {
					return nil, e
				}
				response, good, e := mergeDriveStatus([]byte(out), currentGoodDriveStatus(), time.Now())
				if e != nil {
					return nil, e
				}
				if len(good) > 0 {
					rememberGoodDriveStatus(good)
				}
				return response, nil
			}, backupStatusTTL, markDriveStatusStale)

			if err != nil {
				if unavailable, ok := err.(driveUnavailableError); ok {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": unavailable.Error()})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi đọc Google Drive", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 3: Kích hoạt một loại backup cố định bằng script được quản lý.
		auth.POST("/run-job", func(c *gin.Context) {
			var req struct {
				JobID string `json:"jobId"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
				return
			}
			command, ok := commandForJob(req.JobID)
			if !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "jobId không hợp lệ"})
				return
			}
			output, err := runJobSSHCommand(command)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy backup"})
				return
			}
			if strings.TrimSpace(output) != "STARTED" {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được backup đã khởi chạy"})
				return
			}
			invalidate("cron-jobs")
			c.JSON(http.StatusAccepted, gin.H{"status": "started"})
		})

		// Chạy thủ công đúng script được quản lý còn hiện diện trong crontab.
		var cronRunMu sync.Mutex
		lastCronRun := make(map[string]time.Time)
		auth.POST("/run-cron-job", func(c *gin.Context) {
			var req struct {
				JobID string `json:"jobId"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
				return
			}
			command, ok := commandForCronJob(req.JobID)
			if !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Mã cronjob không hợp lệ"})
				return
			}
			cronRunMu.Lock()
			if time.Since(lastCronRun[req.JobID]) < time.Minute {
				cronRunMu.Unlock()
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "Cronjob vừa được khởi chạy; hãy đợi ít nhất 1 phút"})
				return
			}
			lastCronRun[req.JobID] = time.Now()
			cronRunMu.Unlock()
			releaseReservation := func() {
				cronRunMu.Lock()
				delete(lastCronRun, req.JobID)
				cronRunMu.Unlock()
			}
			output, err := runJobSSHCommand(command)
			if err != nil {
				releaseReservation()
				if strings.Contains(err.Error(), "CRON_ALREADY_RUNNING") {
					c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy đợi hoàn tất và xem log"})
					return
				}
				if strings.Contains(err.Error(), "CRON_NOT_SCHEDULED") {
					c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy"})
					return
				}
				if strings.Contains(err.Error(), "CRON_SCRIPT_UNAVAILABLE") {
					c.JSON(http.StatusConflict, gin.H{"error": "Script cronjob không còn khả dụng"})
					return
				}
				if req.JobID == "cleanup-panel" {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Dọn dẹp aaPanel chưa hoàn tất; hãy kiểm tra log cronjob"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy cronjob"})
				return
			}
			if req.JobID == "cleanup-panel" {
				deleted, ok := parsePanelCleanupResult(output)
				if !ok {
					releaseReservation()
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả dọn dẹp aaPanel"})
					return
				}
				invalidate("cron-jobs")
				c.JSON(http.StatusOK, gin.H{"status": "completed", "deletedCount": deleted})
				return
			}
			if strings.TrimSpace(output) != "STARTED" {
				releaseReservation()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được cronjob đã khởi chạy"})
				return
			}
			// Triggering is not completion: keep the expensive Drive/snapshot caches
			// available until their normal TTL expires, so dashboards stay responsive.
			invalidate("cron-jobs")
			c.JSON(http.StatusAccepted, gin.H{"status": "started"})
		})

		// API 4: Giám sát Uptime các website (động theo thư mục /www/wwwroot)
		auth.GET("/websites-status", func(c *gin.Context) {
			output, err := cachedGet("websites-status", 15*time.Second, func() ([]byte, error) {
				return collectWebsiteStatuses(executeSSHCommandRaw)
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kiểm tra website", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 5: Đọc Log hệ thống Real-time
		auth.GET("/logs", func(c *gin.Context) {
			logType := c.Query("type")
			cmd := ""

			switch logType {
			case "backup":
				// Kết hợp: Log cron + Chi tiết file backup trên Google Drive
				cmd = `
echo "══════════════════════════════════════════════════════"
echo "  LỊCH SỬ CHẠY SCRIPT (Cron Execution Log)"
echo "══════════════════════════════════════════════════════"
tail -n 15 /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/cleanup-panel-backups.log /root/check_integrity.log /www/server/cron/*.log 2>/dev/null
echo ""
echo "══════════════════════════════════════════════════════"
echo "  CHI TIẾT BẢN SAO LƯU TRÊN GOOGLE DRIVE"
echo "══════════════════════════════════════════════════════"
for DAY in $(/usr/bin/rclone lsf --dirs-only gdrive:Backup --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | sort | tail -n 3); do
  DAYNAME=$(echo "$DAY" | tr -d '/')
  echo ""
  echo "📅 [$DAYNAME]"
  echo "  📁 site/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/site/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
  echo "  📁 database/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/database/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
done
`
			case "system":
				cmd = "tail -n 50 /var/log/messages"
			case "secure":
				cmd = "tail -n 50 /var/log/secure"
			default:
				cmd = "tail -n 50 /var/log/messages"
			}

			key := "logs-" + logType
			ttl := 10 * time.Second
			if logType == "backup" {
				ttl = 30 * time.Second
			}
			output, err := cachedGet(key, ttl, func() ([]byte, error) {
				out, e := executeSSHCommand(cmd)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log", "detail": err.Error()})
				return
			}

			c.String(http.StatusOK, string(output))
		})

		// API 6: Liệt kê các script được quản lý trong root crontab.
		auth.POST("/cron-tracking", func(c *gin.Context) {
			output, err := executeSSHCommand(buildCronTrackingInstallCommand())
			if err != nil {
				switch {
				case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy cron sao lưu website, cơ sở dữ liệu hoặc cấu hình aaPanel để bật ghi nhận"})
				case strings.Contains(err.Error(), "CRON_DUPLICATE"):
					c.JSON(http.StatusConflict, gin.H{"error": "Có lịch sao lưu trùng; cần kiểm tra crontab trước khi bật ghi nhận"})
				case strings.Contains(err.Error(), "CRON_WRAPPER_CONFLICT"):
					c.JSON(http.StatusConflict, gin.H{"error": "File bộ ghi nhận đã tồn tại nhưng không thuộc dashboard; chưa ghi đè"})
				case strings.Contains(err.Error(), "CRON_SCRIPT_UNAVAILABLE"):
					c.JSON(http.StatusConflict, gin.H{"error": "Script sao lưu không còn khả dụng; chưa đổi crontab"})
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể bật ghi nhận cron tự động; lịch cũ được lưu dự phòng nếu đã cập nhật"})
				}
				return
			}
			if strings.TrimSpace(output) != "TRACKING_ENABLED" {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được việc bật ghi nhận cron tự động"})
				return
			}
			invalidate("cron-jobs", "server-status", "backup-status")
			c.JSON(http.StatusOK, gin.H{"status": "enabled"})
		})

		auth.GET("/cron-jobs", func(c *gin.Context) {
			output, err := cachedGet("cron-jobs", 15*time.Second, func() ([]byte, error) {
				return fetchManagedCronJobs()
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc tiến trình định kỳ", "detail": err.Error()})
				return
			}
			c.Data(http.StatusOK, "application/json", output)
		})

		auth.GET("/cron-jobs/:id/log", func(c *gin.Context) {
			task, ok := managedCronTaskByID(c.Param("id"))
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
				return
			}
			c.Header("Cache-Control", "no-store")
			result, err := fetchManagedCronJobLog(task)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log cronjob"})
				return
			}
			c.JSON(http.StatusOK, result)
		})

		auth.PUT("/cron-jobs/:id/schedule", func(c *gin.Context) {
			task, ok := managedCronTaskByID(c.Param("id"))
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
				return
			}
			var req struct {
				Time             string `json:"time"`
				ExpectedSchedule string `json:"expectedSchedule"`
				ExpectedEnabled  *bool  `json:"expectedEnabled"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedEnabled == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
				return
			}
			command, valid := commandForCronScheduleUpdate(task, req.Time, req.ExpectedSchedule, *req.ExpectedEnabled)
			if !valid {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Giờ chạy hoặc lịch hiện tại không hợp lệ"})
				return
			}
			output, err := executeSSHCommand(command)
			if err != nil {
				switch {
				case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy"})
				case strings.Contains(err.Error(), "CRON_DUPLICATE"):
					c.JSON(http.StatusConflict, gin.H{"error": "Có nhiều dòng cron cho cùng script; cần kiểm tra trên máy chủ"})
				case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy làm mới danh sách và thử lại"})
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu lịch chạy cronjob"})
				}
				return
			}
			if !strings.HasPrefix(strings.TrimSpace(output), "UPDATED|/root/backup-monitor/cron-recovery/schedule-"+task.ID+".") {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được lịch chạy đã lưu"})
				return
			}
			invalidate("cron-jobs", "server-status")
			c.JSON(http.StatusOK, gin.H{"schedule": fmt.Sprintf("%s %s %s", req.Time[3:], req.Time[:2], strings.Join(strings.Fields(req.ExpectedSchedule)[2:], " "))})
		})

		auth.DELETE("/cron-jobs/:id", func(c *gin.Context) {
			task, ok := managedCronTaskByID(c.Param("id"))
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
				return
			}
			var req struct {
				ExpectedSchedule string `json:"expectedSchedule"`
				ExpectedEnabled  *bool  `json:"expectedEnabled"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedEnabled == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
				return
			}
			command, valid := commandForCronDeletion(task, req.ExpectedSchedule, *req.ExpectedEnabled)
			if !valid {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Lịch hiện tại không hợp lệ"})
				return
			}
			output, err := executeSSHCommand(command)
			// Refresh even after an uncertain SSH outcome: installation may have
			// completed before the connection failed.
			invalidate("cron-jobs", "server-status", "backup-status")
			if err != nil {
				switch {
				case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy; hãy làm mới danh sách"})
				case strings.Contains(err.Error(), "CRON_DUPLICATE"):
					c.JSON(http.StatusConflict, gin.H{"error": "Có nhiều dòng cron cho cùng script; cần kiểm tra trên máy chủ"})
				case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy đóng xác nhận, làm mới danh sách và thử lại"})
				case strings.Contains(err.Error(), "CRON_ALREADY_RUNNING"):
					c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy chờ kết thúc rồi xóa lịch"})
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa lịch cron; không xóa script, log hay backup"})
				}
				return
			}
			backupPath, ok := strings.CutPrefix(strings.TrimSpace(output), "DELETED|")
			if !ok || !strings.HasPrefix(backupPath, "/root/backup-monitor/cron-recovery/delete-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả xóa; hãy làm mới danh sách trước khi thử lại"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "deleted", "backupPath": backupPath})
		})

		auth.PUT("/cron-jobs/:id/state", func(c *gin.Context) {
			task, ok := managedCronTaskByID(c.Param("id"))
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
				return
			}
			var req struct {
				Enabled          *bool  `json:"enabled"`
				ExpectedSchedule string `json:"expectedSchedule"`
				ExpectedEnabled  *bool  `json:"expectedEnabled"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.ExpectedEnabled == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
				return
			}
			command, valid := commandForCronStateUpdate(task, req.ExpectedSchedule, *req.ExpectedEnabled, *req.Enabled)
			if !valid {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Lịch hiện tại không hợp lệ"})
				return
			}
			output, err := executeSSHCommand(command)
			invalidate("cron-jobs", "server-status", "backup-status")
			if err != nil {
				switch {
				case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong danh sách; hãy làm mới"})
				case strings.Contains(err.Error(), "CRON_DUPLICATE"):
					c.JSON(http.StatusConflict, gin.H{"error": "Có nhiều dòng cron cho cùng script; cần kiểm tra trên máy chủ"})
				case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Lịch hoặc trạng thái đã thay đổi; hãy đóng xác nhận và làm mới danh sách"})
				case strings.Contains(err.Error(), "CRON_ALREADY_RUNNING"):
					c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy chờ kết thúc rồi đổi trạng thái"})
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật trạng thái cron"})
				}
				return
			}
			operation := "pause"
			if *req.Enabled {
				operation = "enable"
			}
			backupPath, ok := strings.CutPrefix(strings.TrimSpace(output), "UPDATED|")
			if !ok || !strings.HasPrefix(backupPath, "/root/backup-monitor/cron-recovery/"+operation+"-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được trạng thái đã lưu; hãy làm mới danh sách"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"enabled": *req.Enabled, "backupPath": backupPath})
		})

		// API 7: Đọc cấu hình hệ thống (lưu trên máy ảo)
		auth.GET("/config", func(c *gin.Context) {
			cmds := `
CONFIG=/root/backup-monitor/config.json
if [ -f "$CONFIG" ]; then
  cat "$CONFIG"
else
  echo '{"retention_days":7,"notify_enabled":false,"notify_token":"","notify_chat_id":"","email_recipient":""}'
fi
`
			output, err := cachedGet("config", 10*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình", "detail": err.Error()})
				return
			}
			c.Data(http.StatusOK, "application/json", output)
		})

		// API 8: Ghi cấu hình hệ thống (validate JSON rồi base64 truyền qua SSH)
		auth.PUT("/config", func(c *gin.Context) {
			payload, err := c.GetRawData()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Body không hợp lệ"})
				return
			}
			var cfg map[string]interface{}
			if err := json.Unmarshal(payload, &cfg); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "JSON không hợp lệ"})
				return
			}
			// Chỉ chấp nhận các trường đã biết
			allowed := map[string]bool{
				"retention_days":  true,
				"notify_enabled":  true,
				"notify_token":    true,
				"notify_chat_id":  true,
				"email_recipient": true,
			}
			clean := map[string]interface{}{}
			for k, v := range cfg {
				if allowed[k] {
					clean[k] = v
				}
			}
			normalized, _ := json.Marshal(clean)
			b64 := base64.StdEncoding.EncodeToString(normalized)

			cmds := fmt.Sprintf(`mkdir -p /root/backup-monitor && echo %s | base64 -d > /root/backup-monitor/config.json && echo OK`, b64)
			output, err := executeSSHCommand(cmds)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể ghi cấu hình", "detail": err.Error()})
				return
			}
			invalidate("config")
			c.JSON(http.StatusOK, gin.H{"status": "success", "message": strings.TrimSpace(output), "config": clean})
		})

		// API 9: Liệt kê các ảnh chụp sao lưu trên Google Drive
		auth.GET("/recovery-snapshots", func(c *gin.Context) {
			cmds := `/usr/bin/rclone lsf -R --format "pst" gdrive:Backup --config /root/.config/rclone/rclone.conf --fast-list --timeout 15s --contimeout 5s`
			output, err := cachedGet("recovery-snapshots", 30*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu", "detail": err.Error()})
				return
			}

			c.JSON(http.StatusOK, parseSnapshotList(string(output)))
		})

		// API 10: Liệt kê các file backup cục bộ
		auth.GET("/local-snapshots", func(c *gin.Context) {
			cmds := localBackupFindCommand + ` -printf '%TY-%Tm-%Td %TH:%TM:%TS|%s|%p\n' 2>/dev/null`
			output, err := cachedGet("local-snapshots", 30*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu cục bộ", "detail": err.Error()})
				return
			}

			c.JSON(http.StatusOK, parseLocalSnapshots(string(output)))
		})

		// API 11: Tải về snapshot từ Google Drive về trình duyệt (stream qua SSH)
		auth.GET("/download-snapshot", func(c *gin.Context) {
			rel := c.Query("path")
			if ticketPath, exists := c.Get("download_ticket_path"); exists {
				rel = ticketPath.(string)
			}
			if !validSnapshotPath(rel) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Đường dẫn không hợp lệ"})
				return
			}

			session, err := newSSHSession()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể mở phiên SSH", "detail": err.Error()})
				return
			}
			defer session.Close()

			remotePath := "gdrive:Backup/" + rel
			name := filepath.Base(rel)
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
			c.Header("Content-Type", "application/octet-stream")

			rc := http.NewResponseController(c.Writer)
			fw := &flushWriter{w: c.Writer, f: func() { _ = rc.Flush() }}
			session.Stdout = fw

			var errBuf bytes.Buffer
			session.Stderr = &errBuf

			cmd := fmt.Sprintf(`/usr/bin/rclone cat "%s" --config /root/.config/rclone/rclone.conf`, remotePath)
			// Send response headers immediately so the browser shows the download
			// while Drive is still opening the remote file.
			if err := rc.Flush(); err != nil {
				return
			}
			if err := session.Run(cmd); err != nil {
				fmt.Println("Download error:", err, errBuf.String())
			}
		})

		// API alert settings: never return stored credentials.
		auth.GET("/alert-settings", func(c *gin.Context) {
			settings, err := readAlertSettings("alert_settings.json")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình cảnh báo"})
				return
			}
			c.JSON(http.StatusOK, safeAlertSettings(settings))
		})

		auth.POST("/alert-settings", func(c *gin.Context) {
			var incoming AlertSettings
			if err := c.ShouldBindJSON(&incoming); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
				return
			}
			current, err := readAlertSettings("alert_settings.json")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình cảnh báo hiện tại"})
				return
			}
			settings := mergeAlertSecrets(current, incoming)
			if err := validateAlertSettings(settings); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			integrityScript, realtimeScript := buildAlertScripts(settings)

			if _, err := executeSSHCommand(buildAlertInstallCommand(integrityScript, realtimeScript)); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			data, err := json.Marshal(settings)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể mã hóa cấu hình cảnh báo"})
				return
			}
			if err := writeFileAtomic0600("alert_settings.json", data); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu cấu hình cảnh báo"})
				return
			}

			invalidate("cron-jobs", "server-status")
			c.JSON(http.StatusOK, safeAlertSettings(settings))
		})

		auth.POST("/refresh", func(c *gin.Context) {
			cacheMu.Lock()
			backupEntry, hasBackup := cache["backup-status"]
			cache = make(map[string]cacheEntry)
			if hasBackup && len(backupEntry.value) > 0 {
				if stale := markDriveStatusStale(backupEntry.value); len(stale) > 0 {
					backupEntry.value = stale
					backupEntry.expiresAt = time.Time{}
					cache["backup-status"] = backupEntry
				}
			}
			cacheMu.Unlock()
			c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Cache cleared"})
		})

		auth.POST("/clear-log", func(c *gin.Context) {
			var req struct {
				Target  string   `json:"target"`
				Entries []string `json:"entries"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Lỗi tham số"})
				return
			}
			if req.Target != "drive" && req.Target != "server" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Target không hợp lệ"})
				return
			}
			for _, entry := range req.Entries {
				if !activityLogKeyPattern.MatchString(entry) || (req.Target == "drive" && strings.Count(entry, "|") != 1) {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Nhật ký được chọn không hợp lệ"})
					return
				}
			}
			if req.Target == "drive" {
				if len(req.Entries) == 0 {
					_, err := executeSSHCommand("> /var/log/aapanel_backup.log")
					if err != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký", "detail": err.Error()})
						return
					}
					invalidate("backup-status")
					c.JSON(http.StatusOK, gin.H{"message": "Đã xóa toàn bộ nhật ký Google Drive"})
					return
				}
				conditions := make([]string, 0, len(req.Entries))
				for _, entry := range req.Entries {
					parts := strings.SplitN(entry, "|", 2)
					conditions = append(conditions, `$1=="`+parts[0]+`" && $2=="`+parts[1]+`"`)
				}
				filter := "awk -F'|' '!(" + strings.Join(conditions, " || ") + ")' /var/log/aapanel_backup.log > /tmp/backup-monitor-drive-log && mv /tmp/backup-monitor-drive-log /var/log/aapanel_backup.log"
				_, err := executeSSHCommand(filter)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa log", "detail": err.Error()})
					return
				}
				cacheMu.Lock()
				if entry, ok := cache["backup-status"]; ok {
					if stale := markDriveStatusStale(entry.value); len(stale) > 0 {
						entry.value = stale
						entry.expiresAt = time.Time{}
						cache["backup-status"] = entry
					} else {
						delete(cache, "backup-status")
					}
				}
				cacheMu.Unlock()
				c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên Google Drive"})
				return
			}
			out, err := executeSSHCommandRaw(serverActivityClearCommand(req.Entries))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký máy chủ", "detail": err.Error()})
				return
			}
			invalidate("backup-status", "server-status")
			if len(req.Entries) > 0 && strings.Contains(out, "LOG_FILES_CHANGED=0\n") {
				c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy nhật ký đã chọn trên máy chủ. Hãy tải lại danh sách và thử lại."})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên máy chủ"})
		})

	}
	return r
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("load .env: %v", err)
	}
	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	warmDriveStatusCache()
	runtimeConfig = cfg
	initializeBackupSystem()
	router := setupRouter(cfg)
	log.Printf("Backend listening on %s", cfg.APIBindAddr)
	if err := router.Run(cfg.APIBindAddr); err != nil {
		log.Fatalf("start API server: %v", err)
	}
}
