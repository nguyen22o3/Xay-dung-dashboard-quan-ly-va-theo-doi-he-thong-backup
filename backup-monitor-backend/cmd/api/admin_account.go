package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

const adminPasswordCost = 12

var errAdminExists = errors.New("administrator already exists")
var adminUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)
var adminHashPattern = regexp.MustCompile(`^\$2[aby]\$(12|13|14)\$[./A-Za-z0-9]{53}$`)

type adminAccount struct {
	Version      int    `json:"version"`
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	CreatedAt    string `json:"createdAt"`
}

// One account per installation, independent of the image.
// A malformed/missing mounted state is never replaced by an env password.
type adminAccountStore struct {
	mu      sync.RWMutex
	path    string
	account *adminAccount
}

func openAdminAccount(path string) (*adminAccountStore, error) {
	s := &adminAccountStore{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("cannot read regular administrator account file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("administrator account file must have permissions 0600")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open administrator account file")
	}
	defer f.Close()
	var a adminAccount
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, errors.New("invalid administrator account file")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid administrator account file")
	}
	cost, hashErr := bcrypt.Cost([]byte(a.PasswordHash))
	_, dateErr := time.Parse(time.RFC3339, a.CreatedAt)
	if a.Version != 1 || strings.TrimSpace(a.Username) == "" || len(a.Username) > 256 || !adminHashPattern.MatchString(a.PasswordHash) || hashErr != nil || cost < adminPasswordCost || cost > 14 || dateErr != nil {
		return nil, errors.New("invalid administrator account metadata/hash")
	}
	s.account = &a
	return s, nil
}

func (s *adminAccountStore) username() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.account == nil {
		return ""
	}
	return s.account.Username
}

func (s *adminAccountStore) verify(username, password string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	a := s.account
	s.mu.RUnlock()
	if a == nil || len(password) > 72 {
		return false
	}
	// Run bcrypt even for an incorrect username to avoid a username timing oracle.
	passwordOK := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) == nil
	return constantTimeEqual(username, a.Username) && passwordOK
}

func (s *adminAccountStore) create(username, password string, legacy bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account != nil {
		return errAdminExists
	}
	if (!legacy && !adminUsernamePattern.MatchString(username)) || strings.TrimSpace(username) == "" || len(username) > 256 {
		return errors.New("Tên đăng nhập cần 3–64 ký tự: chữ, số, dấu chấm, gạch dưới hoặc gạch ngang")
	}
	// bcrypt has a 72-byte limit; do not silently truncate UTF-8 passwords.
	if len(password) < 12 || len(password) > 72 || strings.TrimSpace(password) == "" {
		return errors.New("Mật khẩu cần từ 12 đến 72 byte (ký tự có dấu có thể chiếm nhiều byte)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), adminPasswordCost)
	if err != nil {
		return errors.New("cannot hash administrator password")
	}
	a := adminAccount{Version: 1, Username: username, PasswordHash: string(hash), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".admin-account-*")
	if err != nil {
		return errors.New("cannot create administrator account; check persistent directory permissions")
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist administrator account")
	}
	// Publish a fully written file atomically WITHOUT overwriting. The hard-link
	// also makes simultaneous setup requests from separate processes safe.
	if err := os.Link(temp, s.path); err != nil {
		if os.IsExist(err) {
			// Another instance won setup. Adopt its completed state, not ours.
			if existing, loadErr := openAdminAccount(s.path); loadErr == nil {
				s.account = existing.account
			} else {
				return loadErr
			}
			return errAdminExists
		}
		return errors.New("cannot publish administrator account; persistent filesystem must support hard links")
	}
	s.account = &a
	return nil
}

func initializeAdminAccount() (*adminAccountStore, error) {
	path := strings.TrimSpace(os.Getenv("ADMIN_ACCOUNT_FILE"))
	if path == "" {
		path = "admin_account.json"
	}
	s, err := openAdminAccount(path)
	if err != nil {
		return nil, err
	}
	user, password := os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD")
	if s.username() == "" && (user != "" || password != "") {
		if user == "" || password == "" {
			return nil, errors.New("legacy ADMIN_USER and ADMIN_PASSWORD must both be set, or both absent for first-run setup")
		}
		if err := s.create(user, password, true); err != nil {
			return nil, fmt.Errorf("migrate legacy administrator: %w", err)
		}
	}
	removeLegacy, err := explicitBoolEnv("AUTH_REMOVE_LEGACY_ENV")
	if err != nil {
		return nil, err
	}
	if removeLegacy && s.username() != "" {
		if err := removeLegacyAdminEnv(); err != nil {
			return nil, err
		}
	}
	os.Unsetenv("ADMIN_USER")
	os.Unsetenv("ADMIN_PASSWORD")
	return s, nil
}

// Opt-in credential migration. Preserve other settings and write the account first.
// A failed rewrite stops startup; the saved account still permits a safe retry.
func removeLegacyAdminEnv() error {
	info, err := os.Lstat(".env")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("cannot sanitize regular runtime .env")
	}
	values, err := godotenv.Read(".env")
	if err != nil {
		return errors.New("cannot parse runtime .env for administrator migration")
	}
	_, hasUser := values["ADMIN_USER"]
	_, hasPassword := values["ADMIN_PASSWORD"]
	if !hasUser && !hasPassword {
		return nil
	}
	delete(values, "ADMIN_USER")
	delete(values, "ADMIN_PASSWORD")
	contents, err := godotenv.Marshal(values)
	if err != nil {
		return errors.New("cannot serialize runtime .env")
	}
	f, err := os.CreateTemp(".", ".admin-env-*")
	if err != nil {
		return errors.New("cannot sanitize runtime .env")
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(contents + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil || os.Rename(f.Name(), ".env") != nil {
		return errors.New("cannot replace runtime .env after administrator migration")
	}
	return nil
}

type adminCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func readAdminCredentials(c *gin.Context) (adminCredentials, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	var req adminCredentials
	if err := d.Decode(&req); err != nil {
		return req, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return req, errors.New("unexpected trailing data")
	}
	return req, nil
}

func registerAdminSetup(r *gin.Engine, cfg AppConfig, limiter *loginRateLimiter) {
	r.GET("/api/auth/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"setupRequired": cfg.Account.username() == ""})
	})
	r.POST("/api/auth/setup", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		// CORS alone does not block simple cross-site POSTs. Require the browser's
		// exact configured origin, even when the proxy considers it same-origin.
		allowed := false
		for _, origin := range cfg.CORSAllowOrigins {
			allowed = allowed || c.GetHeader("Origin") == origin
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Hãy tạo tài khoản từ địa chỉ dashboard đã cấu hình"})
			return
		}
		if cfg.Account.username() != "" {
			c.JSON(http.StatusConflict, gin.H{"error": "Quản trị viên đã được tạo. Hãy đăng nhập"})
			return
		}
		key, now := remoteAddressKey(c.Request.RemoteAddr), time.Now()
		if !limiter.allowed(key, now) {
			c.Header("Retry-After", "300")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Thử quá nhiều lần, vui lòng chờ 5 phút"})
			return
		}
		req, err := readAdminCredentials(c)
		if err == nil {
			err = cfg.Account.create(req.Username, req.Password, false)
		}
		if errors.Is(err, errAdminExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "Quản trị viên đã được tạo. Hãy đăng nhập"})
			return
		}
		if err != nil {
			limiter.recordFailure(key, now)
			// No request values/passwords are included in errors or logs.
			c.JSON(http.StatusBadRequest, gin.H{"error": "Không thể tạo tài khoản. Tên đăng nhập: 3–64 chữ/số/._-. Mật khẩu: 12–72 byte. Kiểm tra quyền ghi thư mục dữ liệu nếu vẫn lỗi"})
			return
		}
		limiter.reset(key)
		// Login separately: retrying a lost setup response cannot create a second
		// account or overwrite the completed first one.
		c.JSON(http.StatusCreated, gin.H{"status": "created"})
	})
}
