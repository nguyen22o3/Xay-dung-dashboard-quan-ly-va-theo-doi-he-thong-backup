package main

import (
	"bytes"
	"context"
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
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"backup-monitor-backend/internal/hostagent"

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
	Account          *adminAccountStore
	JWTSecret        []byte
	APIBindAddr      string
	CORSAllowOrigins []string
	SSH              SSHSettings
	HostAgent        *hostagent.Client
	HostIdentity     string
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
	LocalPath string `json:"localPath"`
	Date      string `json:"date"`
	Category  string `json:"category"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
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
	snapshots := parseLocalInventory(output)
	if len(snapshots) > 500 {
		return snapshots[:500]
	}
	return snapshots
}

func parseLocalInventory(output string) []localSnapshot {
	snapshots := make([]localSnapshot, 0)
	seen := map[string]bool{}
	roots := configuredLocalBackupRoots()
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 || seen[parts[2]] {
			continue
		}
		prefix := ""
		for _, root := range roots {
			if strings.HasPrefix(parts[2], root+"/") {
				prefix = root + "/"
				break
			}
		}
		if prefix == "" {
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
			LocalPath: parts[2],
			Date:      backupTimestamp(name, modified), Category: category,
			Name: name, Size: size, Modified: modified,
		})
		seen[parts[2]] = true
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Date != snapshots[j].Date {
			return snapshots[i].Date > snapshots[j].Date
		}
		return snapshots[i].Modified > snapshots[j].Modified
	})
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
	mode := strings.TrimSpace(os.Getenv("HOST_CONNECTION_MODE"))
	if mode == "" {
		mode = "ssh"
	}
	if mode != "ssh" && mode != "local-agent" {
		return AppConfig{}, fmt.Errorf("unsupported HOST_CONNECTION_MODE")
	}
	jwtSecret, err := loadJWTSecret(mode)
	if err != nil {
		return AppConfig{}, err
	}
	if len(jwtSecret) < 32 {
		return AppConfig{}, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}

	var sshSettings SSHSettings
	var agent *hostagent.Client
	identity := ""
	if mode == "local-agent" {
		agent, err = loadLocalAgent()
		identity = "local-agent"
	} else {
		sshSettings, err = loadSSHSettingsFromEnv()
		identity = sshSettings.Host
	}
	if err != nil {
		return AppConfig{}, err
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
	cfg := AppConfig{JWTSecret: []byte(jwtSecret), APIBindAddr: bindAddr, CORSAllowOrigins: corsOrigins,
		SSH: sshSettings, HostAgent: agent, HostIdentity: identity}
	cfg.Account, err = initializeAdminAccount()
	if err != nil {
		return AppConfig{}, err
	}
	return cfg, nil
}

func loadSSHSettingsFromEnv() (SSHSettings, error) {
	sshHost, err := requiredEnv("SSH_HOST")
	if err != nil {
		return SSHSettings{}, err
	}
	if _, _, err := net.SplitHostPort(sshHost); err != nil {
		return SSHSettings{}, fmt.Errorf("SSH_HOST must use host:port format: %w", err)
	}
	sshUser, err := requiredEnv("SSH_USER")
	if err != nil {
		return SSHSettings{}, err
	}
	allowRoot, err := explicitBoolEnv("ALLOW_ROOT_SSH")
	if err != nil {
		return SSHSettings{}, err
	}
	if sshUser == "root" && !allowRoot {
		return SSHSettings{}, fmt.Errorf("SSH_USER=root requires explicit ALLOW_ROOT_SSH=true")
	}

	insecureHostKey, err := explicitBoolEnv("SSH_INSECURE_IGNORE_HOST_KEY")
	if err != nil {
		return SSHSettings{}, err
	}
	knownHostsPath := strings.TrimSpace(os.Getenv("SSH_KNOWN_HOSTS"))
	if !insecureHostKey && knownHostsPath == "" {
		return SSHSettings{}, fmt.Errorf("SSH_KNOWN_HOSTS is required unless SSH_INSECURE_IGNORE_HOST_KEY=true")
	}

	sshPassword := os.Getenv("SSH_PASSWORD")
	privateKeyPath := strings.TrimSpace(os.Getenv("SSH_PRIVATE_KEY_PATH"))
	if sshPassword == "" && privateKeyPath == "" {
		return SSHSettings{}, fmt.Errorf("set SSH_PASSWORD or SSH_PRIVATE_KEY_PATH")
	}
	settings := SSHSettings{
		Host:                 sshHost,
		User:                 sshUser,
		Password:             sshPassword,
		PrivateKeyPath:       privateKeyPath,
		PrivateKeyPassphrase: os.Getenv("SSH_PRIVATE_KEY_PASSPHRASE"),
		KnownHostsPath:       knownHostsPath,
		InsecureHostKey:      insecureHostKey,
	}
	if _, err := buildSSHClientConfig(settings); err != nil {
		return SSHSettings{}, fmt.Errorf("invalid SSH configuration: %w", err)
	}
	return settings, nil
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
	return cachedRead(key, ttl, producer, ttlForValue, staleValue, true)
}

// Short local reads wait for one shared refresh instead of returning yesterday's
// inventory for another frontend polling cycle. Errors still keep the last value.
func cachedGetFresh(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	return cachedRead(key, ttl, producer, nil, nil, false)
}

func cachedRead(key string, ttl time.Duration, producer func() ([]byte, error), ttlForValue func([]byte) time.Duration, staleValue func([]byte) []byte, allowStale bool) ([]byte, error) {
	showStale := func(value []byte) []byte {
		if staleValue != nil {
			if marked := staleValue(value); len(marked) > 0 {
				return marked
			}
		}
		return value
	}
	flightMu.Lock()
	cacheMu.Lock()
	entry, hasEntry := cache[key]
	isFresh := hasEntry && time.Now().Before(entry.expiresAt)
	cacheMu.Unlock()

	if isFresh {
		flightMu.Unlock()
		return entry.value, nil
	}

	call, isRunning := inFlight[key]
	if isRunning {
		flightMu.Unlock()
		if allowStale && hasEntry && len(entry.value) > 0 {
			return showStale(entry.value), nil
		}
		<-call.done
		if call.err != nil && hasEntry && len(entry.value) > 0 {
			return showStale(entry.value), nil
		}
		return call.val, call.err
	}

	call = &flightCall{done: make(chan struct{})}
	inFlight[key] = call
	flightMu.Unlock()

	doWork := func() ([]byte, error) {
		b, err := producer()
		flightMu.Lock()
		current := inFlight[key] == call
		if err == nil && current {
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

		if current {
			delete(inFlight, key)
		}
		close(call.done)
		flightMu.Unlock()

		return b, err
	}

	// Nếu đã có cache cũ, trả về luôn và làm mới ngầm (Stale-While-Revalidate)
	if allowStale && hasEntry && len(entry.value) > 0 {
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
	// Keep callers independent of the shared internal read-cache layout.
	for _, key := range keys {
		switch key {
		case "backup-status":
			keys = append(keys, "drive-inventory", "backup-activity")
		case "recovery-snapshots":
			keys = append(keys, "drive-inventory")
		case "local-snapshots":
			keys = append(keys, "server-status")
		}
	}
	flightMu.Lock()
	cacheMu.Lock()
	for _, k := range keys {
		delete(cache, k)
		// Old reads may finish later, but cannot refill the new configuration's cache.
		delete(inFlight, k)
	}
	cacheMu.Unlock()
	flightMu.Unlock()
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

	// The actual NewSession below checks the pooled connection and retries once.
	if sshClient != nil {
		return sshClient, nil
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

	// A stale pooled connection gets one reconnect without a separate probe session.
	sshClientMu.Lock()
	if sshClient == client {
		sshClient = nil
	}
	sshClientMu.Unlock()
	_ = client.Close()
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
	return executeSSHCommandInputRaw(command, nil)
}

func executeSSHCommandInputRaw(command string, input []byte) (string, error) {
	return executeSSHCommandInputRawTimeout(command, input, 60*time.Second)
}

func executeSSHCommandInputRawTimeout(command string, input []byte, timeout time.Duration) (string, error) {
	command = configuredRuntimeCommand(command)
	if runtimeConfig.HostAgent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
		defer cancel()
		return runtimeConfig.HostAgent.Execute(ctx, command, input)
	}
	session, err := newSSHSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf
	if input != nil {
		session.Stdin = bytes.NewReader(input)
	}

	type runResult struct {
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		done <- runResult{err: session.Run(command)}
	}()

	select {
	case <-time.After(timeout):
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("SSH command timed out after %s; reload state before retrying", timeout)
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
	if p == "" || strings.Contains(p, "..") || !snapshotPathRe.MatchString(p) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." {
			return false
		}
	}
	return true
}

// Alert settings are stored locally. Secret values are accepted on writes but are
// never serialized in API responses.
type AlertSettings struct {
	TelegramToken   string   `json:"telegramToken"`
	TelegramChat    string   `json:"telegramChat"`
	DiscordWebhook  string   `json:"discordWebhook"`
	SmtpEmail       string   `json:"smtpEmail"`
	SmtpPassword    string   `json:"smtpPassword"`
	TargetEmail     string   `json:"targetEmail"`
	Threshold       int      `json:"threshold"`
	MonitorPaths    []string `json:"monitorPaths,omitempty"`
	TelegramEnabled *bool    `json:"telegramEnabled,omitempty"`
	DiscordEnabled  *bool    `json:"discordEnabled,omitempty"`
	SmtpEnabled     *bool    `json:"smtpEnabled,omitempty"`
}

type AlertSettingsResponse struct {
	TelegramChat       string   `json:"telegramChat"`
	SmtpEmail          string   `json:"smtpEmail"`
	TargetEmail        string   `json:"targetEmail"`
	Threshold          int      `json:"threshold"`
	MonitorPaths       []string `json:"monitorPaths"`
	TelegramConfigured bool     `json:"telegramConfigured"`
	DiscordConfigured  bool     `json:"discordConfigured"`
	SmtpConfigured     bool     `json:"smtpConfigured"`
	TelegramEnabled    bool     `json:"telegramEnabled"`
	DiscordEnabled     bool     `json:"discordEnabled"`
	SmtpEnabled        bool     `json:"smtpEnabled"`
}

func alertChannelEnabled(value *bool, configured bool) bool {
	if value == nil {
		return configured
	}
	return *value && configured
}

func effectiveMonitorPaths(settings AlertSettings) []string {
	if settings.MonitorPaths != nil {
		return append([]string{}, settings.MonitorPaths...)
	}
	return configuredLocalBackupRoots()
}

var alertMonitorPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

func validateAlertMonitorPaths(paths []string) error {
	return validateAlertMonitorPathsWithin(paths, configuredLocalBackupRoots())
}

func validateAlertMonitorPathsWithin(paths, allowed []string) error {
	if len(paths) == 0 || len(paths) > 8 {
		return fmt.Errorf("chọn từ 1 đến 8 thư mục backup để giám sát")
	}
	seen := map[string]bool{}
	for _, selected := range paths {
		if len(selected) > 256 || selected == "/" || !alertMonitorPathRe.MatchString(selected) || path.Clean(selected) != selected {
			return fmt.Errorf("đường dẫn giám sát không hợp lệ: %q", selected)
		}
		permitted := false
		for _, root := range allowed {
			if selected == root || strings.HasPrefix(selected, strings.TrimRight(root, "/")+"/") {
				permitted = true
				break
			}
		}
		if !permitted {
			return fmt.Errorf("thư mục giám sát phải nằm trong nơi lưu backup đã cấu hình: %q", selected)
		}
		if seen[selected] {
			return fmt.Errorf("thư mục giám sát bị chọn trùng: %q", selected)
		}
		seen[selected] = true
	}
	return nil
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
		MonitorPaths:       effectiveMonitorPaths(settings),
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
	if err := validateAlertMonitorPaths(effectiveMonitorPaths(settings)); err != nil {
		return err
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
	if incoming.MonitorPaths == nil {
		incoming.MonitorPaths = current.MonitorPaths
	}
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
	return buildAlertScriptsForRoots(settings, effectiveMonitorPaths(settings))
}

func buildAlertScriptsForRoots(settings AlertSettings, roots []string) (string, string) {
	quotedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimRight(root, "/")
		if root != "" {
			quotedRoots = append(quotedRoots, activityShellQuote(root))
		}
	}
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
WATCH_ROOT_CANDIDATES=(%s)
WATCH_ROOTS=()
for ROOT in "${WATCH_ROOT_CANDIDATES[@]}"; do
    [[ -d "$ROOT" && ! -L "$ROOT" ]] && WATCH_ROOTS+=("$ROOT")
done
if (( ${#WATCH_ROOTS[@]} == 0 )); then
    echo 'No configured backup directory is available to monitor' >&2
    exit 1
fi
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
inotifywait -m -r -e delete --format '%%w%%f' "${WATCH_ROOTS[@]}" 2>/dev/null | while IFS= read -r FILE
do
    if [[ ! "$FILE" =~ \.(zip|tar\.gz|gz|sql)$ ]]; then
        continue
    fi

    RELATIVE_FILE=''
    for ROOT in "${WATCH_ROOTS[@]}"; do
        if [[ "$FILE" == "$ROOT"/* ]]; then
            RELATIVE_FILE="${FILE#"$ROOT"/}"
            break
        fi
    done
    [[ -n "$RELATIVE_FILE" ]] || continue

    # aaPanel removes SQL work files after packing its dated ZIP.
    if [[ "$RELATIVE_FILE" =~ ^panel/[0-9]{4}-[0-9]{2}-[0-9]{2}/data/ ]]; then
        continue
    fi

    # Script-controlled same-day replacement or aaPanel's count-based rotation.
    if is_script_rotation "$FILE" || is_expected_rotation "$FILE" %d; then
        continue
    fi

    if [[ "$RELATIVE_FILE" == "panel/$(date +%%F).zip" ]]; then
        check_panel_zip_replacement "$FILE" "$(date +%%s)" </dev/null >/dev/null 2>&1 &
        continue
    fi

    send_delete_alert "$FILE"
 done`, variables, rotationDecisionShell, strings.Join(quotedRoots, " "), localRetentionCopies)

	return configuredBackupCommand(integrityScript), realtimeScript
}

func buildAlertInstallCommand(integrityScript, realtimeScript string, monitorPaths []string) string {
	quotedPaths := make([]string, 0, len(monitorPaths))
	for _, selected := range monitorPaths {
		quotedPaths = append(quotedPaths, activityShellQuote(selected))
	}
	return fmt.Sprintf(`set -eu
umask 077
command -v inotifywait >/dev/null 2>&1 || { dnf install epel-release -y >/dev/null 2>&1 && dnf install inotify-tools -y >/dev/null 2>&1; }
MONITOR_PATHS=(%s)
for ROOT in "${MONITOR_PATHS[@]}"; do
  [ -d "$ROOT" ] && [ ! -L "$ROOT" ] && [ "$(realpath -e -- "$ROOT")" = "$ROOT" ] || { printf 'Invalid or missing monitor directory: %%s\n' "$ROOT" >&2; exit 1; }
done
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
bash -n "$INTEGRITY_TMP" "$REALTIME_TMP"
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
OLD_MONITORS=$(pgrep -f '%s' || true)
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
`, strings.Join(quotedPaths, " "), encodeForShell(integrityScript), encodeForShell(realtimeScript), "^(/bin/)?bash ("+regexp.QuoteMeta(configuredScript("realtime_monitor.sh"))+"|/root/(scripts/)?realtime_monitor[.]sh)$")
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

	// Container readiness only; no credentials or remote server details.
	r.GET("/api/health", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	registerAdminSetup(r, cfg, loginLimiter)

	// LOGIN - không cần auth
	r.POST("/api/login", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		clientKey := remoteAddressKey(c.Request.RemoteAddr)
		now := time.Now()
		if !loginLimiter.allowed(clientKey, now) {
			c.Header("Retry-After", "300")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Đăng nhập thất bại quá nhiều lần, vui lòng thử lại sau"})
			return
		}
		req, err := readAdminCredentials(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		if !cfg.Account.verify(req.Username, req.Password) {
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
	auth.Use(authMiddleware(cfg.JWTSecret, cfg.Account))
	auth.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/api/backup-system" {
			c.Next() // The handler accepts remote configuration under its write lock.
			return
		}
		if c.Request.Method == http.MethodPost {
			switch c.Request.URL.Path {
			case "/api/backup-system/apply", "/api/backup-system/scripts/save", "/api/backup-system/drive/connect":
				// These handlers take the write lock themselves. Holding the read
				// lock here would deadlock configuration writes.
				c.Next()
				return
			}
		}
		backupSystemMutation.RLock()
		if c.Request.Method == http.MethodGet && storageReadRoute(c.Request.URL.Path) && backupSystemReadDue(time.Now()) {
			backupSystemMutation.RUnlock()
			backupSystemMutation.Lock()
			err := refreshBackupSystemIfDue(func() ([]byte, error) { return backupSystemOperation("get", nil) }, time.Now())
			backupSystemMutation.Unlock()
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "Chưa xác nhận được nơi lưu hiện tại từ máy chủ; hãy thử lại"})
				return
			}
			backupSystemMutation.RLock()
		}
		defer backupSystemMutation.RUnlock()
		if c.Request.Method != http.MethodGet && !strings.HasPrefix(c.Request.URL.Path, "/api/backup-system/") && !backupSystemLoaded.Load() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Chưa tải được cấu hình sao lưu từ máy chủ; hãy tải lại mục Hệ thống sao lưu trước khi thực hiện thay đổi"})
			return
		}
		c.Next()
	})
	registerBackupSystemRoutes(auth)
	registerOperationRoutes(auth)
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
			output, err := readServerStatus()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lấy thông số máy chủ", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 2: Kiểm tra các file Backup trên Google Drive và Dung lượng
		auth.GET("/backup-status", func(c *gin.Context) {
			output, err := readBackupStatus()

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
				if strings.Split(req.JobID, "~")[0] == "cleanup-panel" {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Dọn dẹp aaPanel chưa hoàn tất; hãy kiểm tra log cronjob"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy cronjob"})
				return
			}
			if strings.Split(req.JobID, "~")[0] == "cleanup-panel" {
				deleted, ok := parsePanelCleanupResult(output)
				if !ok {
					releaseReservation()
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả dọn dẹp aaPanel"})
					return
				}
				invalidate("cron-jobs", "server-status", "local-snapshots", "backup-activity")
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
			if logType == "backup" {
				cmd = strings.Replace(cmd, "tail -n 15 /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/cleanup-panel-backups.log /root/check_integrity.log", "tail -n 15 "+managedLogArguments(), 1)
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
			output, err := cachedGetFresh("cron-jobs", 3*time.Second, func() ([]byte, error) {
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
					c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
				case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
					c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy làm mới danh sách và thử lại"})
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu lịch chạy cronjob"})
				}
				return
			}
			if !strings.HasPrefix(strings.TrimSpace(output), "UPDATED|"+remoteInstallation().ConfigDir+"/cron-recovery/schedule-"+task.ID+".") {
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
					c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
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
			if !ok || !strings.HasPrefix(backupPath, remoteInstallation().ConfigDir+"/cron-recovery/delete-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
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
					c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
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
			if !ok || !strings.HasPrefix(backupPath, remoteInstallation().ConfigDir+"/cron-recovery/"+operation+"-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
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
			inventory, err := readDriveInventory()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu", "detail": err.Error()})
				return
			}

			if !inventory.HasListing {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu", "detail": inventory.driveError()})
				return
			}
			c.JSON(http.StatusOK, parseSnapshotList(inventory.Listing))
		})

		// API 10: Liệt kê các file backup cục bộ
		auth.GET("/local-snapshots", func(c *gin.Context) {
			output, err := readLocalInventory()
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
			cmd, err := snapshotDownloadCommand(rel)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Đường dẫn không hợp lệ"})
				return
			}
			if runtimeConfig.HostAgent != nil {
				c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(rel)))
				c.Header("Content-Type", "application/octet-stream")
				rc := http.NewResponseController(c.Writer)
				fw := &flushWriter{w: c.Writer, f: func() { _ = rc.Flush() }}
				if err := runtimeConfig.HostAgent.Stream(c.Request.Context(), cmd, fw); err != nil {
					if !c.Writer.Written() {
						c.JSON(http.StatusBadGateway, gin.H{"error": "Không tải được bản sao lưu"})
					} else {
						c.Abort()
					}
				}
				return
			}

			session, err := newSSHSession()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể mở phiên SSH", "detail": err.Error()})
				return
			}
			defer session.Close()

			name := filepath.Base(rel)
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
			c.Header("Content-Type", "application/octet-stream")

			rc := http.NewResponseController(c.Writer)
			fw := &flushWriter{w: c.Writer, f: func() { _ = rc.Flush() }}
			session.Stdout = fw

			var errBuf bytes.Buffer
			session.Stderr = &errBuf

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

			if _, err := executeSSHCommand(buildAlertInstallCommand(integrityScript, realtimeScript, effectiveMonitorPaths(settings))); err != nil {
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
			refreshReadCaches()
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
				out, err := executeSSHCommandRaw(driveActivityClearCommand(req.Entries))
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký Drive; nếu cron đang chạy, hãy chờ tác vụ kết thúc", "detail": err.Error()})
					return
				}
				invalidate("backup-status")
				if len(req.Entries) > 0 && strings.Contains(out, "LOG_FILES_CHANGED=0\n") {
					c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy nhật ký đã chọn. Hãy tải lại danh sách và thử lại."})
					return
				}
				c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký upload Google Drive trên máy chủ; các tệp backup được giữ nguyên"})
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
	if len(os.Args) == 2 && os.Args[1] == "check-host" {
		cfg, err := loadConfigFromEnv()
		if err != nil {
			log.Fatal(err)
		}
		if cfg.HostAgent == nil {
			log.Fatal("check-host requires local-agent mode")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := cfg.HostAgent.Execute(ctx, "'/usr/local/libexec/backup-monitor/backup-manager' version", nil); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Local host agent connected")
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "account-status" || os.Args[1] == "setup-admin") {
		if err := adminInstallCommand(os.Args[1]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) != 1 {
		log.Fatal("API accepts no arguments; retired collector commands are not supported")
	}
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
	apiCtx, cancelAPI := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelAPI()
	if err := initializeRestoreState(); err != nil {
		log.Fatalf("Restore JSON state: %v", err)
	}
	defer activeRestores.lock.Close()
	router := setupRouter(cfg)
	log.Printf("Backend listening on %s", cfg.APIBindAddr)
	if err := serveAPI(apiCtx, cfg.APIBindAddr, router); err != nil {
		log.Fatalf("start API server: %v", err)
	}
}
