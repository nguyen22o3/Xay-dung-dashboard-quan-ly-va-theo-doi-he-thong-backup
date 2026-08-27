package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// createBackupRequest yêu cầu tạo bản backup từ dashboard.
type createBackupRequest struct {
	SourcePath   string   `json:"source_path"`  // Đường dẫn file/folder nguồn cần backup
	Destinations []string `json:"destinations"` // Danh sách đích: "server" | "drive" | "nas"
	DestPath     string   `json:"dest_path"`    // Với "server": đường dẫn đích trên máy (tùy chọn)
	SourceName   string   `json:"source_name"`  // Tên nguồn hiển thị (vd "Server Web LAMP"), tùy chọn
}

// createBackupHandler tạo các bản backup: tar nguồn một lần rồi copy tới từng đích.
func createBackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Chỉ hỗ trợ POST", http.StatusMethodNotAllowed)
		return
	}

	var req createBackupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Dữ liệu JSON không hợp lệ", http.StatusBadRequest)
		return
	}

	req.SourcePath = strings.TrimSpace(req.SourcePath)
	if req.SourcePath == "" {
		http.Error(w, "Thiếu đường dẫn nguồn (source_path)", http.StatusBadRequest)
		return
	}

	// Kiểm tra nguồn tồn tại trên máy (destination server/drive/nas đều đọc từ local).
	if _, err := os.Stat(req.SourcePath); err != nil {
		http.Error(w, "Đường dẫn nguồn không tồn tại: "+req.SourcePath, http.StatusBadRequest)
		return
	}

	msg, okDests, err := createBackupToDests(req.SourcePath, req.Destinations, req.DestPath, req.SourceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"message":      msg,
		"destinations": okDests,
	})
}

// createBackupToDests nén nguồn rồi copy tới từng đích đã chọn, ghi record cho mỗi đích.
// Trả về thông báo thành công, danh sách đích đã ghi và lỗi (nếu có).
func createBackupToDests(sourcePath string, destinations []string, destPath, sourceName string) (string, []string, error) {
	// Chuẩn hoá danh sách đích, loại bỏ trùng lặp (giữ thứ tự).
	set := map[string]bool{}
	var dests []string
	for _, d := range destinations {
		n := normalizeDestination(d)
		if n == "nas" && !nasConfigured() {
			return "", nil, fmt.Errorf("Nơi cất giữ NAS chưa được cấu hình. Vui lòng chỉ chọn Server hoặc Google Drive.")
		}
		if !set[n] {
			set[n] = true
			dests = append(dests, n)
		}
	}
	if len(dests) == 0 {
		return "", nil, fmt.Errorf("Vui lòng chọn ít nhất một nơi cất giữ (destinations)")
	}

	timestamp := time.Now().Format("20060102_150405")
	baseName := filepath.Base(strings.TrimRight(sourcePath, "/"))
	fileName := fmt.Sprintf("manual_%s_%s.tar.gz", sanitizeBase(baseName), timestamp)

	// Temp file để nén.
	tmp, err := os.CreateTemp("", "bkup_*.tar.gz")
	if err != nil {
		return "", nil, fmt.Errorf("Không tạo được file tạm")
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath)

	// 1. Nén nguồn thành tar.gz (chỉ nén một lần).
	if err := createTarball(sourcePath, tmpPath); err != nil {
		return "", nil, fmt.Errorf("Nén backup thất bại: %v", err)
	}

	if strings.TrimSpace(sourceName) == "" {
		sourceName = "Manual"
	}

	// 2. Copy tới từng đích và ghi mỗi record tương ứng.
	okDests := []string{}
	for _, dest := range dests {
		var displayDest, fileRef string
		switch dest {
		case "drive":
			remote := driveRemoteFolder() + "/" + fileName
			if err := copyToDrive(tmpPath, remote); err != nil {
				log.Printf("Upload Google Drive thất bại: %v", err)
				continue
			}
			displayDest = "Google Drive"
			fileRef = remote
		case "nas":
			remote := nasRemoteFolder() + "/" + fileName
			if err := copyToNas(tmpPath, remote); err != nil {
				log.Printf("Upload NAS thất bại: %v", err)
				continue
			}
			displayDest = "NAS"
			fileRef = remote
		default: // server
			destDir := sanitizeServerPath(destPath, "/var/backups")
			if err := os.MkdirAll(destDir, 0755); err != nil {
				log.Printf("Không tạo được thư mục đích: %v", err)
				continue
			}
			fullPath := filepath.Join(destDir, fileName)
			if err := copyLocal(tmpPath, fullPath); err != nil {
				os.Remove(tmpPath)
				log.Printf("Lưu file trên server thất bại: %v", err)
				continue
			}
			displayDest = "Server"
			fileRef = fullPath
		}

		var sizeBytes int64
		if st, e := os.Stat(fileRef); e == nil {
			sizeBytes = st.Size()
		}

		rec := BackupRecord{
			Source:      sourceName,
			Destination: displayDest,
			FileName:    fileName,
			FilePath:    fileRef,
			Status:      "Success",
			SizeMB:      float64(sizeBytes) / (1024 * 1024),
			CreatedAt:   time.Now(),
		}
		if err := DB.Create(&rec).Error; err != nil {
			log.Printf("Lỗi lưu bản ghi backup: %v", err)
			continue
		}
		okDests = append(okDests, displayDest)
	}

	os.Remove(tmpPath)

	if len(okDests) == 0 {
		return "", nil, fmt.Errorf("Tạo bản backup thất bại cho mọi nơi cất giữ đã chọn")
	}

	go sendNotifications(sourceName, "Success (tạo thủ công: "+fileName+")")

	return "Đã tạo bản backup thành công tại: " + strings.Join(okDests, ", "), okDests, nil
}

// deleteBackupHandler xóa backup tại đúng nơi cất giữ của bản ghi.
func deleteBackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Chỉ hỗ trợ DELETE", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Thiếu ID bản ghi", http.StatusBadRequest)
		return
	}

	var rec BackupRecord
	if err := DB.First(&rec, id).Error; err != nil {
		http.Error(w, "Không tìm thấy bản ghi", http.StatusNotFound)
		return
	}

	var err error
	switch rec.Destination {
	case "Google Drive":
		if rec.FilePath != "" {
			err = deleteFromDrive(rec.FilePath)
		}
	case "NAS":
		if rec.FilePath != "" {
			err = deleteFromNas(rec.FilePath)
		}
	case "Server":
		if rec.FilePath != "" {
			err = os.Remove(rec.FilePath)
		}
	default:
		err = fmt.Errorf("không hỗ trợ xóa cho nơi cất giữ %q", rec.Destination)
	}
	if err != nil {
		http.Error(w, "Xóa file thất bại: "+err.Error(), http.StatusInternalServerError)
		return
	}

	DB.Delete(&rec)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Đã xóa bản backup tại " + rec.Destination,
	})
}

// uploadBackupHandler nhận file/folder upload từ trình duyệt, lưu vào thư mục tạm,
// nén lại và copy tới các đích đã chọn.
func uploadBackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Chỉ hỗ trợ POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(512 << 20); err != nil {
		http.Error(w, "Không đọc được file upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Đọc các field đi kèm.
	var destinations []string
	var destPath, sourceName, emptyFolder, serverPath string
	if v := r.FormValue("destinations"); v != "" {
		_ = json.Unmarshal([]byte(v), &destinations)
	}
	destPath = strings.TrimSpace(r.FormValue("dest_path"))
	sourceName = strings.TrimSpace(r.FormValue("source_name"))
	emptyFolder = strings.Trim(strings.ReplaceAll(r.FormValue("empty_folder"), "\\", "/"), "/")
	serverPath = strings.TrimSpace(r.FormValue("source_path"))

	// Tạo thư mục tạm.
	tmpRoot, err := os.MkdirTemp("", "upload_bkup_")
	if err != nil {
		http.Error(w, "Không tạo được thư mục tạm", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpRoot)

	files := r.MultipartForm.File["files"]

	// Nếu người dùng chọn "Nhập đường dẫn" để backup nguồn trên server
	// (có thể gộp cùng file upload), copy nội dung đó vào vùng tạm.
	if serverPath != "" {
		info, statErr := os.Stat(serverPath)
		if statErr != nil {
			http.Error(w, "Không đọc được đường dẫn nguồn "+serverPath+": "+statErr.Error(), http.StatusBadRequest)
			return
		}
		if info.IsDir() {
			base := filepath.Base(strings.TrimRight(serverPath, "/"))
			if base == "." || base == string(filepath.Separator) || base == "" {
				base = "source"
			}
			if copyErr := copyDirRecursive(serverPath, filepath.Join(tmpRoot, base)); copyErr != nil {
				http.Error(w, "Lỗi copy đường dẫn nguồn: "+copyErr.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			dst := filepath.Join(tmpRoot, filepath.Base(serverPath))
			if copyErr := copyFileRecursive(serverPath, dst); copyErr != nil {
				http.Error(w, "Lỗi copy file nguồn: "+copyErr.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// Lưu từng file upload vào thư mục tạm, giữ cấu trúc thư mục.
	for _, fh := range files {
		rel := strings.TrimPrefix(fh.Filename, "/")
		rel = strings.ReplaceAll(rel, "\\", "/")
		dst := filepath.Join(tmpRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			http.Error(w, "Lỗi tạo thư mục tạm: "+err.Error(), http.StatusInternalServerError)
			return
		}
		in, err := fh.Open()
		if err != nil {
			http.Error(w, "Lỗi mở file upload: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out, err := os.Create(dst)
		if err != nil {
			in.Close()
			http.Error(w, "Lỗi ghi file tạm: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			http.Error(w, "Lỗi khi lưu file upload: "+err.Error(), http.StatusInternalServerError)
			return
		}
		in.Close()
		out.Close()
	}

	// Nếu người dùng yêu cầu tạo một thư mục rỗng, tạo nó trong vùng tạm
	// để vẫn backup được cấu trúc.
	if emptyFolder != "" {
		if err := os.MkdirAll(filepath.Join(tmpRoot, emptyFolder), 0755); err != nil {
			http.Error(w, "Lỗi tạo thư mục rỗng: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Nếu không có bất kỳ nguồn nào (file upload, thư mục rỗng, hay đường dẫn server) -> lỗi.
	if len(files) == 0 && emptyFolder == "" && serverPath == "" {
		http.Error(w, "Chưa có nguồn dữ liệu nào được chọn (file/thư mục, đường dẫn server, hoặc thư mục rỗng cần tạo).", http.StatusBadRequest)
		return
	}

	// Chọn nguồn nén: chỉ 1 file & không có gì khác thì nén file đó,
	// còn lại nén cả thư mục (nhiều file, có thư mục rỗng, hoặc có nguồn server).
	var sourcePath string
	if len(files) == 1 && emptyFolder == "" && serverPath == "" {
		rel := strings.TrimPrefix(files[0].Filename, "/")
		rel = strings.ReplaceAll(rel, "\\", "/")
		sourcePath = filepath.Join(tmpRoot, rel)
	} else {
		sourcePath = tmpRoot
	}

	// Gọi logic tạo backup tới các đích đã chọn.
	msg, okDests, err := createBackupToDests(sourcePath, destinations, destPath, sourceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("🗂 Uploaded backup -> %v || %s", okDests, sourcePath)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"message":      msg,
		"destinations": okDests,
	})
}

// copyDirRecursive copy toàn bộ nội dung thư mục src vào dst.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFileRecursive(path, target)
	})
}

// copyFileRecursive copy một file từ src tới dst, tạo thư mục cha nếu cần.
func copyFileRecursive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// sanitizeServerPath chuẩn hoá đường dẫn lưu trữ trên server: làm sạch,
// loại bỏ ".." và đảm bảo đường dẫn tuyệt đối. Nếu rỗng hoặc sai, trả về fallback.
func sanitizeServerPath(raw, fallback string) string {
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if cleaned == "" || cleaned == "." {
		return fallback
	}
	if !filepath.IsAbs(cleaned) {
		cleaned = "/" + cleaned
	}
	return cleaned
}

// folderEntry mô tả một thư mục con trong danh sách dễ duyệt.
type folderEntry struct {
	Name  string `json:"name"`  // Tên thư mục con
	Path  string `json:"path"`  // Đường dẫn đầy đủ
	Write bool   `json:"write"` // Backend có quyền ghi vào thư mục này không
}

// folderPickHandler trả về danh sách thư mục con của `path` (mặc định /)
// kèm khả năng ghi, để frontend hiển thị hộp thoại chọn thư mục lưu trên server.
func folderPickHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Chỉ hỗ trợ GET", http.StatusMethodNotAllowed)
		return
	}
	base := strings.TrimSpace(r.URL.Query().Get("path"))
	if base == "" {
		base = "/"
	}
	// Kiểm tra thư mục tồn tại.
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		http.Error(w, "Thư mục không tồn tại: "+base, http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		http.Error(w, "Không đọc được thư mục: "+err.Error(), http.StatusInternalServerError)
		return
	}

	folders := []folderEntry{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(base, e.Name())
		writeable := unixWritable(full)
		folders = append(folders, folderEntry{
			Name:  e.Name(),
			Path:  full,
			Write: writeable,
		})
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":    base,
		"folders": folders,
	})
}

// unixWritable kiểm tra backend (user đang chạy) có quyền ghi thư mục không.
func unixWritable(dir string) bool {
	test, err := os.CreateTemp(dir, ".wtest_*")
	if err != nil {
		return false
	}
	name := test.Name()
	test.Close()
	os.Remove(name)
	return true
}

// ---------- helpers ----------

func normalizeDestination(d string) string {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "drive", "gdrive", "google drive", "google_drive", "googledrive":
		return "drive"
	case "nas":
		return "nas"
	default:
		return "server"
	}
}

func driveRemoteFolder() string {
	if r := os.Getenv("DRIVE_REMOTE"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return "gdrive:Backup_Dashboard"
}

func sanitizeBase(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return name
}

func createTarball(src, out string) error {
	// tar -czf out -C <parent> <base>
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	cmd := exec.Command("tar", "-czf", out, "-C", parent, base)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyToDrive(src, remote string) error {
	cmd := exec.Command("rclone", "copyto", src, remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func deleteFromDrive(remote string) error {
	cmd := exec.Command("rclone", "delete", remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyLocal(src, dst string) error {
	cmd := exec.Command("cp", src, dst)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// nasRemoteFolders returns the configured NAS remote(s) as a space-separated list.
func nasRemoteFolder() string {
	if r := os.Getenv("NAS_REMOTE"); r != "" {
		return strings.TrimRight(r, "/")
	}
	// Nếu chưa cấu hình NAS_REMOTE thì dùng remote mặc định (chưa có).
	return "nas:backup"
}

// nasConfigured báo liệu NAS đã được cấu hình (biến NAS_REMOTE khác rỗng).
func nasConfigured() bool {
	return os.Getenv("NAS_REMOTE") != ""
}

func copyToNas(src, remote string) error {
	cmd := exec.Command("rclone", "copyto", src, remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func deleteFromNas(remote string) error {
	cmd := exec.Command("rclone", "delete", remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
