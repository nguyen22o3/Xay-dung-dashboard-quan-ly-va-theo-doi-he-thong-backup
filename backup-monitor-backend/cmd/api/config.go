package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"backup-monitor-backend/internal/hostagent"
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
	SMTPEmail        string
	SMTPPassword     string
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
	smtpEmail, smtpPassword, err := loadSMTPSenderFromEnv()
	if err != nil {
		return AppConfig{}, err
	}
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
		SSH: sshSettings, HostAgent: agent, HostIdentity: identity, SMTPEmail: smtpEmail, SMTPPassword: smtpPassword}
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
