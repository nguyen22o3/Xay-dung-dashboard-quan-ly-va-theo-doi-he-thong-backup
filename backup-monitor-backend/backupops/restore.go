package backupops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type RestoreRequest struct {
	Path   string `json:"path"`
	Token  string `json:"token"`
	Parent string `json:"parent,omitempty"`
	Name   string `json:"name,omitempty"`
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
	Parent        string        `json:"parent,omitempty"`
	Name          string        `json:"name,omitempty"`
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

func validRestoreSourcePath(value string) bool {
	return len(value) <= 2048 && validRestorePath(strings.TrimSuffix(value, ".bin"))
}

func (c *Controller) restoreSource(cfg Settings, relative string) (string, error) {
	if !validRestoreSourcePath(relative) {
		return "", errors.New("Chỉ khôi phục tệp backup trong thư mục ngày/site, database hoặc panel")
	}
	drive := cfg.DriveRemote + ":" + cfg.DriveFolder
	archive := strings.TrimSuffix(relative, ".bin")
	if archive == relative {
		return drive + "/" + relative, nil
	}

	data, err := readRegular(c.RcloneConfig)
	if err != nil {
		return "", errors.New("Không đọc được cấu hình rclone để giải mã bản sao lưu")
	}
	options := map[string]string{}
	inCrypt := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if inCrypt {
				break
			}
			inCrypt = line == "[crypt]"
			continue
		}
		if key, value, ok := strings.Cut(line, "="); inCrypt && ok {
			key = strings.TrimSpace(key)
			switch key {
			case "type", "remote", "filename_encryption", "suffix", "no_data_encryption":
				options[key] = strings.TrimSpace(value)
			}
		}
	}
	if options["type"] != "crypt" || strings.TrimSuffix(options["remote"], "/") != drive || options["filename_encryption"] != "off" ||
		(options["suffix"] != "" && options["suffix"] != ".bin") || (options["no_data_encryption"] != "" && options["no_data_encryption"] != "false") {
		return "", errors.New("Cần cấu hình remote crypt trỏ tới cùng thư mục backup trên Drive và dùng hậu tố .bin")
	}
	return "crypt:" + archive, nil
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
	source, err := c.restoreSource(cfg, relative)
	if err != nil {
		return 0, "", err
	}
	raw, err := c.Run(c.rcloneArgs("lsjson", source, "--stat"), nil)
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
	if len(c.Locks) > 0 {
		lock, err := acquire(c.Locks[0], true, true)
		if err != nil {
			return result, err
		}
		defer lock.Close()
	}
	if r.Restore == nil {
		return result, errors.New("Thiếu bản sao lưu")
	}
	cfg, version, err := c.Active()
	if err != nil {
		return result, err
	}
	var destination string
	if r.Restore.Parent != "" || r.Restore.Name != "" {
		destination, err = c.validateRestoreDestination(cfg, r.Restore.Parent, r.Restore.Name)
		if err != nil {
			return result, err
		}
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
	if destination != "" {
		result.Directory, result.Parent, result.Name = destination, r.Restore.Parent, r.Restore.Name
	}
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
	expectedDir := filepath.Join(filepath.Dir(path), "data")
	if result.Parent != "" || result.Name != "" {
		var e error
		expectedDir, e = RestoreDestination(result.Parent, result.Name)
		if e != nil {
			return result, errors.New("Thư mục đích trong trạng thái không hợp lệ")
		}
	}
	if err != nil || !privateRootOwned(info) || result.Token != r.Restore.Token || result.Directory != expectedDir || !validRestoreSourcePath(result.Path) {
		return result, errors.New("Trạng thái khôi phục không an toàn")
	}
	return result, nil
}
func (c *Controller) RestoreStart(r Request) (RestorePlan, error) {
	if len(c.Locks) > 0 {
		lock, err := acquire(c.Locks[0], true, true)
		if err != nil {
			return RestorePlan{}, err
		}
		defer lock.Close()
	}
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
	if err = c.checkRestoreDestination(cfg, result); err != nil {
		return result, err
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
	// Hold the configuration lease before reading or changing restore state.
	// Storage installation must not copy a half-started worker into a new root.
	if len(c.Locks) > 0 {
		configLock, err := acquire(c.Locks[0], true, true)
		if err != nil {
			return err
		}
		defer configLock.Close()
	}
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
			result.Error = "Không hoàn tất tải tệp sao lưu; dữ liệu đang chạy không bị thay đổi"
		} else {
			result.Status = "ready"
		}
		if err := save(); err != nil {
			runErr = errors.New("Không lưu được kết quả tải tệp sao lưu")
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
	if err = c.checkRestoreDestination(cfg, result); err != nil {
		return err
	}
	size, modified, err := c.restoreMetadata(cfg, result.Path)
	if err != nil || size != result.Size || modified != result.Modified {
		return errors.New("Backup đã thay đổi")
	}
	free, err := restoreFreeBytes(filepath.Dir(statePath))
	if err != nil || free <= uint64(size)+512*1024*1024 {
		return errors.New("Không đủ dung lượng trống")
	}
	archive := filepath.Join(filepath.Dir(statePath), "archive"+restoreExtension(strings.TrimSuffix(result.Path, ".bin")))
	if exists(archive) || exists(result.Directory) {
		return errors.New("Tệp đích hoặc phiên tải trước đã tồn tại; chọn tên khác, không ghi đè")
	}
	targetDir := filepath.Dir(result.Directory)
	outputFree, err := restoreFreeBytes(targetDir)
	if err != nil || outputFree <= uint64(size)+512*1024*1024 {
		return errors.New("Không đủ dung lượng ở nơi lưu đã chọn")
	}
	if err = SafeParents(result.Directory); err != nil {
		return err
	}
	source, err := c.restoreSource(cfg, result.Path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	args := c.rcloneArgs("copyto", source, archive, "--immutable", "--stats", "5s", "--use-json-log", "--stats-log-level", "NOTICE")
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
	if err = moveFile(archive, result.Directory); err != nil {
		return errors.New("Không thể lưu tệp vào vị trí đích")
	}
	_ = os.Chmod(result.Directory, 0600)
	result.Files = 1
	result.ExpandedBytes = size
	result.Progress = &TaskProgress{Phase: "download", TotalBytes: size, Bytes: size, ObservedAt: time.Now().UTC()}
	return nil
}
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		_ = os.Remove(dst)
		return err
	}
	if err = out.Sync(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	_ = in.Close()
	_ = os.Remove(src)
	return nil
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

var restoreParentChars = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var restoreNameChars = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// Use POSIX paths even when the API is running on Windows. The destination is
// always on the AlmaLinux host, never on the machine running the browser/API.
func RestoreDestination(parent, name string) (string, error) {
	if len(parent) > 1024 || parent == "/" || !restoreParentChars.MatchString(parent) || path.Clean(parent) != parent || len(name) > 128 || !restoreNameChars.MatchString(name) {
		return "", errors.New("Chọn đường dẫn tuyệt đối trên AlmaLinux và tên tệp mới (chữ không dấu, số, -, _, .; tối đa 128 ký tự)")
	}
	return path.Join(parent, name), nil
}

func (c *Controller) validateRestoreDestination(cfg Settings, parent, name string) (string, error) {
	dir, err := RestoreDestination(parent, name)
	if err != nil {
		return "", err
	}
	if err = c.LocalPath(parent, "backup"); err != nil {
		return "", err
	}
	// Every ancestor must be a real, root-owned directory that another user
	// cannot replace while the privileged download/extractor is running.
	for p := parent; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || !info.IsDir() || !privateRootOwned(info) {
			return "", errors.New("Thư mục cha phải tồn tại, do root sở hữu và không cho người khác ghi; không dùng liên kết tượng trưng")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	protected := []string{c.RecoveryRoot, c.VersionMetadataDir, filepath.Dir(c.ConfigPath), filepath.Dir(c.RcloneConfig), filepath.Dir(c.Binary), "/etc", "/usr", "/boot", "/bin", "/sbin", "/lib", "/lib64", "/var/lib", "/www/wwwroot", "/www/server/data", "/www/server/panel"}
	settings := []Settings{cfg}
	for key, loc := range cfg.CronLocations {
		local, e := CronSettings(cfg, loc.ID, key)
		if e != nil {
			return "", e
		}
		settings = append(settings, local)
	}
	for _, local := range settings {
		for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
			protected = append(protected, effectiveSource(local, id).Path)
		}
		// A separate top-level output folder inside a backup root is allowed,
		// but never a day/category namespace scanned by sync/retention jobs.
		if validateAuxiliaryPath(local.BackupRoot, dir) != nil {
			return "", errors.New("Chọn thư mục riêng ngoài các bản backup theo ngày hoặc site/database/panel")
		}
	}
	for _, p := range protected {
		if p != "" && p != "." && (within(dir, p) || within(p, dir)) {
			return "", errors.New("Không tải/giải nén vào thư mục dữ liệu đang chạy hoặc thư mục quản lý hệ thống")
		}
	}
	if _, err = os.Lstat(dir); !os.IsNotExist(err) {
		return "", errors.New("Tên tệp đích đã tồn tại hoặc không kiểm tra được; chọn tên khác, không ghi đè")
	}
	return dir, nil
}

func (c *Controller) checkRestoreDestination(cfg Settings, plan RestorePlan) error {
	if plan.Parent == "" && plan.Name == "" {
		// Backward-compatible legacy plans use their token's private data dir.
		if _, err := os.Lstat(plan.Directory); !os.IsNotExist(err) {
			return errors.New("Tệp đích đã tồn tại; hãy kiểm tra lại")
		}
		return SafeParents(plan.Directory)
	}
	dir, err := c.validateRestoreDestination(cfg, plan.Parent, plan.Name)
	if err != nil {
		return err
	}
	if dir != plan.Directory {
		return errors.New("Tệp đích không khớp kế hoạch đã xác nhận")
	}
	return nil
}

type RestoreCheckItem struct {
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256,omitempty"`
	Status        string `json:"status"`
	Detail        string `json:"detail"`
	Files         int    `json:"files"`
	ExpandedBytes int64  `json:"expandedBytes"`
}
type RestoreCheck struct {
	Error      string             `json:"error,omitempty"`
	Token      string             `json:"token"`
	StartedAt  time.Time          `json:"startedAt"`
	FinishedAt *time.Time         `json:"finishedAt,omitempty"`
	Status     string             `json:"status"`
	Items      []RestoreCheckItem `json:"items"`
}
type drillImages struct {
	MySQL   string `json:"mysql"`
	Archive string `json:"archive"`
}

var digestImage = regexp.MustCompile(`^docker\.io/library/(mariadb|python)@sha256:[a-f0-9]{64}$`)

func (c *Controller) RestoreCheckStatus() ([]RestoreCheck, error) {
	root := filepath.Join(c.RecoveryRoot, "restore-checks")
	result := []RestoreCheck{}
	if err := SafeParents(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || !restoreTokenRe.MatchString(e.Name()) {
			continue
		}
		p := filepath.Join(root, e.Name(), "state.json")
		raw, err := readRegular(p)
		if errors.Is(err, os.ErrNotExist) {
			// A worker creates the directory before publishing its first state.
			continue
		}
		if err != nil || len(raw) > 1024*1024 {
			return nil, errors.New("invalid restore check state")
		}
		info, err := nearest(p)
		if err != nil || !privateRootOwned(info) {
			return nil, errors.New("unsafe restore check state")
		}
		var state RestoreCheck
		if json.Unmarshal(raw, &state) != nil || state.Token != e.Name() {
			return nil, errors.New("invalid restore check identity")
		}
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.After(result[j].StartedAt) })
	if len(result) > 50 {
		result = result[:50]
	}
	return result, nil
}

// Only the fixed service CLI calls this method. There is no user-controlled SQL
// endpoint and no production socket/port/environment is passed to containers.
func (c *Controller) RestoreCheckRun(ctx context.Context) (runErr error) {
	if len(c.Locks) > 0 {
		lock, err := acquire(c.Locks[0], true, true)
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	cfg, _, err := c.Active()
	if err != nil {
		return err
	}
	root := filepath.Join(c.RecoveryRoot, "restore-checks")
	if err = mkdirPrivate(root); err != nil {
		return err
	}
	lock, err := acquire(filepath.Join(root, ".worker.lock"), false, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(random[:])
	dir := filepath.Join(root, token)
	if err = mkdirPrivate(dir); err != nil {
		return err
	}
	state := RestoreCheck{Token: token, StartedAt: time.Now().UTC(), Status: "running", Items: []RestoreCheckItem{}}
	save := func() error {
		raw, _ := json.Marshal(state)
		return c.Write(filepath.Join(dir, "state.json"), raw, 0600)
	}
	if err = save(); err != nil {
		return err
	}
	defer func() {
		ended := time.Now().UTC()
		state.FinishedAt = &ended
		state.Status = "success"
		if runErr != nil {
			state.Status = "failed"
			state.Error = "Kiểm tra chưa đạt hoặc chưa hoàn tất; xem kết quả từng bản sao lưu và diagnostics riêng trên máy chủ"
		}
		for _, item := range state.Items {
			if item.Status != "success" {
				state.Status = "failed"
			}
		}
		if e := save(); e != nil {
			runErr = e
		}
	}()
	imagesRaw, err := readRegular(filepath.Join(filepath.Dir(c.ConfigPath), "restore-check-images.json"))
	if err != nil {
		return errors.New("restore images are not configured")
	}
	var images drillImages
	if json.Unmarshal(imagesRaw, &images) != nil || !strings.HasPrefix(images.MySQL, "docker.io/library/mariadb@") || !strings.HasPrefix(images.Archive, "docker.io/library/python@") || !digestImage.MatchString(images.MySQL) || !digestImage.MatchString(images.Archive) {
		return errors.New("restore images must use pinned official digests")
	}
	sources, err := drillSources(cfg.BackupRoot)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, item := range sources {
		seen[item.Kind] = true
	}
	for _, kind := range []string{"database", "site", "panel"} {
		if !seen[kind] {
			state.Items = append(state.Items, RestoreCheckItem{Kind: kind, Status: "failed", Detail: "Không tìm thấy bản sao lưu; chưa kiểm tra được"})
		}
	}
	for _, item := range sources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		item.Status = "running"
		state.Items = append(state.Items, item)
		index := len(state.Items) - 1
		if err = save(); err != nil {
			return err
		}
		workspace, err := os.MkdirTemp(dir, "check-")
		if err != nil {
			return err
		}
		// This directory was created exclusively by this invocation and is never a
		// production path. Container writable data lives only in an anonymous tmpfs.
		e := func() error {
			defer os.RemoveAll(workspace)
			archive := filepath.Join(workspace, "input"+restoreExtension(item.Source))
			hash, e := snapshotDrillArchive(item.Source, archive)
			if e != nil {
				return e
			}
			state.Items[index].SHA256 = hash
			data := filepath.Join(workspace, "data")
			if e = mkdirPrivate(data); e != nil {
				return e
			}
			free, e := restoreFreeBytes(workspace)
			if e != nil || free < 1024*1024*1024 {
				return errors.New("insufficient drill disk margin")
			}
			files, expanded, e := extractRestore(archive, data, min(int64(free-512*1024*1024), int64(4*1024*1024*1024)))
			state.Items[index].Files = files
			state.Items[index].ExpandedBytes = expanded
			if e != nil {
				return e
			}
			if files == 0 {
				return errors.New("empty archive")
			}
			if item.Kind == "database" {
				sql := filepath.Join(data, "database.sql")
				if e = os.Chmod(sql, 0444); e != nil {
					return e
				}
				return runDrillContainer(ctx, images.MySQL, "999:999", sql, mysqlDrill, 768)
			}
			// Archive integrity only: never execute PHP, restore live vhosts, or start
			// aaPanel from extracted data. JSON structure is additionally validated.
			if e = filepath.WalkDir(data, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					return os.Chmod(p, 0755)
				}
				return os.Chmod(p, 0444)
			}); e != nil {
				return e
			}
			validation := "CHECK_JSON=0\n" + archiveDrill
			if item.Kind == "panel" {
				validation = "CHECK_JSON=1\n" + archiveDrill
			}
			return runDrillContainer(ctx, images.Archive, "65534:65534", data, validation, 256)
		}()
		state.Items[index].Status = "success"
		state.Items[index].Detail = "Giải nén và kiểm tra cấu trúc archive; chưa kiểm tra ứng dụng chạy"
		if item.Kind == "database" {
			state.Items[index].Detail = "Đã phục hồi SQL, kiểm tra bảng và đọc số hàng trong container không có mạng"
		}
		if e != nil {
			state.Items[index].Status = "failed"
			state.Items[index].Detail = "Kiểm tra không đạt; xem diagnostics riêng trên máy chủ (không thay đổi dữ liệu đang chạy)"
			_ = c.Write(filepath.Join(dir, fmt.Sprintf("diagnostic-%d.txt", index)), []byte(e.Error()), 0600)
		}
		if err = save(); err != nil {
			return err
		}
	}
	for _, item := range state.Items {
		if item.Status == "failed" {
			return errors.New("one or more isolated restore checks failed")
		}
	}
	return nil
}
func drillSources(root string) ([]RestoreCheckItem, error) {
	if err := SafeParents(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	chosen := map[string]RestoreCheckItem{}
	days := map[string]string{}
	times := map[string]time.Time{}
	now := time.Now().Format("2006-01-02")
	count := 0
	for _, day := range entries {
		date, e := time.Parse("2006-01-02", day.Name())
		if e != nil || date.Format("2006-01-02") != day.Name() || day.Name() > now || !day.IsDir() || day.Type()&os.ModeSymlink != 0 {
			continue
		}
		err = filepath.WalkDir(filepath.Join(root, day.Name()), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			count++
			if count > 200000 {
				return errors.New("backup index exceeds drill budget")
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink in drill source")
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(root, p)
			if e != nil || !validRestorePath(filepath.ToSlash(rel)) {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			kind := parts[1]
			key := kind + ":" + filepath.Dir(strings.Join(parts[2:], "/"))
			if kind == "panel" {
				key = kind
			}
			info, e := d.Info()
			if e != nil || !info.Mode().IsRegular() {
				return errors.New("invalid backup source")
			}
			if day.Name() > days[key] || day.Name() == days[key] && (info.ModTime().After(times[key]) || info.ModTime() == times[key] && p > chosen[key].Source) {
				chosen[key] = RestoreCheckItem{Kind: kind, Source: p}
				days[key] = day.Name()
				times[key] = info.ModTime()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := []RestoreCheckItem{}
	for _, item := range chosen {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Source < result[j].Source })
	if len(result) > 200 {
		return nil, errors.New("too many restore targets")
	}
	return result, nil
}
func snapshotDrillArchive(source, target string) (string, error) {
	if e := SafeParents(source); e != nil {
		return "", e
	}
	f, e := openRegular(source)
	if e != nil {
		return "", e
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || before.Size() <= 0 || before.Size() > 2*1024*1024*1024 {
		return "", errors.New("invalid archive size")
	}
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, before.Size()+1))
	closeErr := out.Close()
	after, statErr := f.Stat()
	if e != nil || closeErr != nil || statErr != nil || n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", errors.New("archive changed during snapshot")
	}
	if e = os.Chmod(target, 0444); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func runDrillContainer(ctx context.Context, image, user, input, script string, memory int) error {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	name := "backup-monitor-check-" + hex.EncodeToString(random[:])
	args := []string{"run", "--rm", "--name", name, "--pull=never", "--image-volume=ignore", "--network=none", "--read-only", "--cap-drop=all", "--security-opt=no-new-privileges", "--pids-limit=128", "--cpus=1", "--memory=" + fmt.Sprint(memory) + "m", "--memory-swap=" + fmt.Sprint(memory) + "m", "--user=" + user, "--tmpfs=/work:rw,nosuid,nodev,size=512m,mode=1777", "--volume=" + input + ":/input/archive:ro,Z", "--entrypoint=/bin/sh", image, "-c", script}
	command := exec.CommandContext(ctx, "/usr/bin/podman", args...)
	var output drillOutput
	command.Stdout = &output
	command.Stderr = &output
	// Killing the podman client does not necessarily remove its container.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "/usr/bin/podman", "rm", "--force", name).Run()
	}()
	if err := command.Run(); err != nil {
		return errors.New("isolated restore failed: " + string(output.data))
	}
	return nil
}

type drillOutput struct{ data []byte }

func (w *drillOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > 32768 {
		w.data = append([]byte(nil), w.data[len(w.data)-32768:]...)
	}
	return n, nil
}

const mysqlDrill = `set -eu
mariadb-install-db --no-defaults --auth-root-authentication-method=normal --datadir=/work/mysql > /work/init.log 2>&1
mariadbd --no-defaults --datadir=/work/mysql --socket=/work/mysql.sock --pid-file=/work/mysql.pid --skip-networking --innodb-buffer-pool-size=64M --innodb-log-buffer-size=8M --performance-schema=OFF --log-error=/work/mysql.log &
pid=$!;trap 'kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true' EXIT
ready=0;for i in $(seq 1 90); do if mysqladmin --no-defaults --socket=/work/mysql.sock -uroot ping >/dev/null 2>&1; then ready=1;break;fi;sleep 1;done;test "$ready" = 1
mysql --no-defaults --socket=/work/mysql.sock -uroot -e 'CREATE DATABASE checkdb'
mysql --no-defaults --socket=/work/mysql.sock -uroot checkdb < /input/archive
tables=$(mysql --no-defaults --socket=/work/mysql.sock -uroot -N -e "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema')")
test "$tables" -gt 0
mysqlcheck --no-defaults --socket=/work/mysql.sock -uroot --all-databases --check >/work/checks
mysql --no-defaults --socket=/work/mysql.sock -uroot -N -e "SELECT CONCAT('SELECT COUNT(*) FROM ',CHAR(96),REPLACE(table_schema,CHAR(96),CONCAT(CHAR(96),CHAR(96))),CHAR(96),'.',CHAR(96),REPLACE(table_name,CHAR(96),CONCAT(CHAR(96),CHAR(96))),CHAR(96),';') FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema') AND table_type='BASE TABLE'" > /work/count.sql
mysql --no-defaults --socket=/work/mysql.sock -uroot < /work/count.sql >/work/counts
`
const archiveDrill = `set -eu
export CHECK_JSON
python - <<'PY'
import os, sqlite3, json, tempfile, shutil
root='/input/archive'
for directory, _, files in os.walk(root):
    for name in files:
        path=os.path.join(directory,name)
        with open(path,'rb') as f: header=f.read(16)
        if header == b'SQLite format 3\x00':
            # WAL/SHM recovery needs a writable PRIVATE copy, not the archived
            # read-only directory and never a live aaPanel database.
            with tempfile.TemporaryDirectory(dir='/work') as scratch:
                target=os.path.join(scratch,'check.sqlite')
                shutil.copyfile(path,target)
                for suffix in ('-wal','-shm','-journal'):
                    if os.path.isfile(path+suffix): shutil.copyfile(path+suffix,target+suffix)
                db=sqlite3.connect(target)
                try:
                    result=db.execute('PRAGMA integrity_check').fetchall()
                    if result != [('ok',)]: raise RuntimeError('SQLite integrity check failed: '+name)
                finally: db.close()
        if os.environ.get('CHECK_JSON')=='1' and name.lower().endswith('.json') and os.path.getsize(path)>0:
            if os.path.getsize(path)>32*1024*1024: raise RuntimeError('JSON too large')
            with open(path,'r',encoding='utf-8-sig') as f: json.load(f)
print('Archive integrity checked; application execution not tested')
PY
`
