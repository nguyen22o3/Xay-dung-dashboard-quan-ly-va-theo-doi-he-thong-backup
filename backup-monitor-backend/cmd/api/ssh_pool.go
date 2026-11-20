package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

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
