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
			return entry.value, nil
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
			cacheMu.Lock()
			cache[key] = cacheEntry{value: b, expiresAt: time.Now().Add(ttl)}
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
		return entry.value, nil
	}

	b, err := doWork()
	if err != nil && hasEntry && len(entry.value) > 0 {
		return entry.value, nil
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
	TelegramToken  string `json:"telegramToken"`
	TelegramChat   string `json:"telegramChat"`
	DiscordWebhook string `json:"discordWebhook"`
	SmtpEmail      string `json:"smtpEmail"`
	SmtpPassword   string `json:"smtpPassword"`
	TargetEmail    string `json:"targetEmail"`
	Threshold      int    `json:"threshold"`
}

type AlertSettingsResponse struct {
	TelegramChat       string `json:"telegramChat"`
	SmtpEmail          string `json:"smtpEmail"`
	TargetEmail        string `json:"targetEmail"`
	Threshold          int    `json:"threshold"`
	TelegramConfigured bool   `json:"telegramConfigured"`
	DiscordConfigured  bool   `json:"discordConfigured"`
	SmtpConfigured     bool   `json:"smtpConfigured"`
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

	integrityScript := fmt.Sprintf(`#!/bin/bash
set -u
%s
BACKUP_COUNT=$(/usr/bin/rclone lsd gdrive:Backup/ --config /root/.config/rclone/rclone.conf 2>/dev/null | wc -l)
if [ "$BACKUP_COUNT" -lt %d ]; then
    MSG="CANH BAO TOAN VEN DU LIEU! So luong ban sao luu tren Drive hien tai la $BACKUP_COUNT/%d. Vui long kiem tra!"
    if [ -n "$DISCORD_WEBHOOK" ]; then curl -fsS -H "Content-Type: application/json" --data "{\"content\":\"$MSG\"}" "$DISCORD_WEBHOOK" >/dev/null; fi
    if [ -n "$TELEGRAM_TOKEN" ]; then curl -fsS -X POST "https://api.telegram.org/bot${TELEGRAM_TOKEN}/sendMessage" --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$MSG" >/dev/null; fi
    if [ -n "$SMTP_PASSWORD" ]; then printf 'From: %%s\nTo: %%s\nSubject: [ALERT] Backup Monitor\n\n%%s\n' "$SMTP_EMAIL" "$TARGET_EMAIL" "$MSG" | curl -fsS --url 'smtps://smtp.gmail.com:465' --ssl-reqd --mail-from "$SMTP_EMAIL" --mail-rcpt "$TARGET_EMAIL" --user "$SMTP_EMAIL:$SMTP_PASSWORD" -T - >/dev/null; fi
fi`, variables, settings.Threshold, settings.Threshold)

	realtimeScript := fmt.Sprintf(`#!/bin/bash
set -u
%s
exec 9>/run/backup-monitor-inotify.lock
flock -n 9 || exit 0
inotifywait -m -r -e delete --format '%%w%%f' /www/backup/ 2>/dev/null | while IFS= read -r FILE
do
    FILE_DATE=$(echo "$FILE" | grep -oP '20\d{2}-?\d{2}-?\d{2}' | head -1)
    if [ ! -z "$FILE_DATE" ]; then
        NORMALIZED_DATE=$(date -d "${FILE_DATE//-/}" +%%Y-%%m-%%d 2>/dev/null)
        if [ $? -eq 0 ]; then
            FILE_EPOCH=$(date -d "$NORMALIZED_DATE" +%%s)
            TODAY_EPOCH=$(date +%%s)
            DIFF_DAYS=$(( (TODAY_EPOCH - FILE_EPOCH) / 86400 ))
            if [ "$DIFF_DAYS" -ge %d ]; then
                continue
            fi
        fi
    fi

    MSG="BAO DONG KHAN CAP: File backup [$FILE] vua bi XOA khoi may chu! Thoi gian: $(date)"
    if [ -n "$DISCORD_WEBHOOK" ]; then curl -fsS -H "Content-Type: application/json" --data "{\"content\":\"$MSG\"}" "$DISCORD_WEBHOOK" >/dev/null; fi
    if [ -n "$TELEGRAM_TOKEN" ]; then curl -fsS -X POST "https://api.telegram.org/bot${TELEGRAM_TOKEN}/sendMessage" --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$MSG" >/dev/null; fi
    if [ -n "$SMTP_PASSWORD" ]; then printf 'From: %%s\nTo: %%s\nSubject: [URGENT] File Deleted\n\n%%s\n' "$SMTP_EMAIL" "$TARGET_EMAIL" "$MSG" | curl -fsS --url 'smtps://smtp.gmail.com:465' --ssl-reqd --mail-from "$SMTP_EMAIL" --mail-rcpt "$TARGET_EMAIL" --user "$SMTP_EMAIL:$SMTP_PASSWORD" -T - >/dev/null; fi
done`, variables, settings.Threshold)

	return integrityScript, realtimeScript
}

func buildAlertInstallCommand(integrityScript, realtimeScript string) string {
	return fmt.Sprintf(`set -eu
umask 077
command -v inotifywait >/dev/null 2>&1 || { dnf install epel-release -y >/dev/null 2>&1 && dnf install inotify-tools -y >/dev/null 2>&1; }
printf '%%s' '%s' | base64 -d > /root/check_integrity.sh
printf '%%s' '%s' | base64 -d > /root/realtime_monitor.sh
chmod 600 /root/check_integrity.sh /root/realtime_monitor.sh
(crontab -l 2>/dev/null | grep -v 'check_integrity.sh' | grep -v 'realtime_monitor.sh'; printf '%%s\n' '0 12 * * * /bin/bash /root/check_integrity.sh' '@reboot nohup /bin/bash /root/realtime_monitor.sh >/dev/null 2>&1 &') | crontab -
nohup /bin/bash /root/realtime_monitor.sh >/dev/null 2>&1 &`, encodeForShell(integrityScript), encodeForShell(realtimeScript))
}

var runJobSSHCommand = executeSSHCommand

func commandForJob(jobID string) (string, bool) {
	switch jobID {
	case "drive-sync":
		return `SCRIPT_FILE=$(find /www/server/cron -maxdepth 1 -type f ! -name '*.log' -exec grep -IlE 'rclone|gdrive|auto_backup\.sh|/root/backup-monitor' {} + 2>/dev/null | head -n 1)
if [ -n "$SCRIPT_FILE" ]; then
  nohup /bin/bash "$SCRIPT_FILE" >/dev/null 2>&1 &
  printf '{"status":"success","message":"Drive sync triggered"}\n'
else
  printf '{"status":"error","message":"Drive sync script not found"}\n'
fi`, true
	case "site-backup":
		return `SCRIPT_FILE=$(find /www/server/cron -maxdepth 1 -type f ! -name '*.log' -exec grep -IlE 'backup\.py[[:space:]]+site' {} + 2>/dev/null | head -n 1)
if [ -n "$SCRIPT_FILE" ]; then
  nohup /bin/bash "$SCRIPT_FILE" >/dev/null 2>&1 &
  printf '{"status":"success","message":"Site backup triggered"}\n'
else
  printf '{"status":"error","message":"Site backup script not found"}\n'
fi`, true
	case "database-backup":
		return `SCRIPT_FILE=$(find /www/server/cron -maxdepth 1 -type f ! -name '*.log' -exec grep -IlE 'backup\.py[[:space:]]+database' {} + 2>/dev/null | head -n 1)
if [ -n "$SCRIPT_FILE" ]; then
  nohup /bin/bash "$SCRIPT_FILE" >/dev/null 2>&1 &
  printf '{"status":"success","message":"Database backup triggered"}\n'
else
  printf '{"status":"error","message":"Database backup script not found"}\n'
fi`, true
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
WEBSITES=$(find /www/wwwroot -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -v "/default$" | wc -l)
DATABASES=$(find /www/server/data -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -vE "/(mysql|performance_schema|sys|phpmyadmin)$" | wc -l)
CRONJOBS=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -q "acme" "$cmd" 2>/dev/null; then echo 1; fi; done | wc -l)
CRON_TIMES=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -q "acme" "$cmd" 2>/dev/null; then printf "%02d:%02d\n" "$hour" "$min"; fi; done | sort | paste -sd, - | sed 's/,/, /g')

DRIVE_CRONJOBS=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -qE "acme|backup\.py" "$cmd" 2>/dev/null; then echo 1; fi; done | wc -l)
DRIVE_CRON_TIMES=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -qE "acme|backup\.py" "$cmd" 2>/dev/null; then printf "%02d:%02d\n" "$hour" "$min"; fi; done | sort | paste -sd, - | sed 's/,/, /g')

LOCAL_CRONJOBS=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if grep -q "backup\.py" "$cmd" 2>/dev/null; then echo 1; fi; done | wc -l)
LOCAL_CRON_TIMES=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if grep -q "backup\.py" "$cmd" 2>/dev/null; then printf "%02d:%02d\n" "$hour" "$min"; fi; done | sort | paste -sd, - | sed 's/,/, /g')

LOCAL_BACKUP_DIR="/www/backup"
if [ -d "$LOCAL_BACKUP_DIR" ]; then
  # Only count backup artifacts belonging to websites, databases, or aaPanel.
  # Ignore temporary files and unrelated content kept in /www/backup.
  LOCAL_FILES=$(find "$LOCAL_BACKUP_DIR" -type f \( -path "$LOCAL_BACKUP_DIR/site/*" -o -path "$LOCAL_BACKUP_DIR/database/*" -o -path "$LOCAL_BACKUP_DIR/panel/*" \) -printf '%p\n' 2>/dev/null || true)
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

echo "{\"disk\": $DISK, \"ram\": $RAM, \"cpu\": \"$CPU\", \"uptime\": \"$UPTIME\", \"websites\": $WEBSITES, \"databases\": $DATABASES, \"drive_crons\": $DRIVE_CRONJOBS, \"drive_cron_times\": \"$DRIVE_CRON_TIMES\", \"local_crons\": $LOCAL_CRONJOBS, \"local_cron_times\": \"$LOCAL_CRON_TIMES\", \"crons\": $CRONJOBS, \"cron_times\": \"$CRON_TIMES\", \"local_backup\": $LOCAL_BACKUP}"
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
ABOUT=$(timeout -k 5s 10s /usr/bin/rclone about gdrive: --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --json 2>/dev/null)
if [ -n "$ABOUT" ]; then
  echo "$ABOUT" > /tmp/rclone_about.json
elif [ -f /tmp/rclone_about.json ]; then
  ABOUT=$(cat /tmp/rclone_about.json)
else
  ABOUT="{}"
fi
TODAY=$(date +"%Y-%m-%d")

LSF_OUT=$(timeout -k 5s 40s /usr/bin/rclone lsf -R --format "ps" gdrive:Backup --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list 2>&1)
LSF_STATUS=$?
if [ "$LSF_STATUS" -ne 0 ]; then
  printf 'Không thể đọc gdrive:Backup bằng rclone (mã lỗi %s): %s\n' "$LSF_STATUS" "$LSF_OUT" >&2
  exit 1
fi
EVAL_OUT=$(printf '%s\n' "$LSF_OUT" | awk -F';' -v today="$TODAY" '
$2 != "-1" && $2 != "" {
    total_bytes += $2
    total_files++
    split($1, a, "/")
    day = a[1]
    if (day != "") {
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
LOCAL_ACTIVITY="[$LOCAL_LOGS]"

echo "{\"about\": $ABOUT, \"size\": $SIZE, \"dirs\": \"$DIRS\", \"totalFolders\": $TOTAL_FOLDERS, \"history\": $HISTORY, \"activity\": $ACTIVITY, \"localActivity\": $LOCAL_ACTIVITY, \"todayBreakdown\": {\"site\": ${TODAY_SITE:-0}, \"database\": ${TODAY_DB:-0}, \"panel\": ${TODAY_PANEL:-0}}, \"todayBreakdownBytes\": {\"site\": ${TODAY_SITE_BYTES:-0}, \"database\": ${TODAY_DB_BYTES:-0}, \"panel\": ${TODAY_PANEL_BYTES:-0}}}"
`
			output, err := cachedGet("backup-status", 120*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})

			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi đọc Google Drive", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 3: Kích hoạt một loại backup cố định. Client không được gửi lệnh/đường dẫn.
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
				c.JSON(http.StatusInternalServerError, gin.H{"error": "SSH error", "detail": err.Error()})
				return
			}
			invalidate("backup-status", "cron-jobs", "recovery-snapshots")
			c.Data(http.StatusOK, "application/json", []byte(output))
		})

		// API 4: Giám sát Uptime các website (động theo thư mục /www/wwwroot)
		auth.GET("/websites-status", func(c *gin.Context) {
			cmds := `
sites=$(ls -d /www/wwwroot/*/ 2>/dev/null | sed 's#/$##' | xargs -n1 basename 2>/dev/null | grep -v '^default$' | head -n 10)
if [ -z "$sites" ]; then
  sites="web1.local
web2.local
web3.local"
fi
echo "["
first=1
for site in $sites; do
  res=$(curl -o /dev/null -s -w "%{http_code},%{time_total}" --resolve "$site:80:127.0.0.1" --max-time 2 "http://$site" 2>/dev/null)
  if [ -z "$res" ] || [ "$(echo "$res" | cut -d',' -f1)" = "000" ]; then
    res=$(curl -o /dev/null -s -w "%{http_code},%{time_total}" --max-time 2 "http://$site" 2>/dev/null || echo "000,0")
  fi
  code=$(echo "$res" | cut -d',' -f1)
  sec=$(echo "$res" | cut -d',' -f2)
  ms=$(awk -v s="$sec" 'BEGIN {printf "%.0fms", s*1000}')
  status="OFFLINE"
  if [ "$code" = "200" ] || [ "$code" = "301" ] || [ "$code" = "302" ] || [ "$code" = "403" ]; then
    status="ONLINE"
  fi
  if [ "$first" -ne 1 ]; then printf ","; fi
  first=0
  printf '{"name":"%s","status":"%s","code":"%s","time":"%s"}' "$site" "$status" "$code" "$ms"
done
echo "]"
`
			output, err := cachedGet("websites-status", 15*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
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
tail -n 15 /www/server/cron/*.log 2>/dev/null
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

		// API 6: Liệt kê các tiến trình định kỳ (Cron Jobs) với trạng thái thật
		auth.GET("/cron-jobs", func(c *gin.Context) {
			cmds := `
CRONTAB=$(crontab -l 2>/dev/null | grep "/www/server/cron" | grep -v "acme")
echo "["
FIRST=1
printf '%s\n' "$CRONTAB" | while IFS= read -r line; do
  [ -z "$line" ] && continue
  # Tách lịch trình cron (5 trường đầu)
  SCHED=$(echo "$line" | awk '{print $1" "$2" "$3" "$4" "$5}')
  
  # Tìm đường dẫn script (ví dụ: /www/server/cron/a50afd8f7d0e95dc2fd7bbbccfdcccd82)
  SCRIPT=$(echo "$line" | grep -oE '/www/server/cron/[a-zA-Z0-9]+' | head -n 1)
  [ -z "$SCRIPT" ] && continue
  
  # Tên script (chính là chuỗi mã băm)
  HASHNAME=$(basename "$SCRIPT")
  LOG="/www/server/cron/${HASHNAME}.log"
    # Detect common aaPanel cron types
    CONTENT=$(cat "$SCRIPT" 2>/dev/null)
    if echo "$CONTENT" | grep -q "acme_v2.py"; then
        NAME="Gia hạn SSL"
    elif echo "$CONTENT" | grep -q "backup.py database"; then
        NAME="Backup Database"
    elif echo "$CONTENT" | grep -q "backup.py site"; then
        NAME="Backup Site"
    elif echo "$CONTENT" | grep -q "G-Drive" || echo "$CONTENT" | grep -q "rclone" || echo "$CONTENT" | grep -q "gdrive" || echo "$CONTENT" | grep -q "/root/backup-monitor" || echo "$CONTENT" | grep -q "auto_backup.sh"; then
        NAME="Lưu trữ bản backup lên Google Drive"
    else
        NAME="Cron Task ($HASHNAME)"
    fi

  LAST_RUN=""
  STATUS="never"
  if [ -f "$LOG" ]; then
    LAST_RUN=$(stat -c '%y' "$LOG" 2>/dev/null | cut -d'.' -f1)
    if grep -qiE 'error|fail|failed|exception|panic' "$LOG" 2>/dev/null; then
      STATUS="failed"
    elif [ -s "$LOG" ]; then
      STATUS="success"
    fi
  fi
  
  if [ "$FIRST" -ne 1 ]; then printf ","; fi
  FIRST=0
  printf '{"name":"%s","script":"%s","schedule":"%s","last_run":"%s","status":"%s"}' "$NAME" "$SCRIPT" "$SCHED" "$LAST_RUN" "$STATUS"
done
echo "]"
`
			output, err := cachedGet("cron-jobs", 15*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc tiến trình định kỳ", "detail": err.Error()})
				return
			}
			c.Data(http.StatusOK, "application/json", output)
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
			cmds := `
find /www/backup -type f -mtime -14 \( -name "*.tar.gz" -o -name "*.sql" -o -name "*.zip" -o -name "*.gz" \) -printf '%T@|%s|%P\n' 2>/dev/null | sort -rn | head -n 500 | awk -F'|' '
{
  # Convert epoch to date (requires GNU awk or shell date)
  cmd = "date -d @" int($1) " +\"%Y-%m-%d %H:%M:%S\"";
  cmd | getline date_str;
  close(cmd);
  size=$2;
  path=$3;
  # Category is the first dir
  split(path, p, "/");
  if(length(p)>1) { cat=p[1]; name=p[length(p)] } else { cat="root"; name=path }
  printf "%s|%s|%s|%s\n", date_str, cat, name, size
}'
`
			output, err := cachedGet("local-snapshots", 30*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu cục bộ", "detail": err.Error()})
				return
			}

			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			type localSnap struct {
				Date     string `json:"date"`
				Category string `json:"category"`
				Name     string `json:"name"`
				Size     int64  `json:"size"`
			}
			snaps := []localSnap{}
			for _, line := range lines {
				parts := strings.Split(line, "|")
				if len(parts) < 4 {
					continue
				}
				var size int64
				fmt.Sscanf(parts[3], "%d", &size)
				snaps = append(snaps, localSnap{
					Date:     parts[0],
					Category: parts[1],
					Name:     parts[2],
					Size:     size,
				})
			}
			c.JSON(http.StatusOK, snaps)
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

			c.JSON(http.StatusOK, safeAlertSettings(settings))
		})

		auth.POST("/refresh", func(c *gin.Context) {
			cacheMu.Lock()
			cache = make(map[string]cacheEntry)
			cacheMu.Unlock()
			c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Cache cleared"})
		})

		auth.POST("/clear-log", func(c *gin.Context) {
			var req struct {
				Target string `json:"target"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Lỗi tham số"})
				return
			}
			if req.Target != "drive" && req.Target != "server" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Target không hợp lệ"})
				return
			}
			if req.Target == "drive" {
				_, err := executeSSHCommand("> /var/log/aapanel_backup.log")
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa log", "detail": err.Error()})
					return
				}
				cacheMu.Lock()
				delete(cache, "backup-status")
				cacheMu.Unlock()
				c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên Google Drive"})
				return
			}
			c.JSON(http.StatusNotImplemented, gin.H{"error": "Xóa nhật ký máy chủ chưa được hỗ trợ an toàn"})
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

	router := setupRouter(cfg)
	log.Printf("Backend listening on %s", cfg.APIBindAddr)
	if err := router.Run(cfg.APIBindAddr); err != nil {
		log.Fatalf("start API server: %v", err)
	}
}
