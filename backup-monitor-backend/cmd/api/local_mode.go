package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"backup-monitor-backend/internal/hostagent"
)

func loadJWTSecret(mode string) (string, error) {
	if value := os.Getenv("JWT_SECRET"); value != "" {
		return value, nil
	}
	if mode != "local-agent" {
		return requiredEnv("JWT_SECRET")
	}
	path := strings.TrimSpace(os.Getenv("JWT_SECRET_FILE"))
	if path == "" {
		path = "/data/jwt-secret"
	}
	if data, err := readPrivateSecret(path); err == nil {
		return string(data), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	key := make([]byte, 48)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(key)
	// A hard-link publishes only a completed key, never overwrites another
	// instance's persisted key and does not expose a partial secret at startup.
	f, err := os.CreateTemp(filepath.Dir(path), ".jwt-secret-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.WriteString(secret)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	data, err := readPrivateSecret(path)
	return string(data), err
}
func readPrivateSecret(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil, errors.New("unsafe persisted JWT secret")
	}
	data, err := os.ReadFile(path)
	if err == nil && len(data) < 32 {
		return nil, errors.New("invalid persisted JWT secret")
	}
	return data, err
}
func loadLocalAgent() (*hostagent.Client, error) {
	socket := strings.TrimSpace(os.Getenv("HOST_AGENT_SOCKET"))
	if socket == "" {
		socket = "/run/host-agent/agent.sock"
	}
	tokenPath := strings.TrimSpace(os.Getenv("HOST_AGENT_TOKEN_FILE"))
	if tokenPath == "" {
		tokenPath = "/run/host-agent-secrets/agent-token"
	}
	info, err := os.Lstat(tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 || info.Size() > 4096 {
		return nil, errors.New("invalid local agent credential file")
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, errors.New("cannot read local agent credential")
	}
	return hostagent.NewClient(socket, strings.TrimSpace(string(data)))
}

// Installer creates the first account before opening the LAN listener. Neither
// passwords nor their hashes are passed via argv, environment or logs.
func adminInstallCommand(operation string) error {
	account, err := initializeAdminAccount()
	if err != nil {
		return err
	}
	if operation == "account-status" {
		if account.username() == "" {
			fmt.Println("empty")
		} else {
			fmt.Println("exists")
		}
		return nil
	}
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 4096))
	username, err := reader.ReadString('\n')
	if err != nil {
		return errors.New("username required on stdin")
	}
	password, err := reader.ReadString('\n')
	if err != nil {
		return errors.New("password required on stdin")
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return errors.New("unexpected administrator input")
	}
	if err := account.create(strings.TrimSuffix(username, "\n"), strings.TrimSuffix(password, "\n"), false); err != nil {
		return errors.New("cannot create administrator: username 3-64 characters, password 12-72 bytes; account must not exist")
	}
	fmt.Println("Administrator created")
	return nil
}
