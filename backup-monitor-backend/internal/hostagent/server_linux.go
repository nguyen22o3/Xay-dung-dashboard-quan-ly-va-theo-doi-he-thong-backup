package hostagent

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"backup-monitor-backend/internal/backupops"
)

type boundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > MaxOutput-b.Len() {
		b.overflow = true
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}
func shellCommand(ctx context.Context, req Request) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", req.Command)
	cmd.Dir = "/root"
	cmd.Env = []string{"HOME=/root", "USER=root", "LOGNAME=root", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	cmd.Stdin = bytes.NewReader(req.Input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func agentHandler(token string) http.Handler {
	sem := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || (r.URL.Path != "/execute" && r.URL.Path != "/stream") {
			http.NotFound(w, r)
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxRequest)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var req Request
		if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.Command == "" || strings.ContainsRune(req.Command, 0) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		timeout := 60 * time.Second
		if req.Command == "'/usr/local/libexec/backup-monitor/backup-manager' config" {
			timeout = 10 * time.Minute
		}
		if r.URL.Path == "/stream" {
			timeout = 30 * time.Minute
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		cmd := shellCommand(ctx, req)
		// Never return/log credential-bearing shell stderr or command text.
		cmd.Stderr = io.Discard
		if r.URL.Path == "/stream" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Trailer", "X-Agent-Result")
			cmd.Stdout = w
			if cmd.Run() != nil {
				w.Header().Set("X-Agent-Result", "failed")
			} else {
				w.Header().Set("X-Agent-Result", "ok")
			}
			return
		}
		var out boundedOutput
		cmd.Stdout = &out
		err := cmd.Run()
		reply := Response{Output: out.String()}
		if err != nil || out.overflow {
			reply.Output = ""
			reply.Error = "Lệnh trên máy chủ không hoàn tất; kiểm tra công cụ và quyền truy cập"
			if ctx.Err() != nil {
				reply.Error = "Lệnh trên máy chủ quá thời gian cho phép"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	})
}

// Serve deliberately accepts only Unix sockets. Possession of the socket and
// token grants the API the same host-admin authority as the legacy root SSH.
func Serve(ctx context.Context, socket, tokenFile string, gid int) error {
	if os.Geteuid() != 0 || !filepath.IsAbs(socket) || gid < 1 {
		return errors.New("agent requires root and an absolute Unix socket")
	}
	if err := backupops.SafeParents(socket); err != nil {
		return errors.New("unsafe agent socket path")
	}
	if err := backupops.SafeParents(tokenFile); err != nil {
		return errors.New("unsafe agent credential path")
	}
	dir, err := os.Lstat(filepath.Dir(socket))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 || dir.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe agent socket directory")
	}
	if stat, ok := dir.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
		return errors.New("agent directory must be root owned")
	}
	key, err := os.Lstat(tokenFile)
	if err != nil || !key.Mode().IsRegular() || key.Mode().Perm()&0027 != 0 || key.Size() > 4096 {
		return errors.New("unsafe agent credential permissions")
	}
	if stat, ok := key.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
		return errors.New("agent credential must be root owned")
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return errors.New("cannot read agent credential")
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 43 || strings.ContainsAny(token, "\r\n") {
		return errors.New("invalid agent credential")
	}
	if existing, err := os.Lstat(socket); err == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace a non-socket path")
		}
		// Never unlink a live service's socket.
		conn, dialErr := net.DialTimeout("unix", socket, time.Second)
		if dialErr == nil {
			conn.Close()
			return errors.New("agent is already running")
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chown(socket, 0, gid); err != nil {
		return err
	}
	if err := os.Chmod(socket, 0660); err != nil {
		return err
	}
	server := &http.Server{Handler: agentHandler(token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Println("backup-monitor host agent listening on private Unix socket")
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
