package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ===== SNAPSHOT MODELS & HELPERS =====

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

type downloadTicket struct {
	path      string
	expiresAt time.Time
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

// ===== SNAPSHOT ROUTE HANDLERS =====

func registerSnapshotRoutes(auth *gin.RouterGroup) {
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

	auth.GET("/local-snapshots", func(c *gin.Context) {
		output, err := readLocalInventory()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu cục bộ", "detail": err.Error()})
			return
		}

		c.JSON(http.StatusOK, parseLocalSnapshots(string(output)))
	})

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

		if err := rc.Flush(); err != nil {
			return
		}
		if err := session.Run(cmd); err != nil {
			fmt.Println("Download error:", err, errBuf.String())
		}
	})
}
