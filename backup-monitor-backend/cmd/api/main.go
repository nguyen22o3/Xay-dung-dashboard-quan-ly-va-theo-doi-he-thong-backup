package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// ===== ROUTER SETUP =====

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

	// Đăng ký các nhóm route theo từng phân hệ chức năng
	registerBackupSystemRoutes(auth)
	registerOperationRoutes(auth)
	registerSnapshotRoutes(auth)
	registerCronRoutes(auth)
	registerServerRoutes(auth)
	registerAlertRoutes(auth)

	return r
}

// ===== APPLICATION ENTRYPOINT =====

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

// Drain active downloads and requests before the process exits.
func serveAPI(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
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
