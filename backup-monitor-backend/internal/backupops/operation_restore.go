package backupops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type RestoreRequest struct {
	Path  string `json:"path"`
	Token string `json:"token"`
}
type RestorePlan struct {
	Progress      *TaskProgress `json:"progress,omitempty"`
	Token         string        `json:"token"`
	Path          string        `json:"path"`
	Size          int64         `json:"size"`
	Modified      string        `json:"modified"`
	Version       string        `json:"version"`
	CreatedAt     time.Time     `json:"createdAt"`
	Directory     string        `json:"directory"`
	Status        string        `json:"status"`
	Error         string        `json:"error,omitempty"`
	Files         int           `json:"files"`
	ExpandedBytes int64         `json:"expandedBytes"`
}

var restoreTokenRe = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validRestorePath(value string) bool {
	if len(value) > 2048 || strings.ContainsAny(value, "\\\x00\r\n") || strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.Contains(value, "..") || !regexp.MustCompile(`^[A-Za-z0-9._/-]+$`).MatchString(value) {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) < 3 {
		return false
	}
	day, err := time.Parse("2006-01-02", parts[0])
	if err != nil || day.Format("2006-01-02") != parts[0] {
		return false
	}
	switch parts[1] {
	case "site":
		return strings.HasSuffix(value, ".tar.gz")
	case "database":
		return strings.HasSuffix(value, ".sql.gz")
	case "panel":
		return strings.HasSuffix(value, ".zip")
	}
	return false
}
func (c *Controller) restoreStatePath(token string) (string, error) {
	if !restoreTokenRe.MatchString(token) {
		return "", errors.New("Mã khôi phục không hợp lệ")
	}
	root := filepath.Join(c.RecoveryRoot, "restores")
	if err := SafeParents(root); err != nil {
		return "", err
	}
	return filepath.Join(root, token, "state.json"), nil
}
func (c *Controller) restoreMetadata(cfg Settings, relative string) (int64, string, error) {
	if !validRestorePath(relative) {
		return 0, "", errors.New("Chỉ khôi phục tệp backup trong thư mục ngày/site, database hoặc panel")
	}
	raw, err := c.Run(c.rcloneArgs("lsjson", cfg.DriveRemote+":"+cfg.DriveFolder+"/"+relative, "--stat"), nil)
	if err != nil {
		return 0, "", errors.New("Không xác minh được tệp backup trên Drive")
	}
	var info struct {
		Size    int64
		ModTime string
		IsDir   bool
	}
	if json.Unmarshal(raw, &info) != nil || info.IsDir || info.Size <= 0 || info.ModTime == "" {
		return 0, "", errors.New("Thông tin backup không hợp lệ")
	}
	return info.Size, info.ModTime, nil
}
func (c *Controller) RestorePreview(r Request) (RestorePlan, error) {
	var result RestorePlan
	if r.Restore == nil {
		return result, errors.New("Thiếu bản sao lưu")
	}
	cfg, version, err := c.Active()
	if err != nil {
		return result, err
	}
	size, modified, err := c.restoreMetadata(cfg, r.Restore.Path)
	if err != nil {
		return result, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, err
	}
	token := hex.EncodeToString(random[:])
	statePath, err := c.restoreStatePath(token)
	if err != nil {
		return result, err
	}
	if err = mkdirPrivate(filepath.Dir(statePath)); err != nil {
		return result, err
	}
	result = RestorePlan{Token: token, Path: r.Restore.Path, Size: size, Modified: modified, Version: version, CreatedAt: time.Now().UTC(), Directory: filepath.Join(filepath.Dir(statePath), "data"), Status: "preview"}
	raw, _ := json.Marshal(result)
	err = writeScriptExclusive(statePath, raw)
	return result, err
}
func (c *Controller) RestoreStatus(r Request) (RestorePlan, error) {
	var result RestorePlan
	if r.Restore == nil {
		return result, errors.New("Thiếu yêu cầu khôi phục")
	}
	path, err := c.restoreStatePath(r.Restore.Token)
	if err != nil {
		return result, err
	}
	raw, err := readRegular(path)
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, &result) != nil {
		return result, errors.New("Không đọc được trạng thái khôi phục")
	}
	info, err := nearest(path)
	if err != nil || !privateRootOwned(info) || result.Token != r.Restore.Token || result.Directory != filepath.Join(filepath.Dir(path), "data") || !validRestorePath(result.Path) {
		return result, errors.New("Trạng thái khôi phục không an toàn")
	}
	return result, nil
}
func (c *Controller) RestoreStart(r Request) (RestorePlan, error) {
	result, err := c.RestoreStatus(r)
	if err != nil {
		return result, err
	}
	statePath, _ := c.restoreStatePath(result.Token)
	lock, err := acquire(filepath.Join(filepath.Dir(statePath), ".lock"), false, true)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	result, err = c.RestoreStatus(r)
	if err != nil {
		return result, err
	}
	if result.Status != "preview" || time.Since(result.CreatedAt) > 10*time.Minute {
		return result, errors.New("Yêu cầu đã dùng hoặc hết hạn; kiểm tra lại bản sao lưu")
	}
	cfg, version, err := c.Active()
	if err != nil || version != result.Version {
		return result, errors.New("Cấu hình đã thay đổi; hãy kiểm tra lại")
	}
	size, modified, err := c.restoreMetadata(cfg, result.Path)
	if err != nil || size != result.Size || modified != result.Modified {
		return result, errors.New("Bản sao lưu đã thay đổi; hãy kiểm tra lại")
	}
	result.Status = "queued"
	raw, _ := json.Marshal(result)
	if err = c.Write(statePath, raw, 0600); err != nil {
		return result, err
	}
	// Only a validated random token is passed to a fixed binary. No archive
	// path, credentials, extraction flags or shell source comes from the user.
	if _, err = c.Run([]string{"/bin/bash", "-c", "nohup \"$1\" restore-run \"$2\" >/dev/null 2>&1 </dev/null &", "restore-launch", c.Binary, result.Token}, nil); err != nil {
		result.Status = "failed"
		result.Error = "Không khởi động được tiến trình khôi phục"
		raw, _ = json.Marshal(result)
		_ = c.Write(statePath, raw, 0600)
		return result, errors.New(result.Error)
	}
	return result, nil
}
func (c *Controller) RestoreRun(token string) (runErr error) {
	req := Request{Restore: &RestoreRequest{Token: token}}
	result, err := c.RestoreStatus(req)
	if err != nil {
		return err
	}
	statePath, _ := c.restoreStatePath(token)
	// The launcher still owns this lock until its JSON response has been saved.
	// Wait for it instead of leaving an otherwise valid request queued forever.
	lock, err := acquire(filepath.Join(filepath.Dir(statePath), ".lock"), false, false)
	if err != nil {
		return err
	}
	defer lock.Close()
	result, err = c.RestoreStatus(req)
	if err != nil || result.Status != "queued" {
		return errors.New("Yêu cầu khôi phục không ở trạng thái chờ")
	}
	result.Status = "running"
	save := func() error { raw, _ := json.Marshal(result); return c.Write(statePath, raw, 0600) }
	if err = save(); err != nil {
		return err
	}
	defer func() {
		if runErr != nil {
			result.Status = "failed"
			result.Error = "Không hoàn tất khôi phục; thư mục riêng được giữ để kiểm tra, dữ liệu đang chạy không bị thay đổi"
		} else {
			result.Status = "ready"
		}
		if err := save(); err != nil {
			runErr = errors.New("Không lưu được kết quả khôi phục; kiểm tra thư mục riêng")
		}
	}()
	// Bound server resource use: do not download/extract multiple archives at once.
	global, err := acquire(filepath.Join(c.RecoveryRoot, "restores", ".worker.lock"), false, true)
	if err != nil {
		return errors.New("Một khôi phục khác đang xử lý; chưa tải backup")
	}
	defer global.Close()
	cfg, version, err := c.Active()
	if err != nil || version != result.Version {
		return errors.New("Cấu hình đã thay đổi")
	}
	size, modified, err := c.restoreMetadata(cfg, result.Path)
	if err != nil || size != result.Size || modified != result.Modified {
		return errors.New("Backup đã thay đổi")
	}
	free, err := restoreFreeBytes(filepath.Dir(statePath))
	if err != nil || free <= uint64(size)+512*1024*1024 {
		return errors.New("Không đủ dung lượng trống")
	}
	archive := filepath.Join(filepath.Dir(statePath), "archive"+restoreExtension(result.Path))
	if exists(archive) || exists(result.Directory) {
		return errors.New("Thư mục khôi phục không trống")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	args := c.rcloneArgs("copyto", cfg.DriveRemote+":"+cfg.DriveFolder+"/"+result.Path, archive, "--immutable", "--stats", "5s", "--use-json-log", "--stats-log-level", "NOTICE")
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	result.Progress = &TaskProgress{Phase: "download", TotalBytes: size, ObservedAt: time.Now().UTC()}
	if err = save(); err != nil {
		return err
	}
	command.Stderr = &restoreProgressWriter{started: result.CreatedAt, update: func(progress *TaskProgress) error { result.Progress = progress; return save() }}
	if err = command.Run(); err != nil {
		return errors.New("Không tải được backup")
	}
	info, err := nearest(archive)
	if err != nil || info.Size() != size || SafeParents(archive) != nil {
		return errors.New("Tệp tải về không hợp lệ")
	}
	size, modified, err = c.restoreMetadata(cfg, result.Path)
	if err != nil || size != result.Size || modified != result.Modified {
		return errors.New("Backup thay đổi trong khi tải")
	}
	free, err = restoreFreeBytes(filepath.Dir(statePath))
	if err != nil || free <= 512*1024*1024 {
		return errors.New("Không đủ chỗ giải nén")
	}
	limit := min(int64(free-512*1024*1024), int64(10*1024*1024*1024))
	if err = mkdirPrivate(result.Directory); err != nil {
		return err
	}
	result.Progress = &TaskProgress{Phase: "extract", ObservedAt: time.Now().UTC()}
	if err = save(); err != nil {
		return err
	}
	lastUpdate := time.Now()
	result.Files, result.ExpandedBytes, err = extractRestore(archive, result.Directory, limit, func(files int, bytes int64) error {
		if time.Since(lastUpdate) < time.Second {
			return nil
		}
		lastUpdate = time.Now()
		result.Files = files
		result.ExpandedBytes = bytes
		result.Progress = &TaskProgress{Phase: "extract", Files: int64(files), Bytes: bytes, ObservedAt: lastUpdate.UTC()}
		return save()
	})
	return err
}
func restoreExtension(value string) string {
	for _, extension := range []string{".tar.gz", ".sql.gz", ".zip"} {
		if strings.HasSuffix(value, extension) {
			return extension
		}
	}
	return ""
}
func safeRestoreMember(name string) (string, error) {
	if strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return "", errors.New("Đường dẫn trong archive không an toàn")
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") || len(clean) > 4096 {
		return "", errors.New("Archive thoát thư mục khôi phục")
	}
	return clean, nil
}

// Extract only directories and regular files into a newly-created private
// staging directory. Refuse links, devices, duplicates, zip bombs and traversal.
// SQL is decompressed but NEVER executed/imported by this operation.
func extractRestore(archive, root string, limit int64, updates ...func(int, int64) error) (int, int64, error) {
	count := 0
	var total int64
	write := func(name string, reader io.Reader, size int64) error {
		clean, err := safeRestoreMember(name)
		if err != nil {
			return err
		}
		if clean == "." || size < 0 || size > limit-total || count >= 100000 {
			return errors.New("Archive vượt giới hạn giải nén")
		}
		target := filepath.Join(root, filepath.FromSlash(clean))
		if !within(target, root) || SafeParents(target) != nil {
			return errors.New("Đường dẫn giải nén không an toàn")
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		n, err := io.Copy(file, io.LimitReader(reader, size+1))
		closeErr := file.Close()
		total += n
		count++
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if n != size {
			return errors.New("Kích thước giải nén không khớp")
		}
		for _, update := range updates {
			if err = update(count, total); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.HasSuffix(archive, ".zip") {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return count, total, err
		}
		defer reader.Close()
		if len(reader.File) > 100000 {
			return count, total, errors.New("Quá nhiều mục trong ZIP")
		}
		for _, entry := range reader.File {
			clean, err := safeRestoreMember(entry.Name)
			if err != nil {
				return count, total, err
			}
			if entry.FileInfo().IsDir() {
				if err = os.MkdirAll(filepath.Join(root, filepath.FromSlash(clean)), 0700); err != nil {
					return count, total, err
				}
				continue
			}
			if !entry.Mode().IsRegular() || entry.UncompressedSize64 > uint64(limit-total) {
				return count, total, errors.New("ZIP chứa liên kết hoặc vượt giới hạn")
			}
			f, err := entry.Open()
			if err != nil {
				return count, total, err
			}
			err = write(entry.Name, f, int64(entry.UncompressedSize64))
			_ = f.Close()
			if err != nil {
				return count, total, err
			}
		}
	} else {
		f, err := openRegular(archive)
		if err != nil {
			return count, total, err
		}
		defer f.Close()
		compressed, err := gzip.NewReader(f)
		if err != nil {
			return count, total, err
		}
		defer compressed.Close()
		if strings.HasSuffix(archive, ".sql.gz") {
			file, err := os.OpenFile(filepath.Join(root, "database.sql"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return count, total, err
			}
			n, copyErr := io.Copy(file, io.LimitReader(compressed, limit+1))
			closeErr := file.Close()
			if copyErr != nil {
				return 1, n, copyErr
			}
			if closeErr != nil {
				return 1, n, closeErr
			}
			if n > limit {
				return 1, n, errors.New("SQL vượt giới hạn")
			}
			return 1, n, nil
		}
		reader := tar.NewReader(compressed)
		headers := 0
		for {
			entry, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return count, total, err
			}
			headers++
			if headers > 100000 {
				return count, total, errors.New("Quá nhiều mục TAR")
			}
			clean, err := safeRestoreMember(entry.Name)
			if err != nil {
				return count, total, err
			}
			switch entry.Typeflag {
			case tar.TypeDir:
				if err = os.MkdirAll(filepath.Join(root, filepath.FromSlash(clean)), 0700); err != nil {
					return count, total, err
				}
			case tar.TypeReg, tar.TypeRegA:
				if err = write(entry.Name, reader, entry.Size); err != nil {
					return count, total, err
				}
			default:
				return count, total, errors.New("TAR chứa liên kết hoặc loại tệp không được phép")
			}
		}
		// Read through the gzip trailer so truncation/checksum errors are not ignored.
		trailing, err := io.Copy(io.Discard, io.LimitReader(compressed, limit-total+1))
		if err != nil {
			return count, total, err
		}
		if trailing > limit-total {
			return count, total, errors.New("Archive vượt giới hạn dữ liệu giải nén")
		}
	}
	return count, total, nil
}
