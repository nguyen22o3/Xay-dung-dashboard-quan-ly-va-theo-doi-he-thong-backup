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
	SourcePath     string   `json:"source_path"`      // Đường dẫn file/folder nguồn cần backup
	Destinations   []string `json:"destinations"`     // Danh sách đích: "server" | "drive" | "nas"
	DestPath       string   `json:"dest_path"`        // Với "server": đường dẫn đích trên máy (tùy chọn)
	SourceName     string   `json:"source_name"`      // Tên nguồn hiển thị (vd "Server Web LAMP"), tùy chọn
	CustomFileName string   `json:"custom_file_name"` // Tên file backup tùy chọn
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

	msg, okDests, err := createBackupToDests(req.SourcePath, req.Destinations, req.DestPath, req.SourceName, req.CustomFileName)
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
func createBackupToDests(sourcePath string, destinations []string, destPath, sourceName, customFileName string) (string, []string, error) {
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
	var baseName string
	if strings.TrimSpace(customFileName) != "" {
		baseName = sanitizeBase(strings.TrimSpace(customFileName))
	} else {
		baseName = sanitizeBase(filepath.Base(strings.TrimRight(sourcePath, "/")))
	}
	fileName := fmt.Sprintf("%s_%s.tar.gz", baseName, timestamp)

	// Temp file để nén.
	tmp, err := os.CreateTemp("", "bkup_*.tar.gz")
	if err != nil {
		return "", nil, fmt.Errorf("Không tạo được file tạm")
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath)

	if strings.TrimSpace(sourceName) == "" {
		sourceName = "Manual"
	}

	// 1. Nén nguồn thành tar.gz (chỉ nén một lần).
	if err := createTarball(sourcePath, tmpPath); err != nil {
		os.Remove(tmpPath)
		// Lưu bản ghi Failed và gửi thông báo để user không bị im lặng
		recFail := BackupRecord{
			Source:    sourceName,
			FileName:  fileName,
			Status:    "Failed",
			SizeMB:    0,
			CreatedAt: time.Now(),
		}
		// Gán destination chung nếu có thể
		if len(dests) == 1 {
			switch dests[0] {
			case "drive":
				recFail.Destination = "Google Drive"
				recFail.FilePath = driveRemoteFolder() + "/" + fileName
			case "nas":
				recFail.Destination = "NAS"
				recFail.FilePath = nasRemoteFolder() + "/" + fileName
			default:
				recFail.Destination = "Server"
				if strings.TrimSpace(destPath) == "" {
					recFail.FilePath = fileName
				} else {
					cleaned := sanitizeServerPath(destPath, "")
					if cleaned == "" {
						cleaned = destPath
					}
					recFail.FilePath = filepath.Join(cleaned, fileName)
				}
			}
		} else {
			recFail.Destination = strings.Join(dests, ",")
		}
		DB.Create(&recFail)
		go sendNotifications(sourceName, "Failed (nén: "+fileName+" - "+err.Error()+")")
		return "", nil, fmt.Errorf("Nén backup thất bại: %v", err)
	}

	// 2. Copy tới từng đích và ghi mỗi record tương ứng.
	okDests := []string{}
	failedDests := []string{}
	for _, dest := range dests {
		var displayDest, fileRef string
		switch dest {
		case "drive":
			remote := driveRemoteFolder() + "/" + fileName
			if err := copyToDrive(tmpPath, remote); err != nil {
				log.Printf("Upload Google Drive thất bại: %v", err)
				recFail := BackupRecord{
					Source:    sourceName,
					Destination: "Google Drive",
					FileName:  fileName,
					FilePath:  remote,
					Status:    "Failed",
					SizeMB:    0,
					CreatedAt: time.Now(),
				}
				DB.Create(&recFail)
				failedDests = append(failedDests, "Google Drive")
				go sendNotifications(sourceName, "Failed (Google Drive: "+fileName+" - "+err.Error()+")")
				continue
			}
			displayDest = "Google Drive"
			fileRef = remote
		case "nas":
			remote := nasRemoteFolder() + "/" + fileName
			if err := copyToNas(tmpPath, remote); err != nil {
				log.Printf("Upload NAS thất bại: %v", err)
				recFail := BackupRecord{
					Source:    sourceName,
					Destination: "NAS",
					FileName:  fileName,
					FilePath:  remote,
					Status:    "Failed",
					SizeMB:    0,
					CreatedAt: time.Now(),
				}
				DB.Create(&recFail)
				failedDests = append(failedDests, "NAS")
				go sendNotifications(sourceName, "Failed (NAS: "+fileName+" - "+err.Error()+")")
				continue
			}
			displayDest = "NAS"
			fileRef = remote
		default: // server
			// Không dùng thư mục mặc định - yêu cầu user tự chọn chỗ lưu
			trimmedDest := strings.TrimSpace(destPath)
			if trimmedDest == "" {
				log.Printf("Thiếu đường dẫn lưu trên Server: user chưa chọn chỗ lưu")
				recFail := BackupRecord{
					Source:    sourceName,
					Destination: "Server",
					FileName:  fileName,
					FilePath:  fileName,
					Status:    "Failed",
					SizeMB:    0,
					CreatedAt: time.Now(),
				}
				DB.Create(&recFail)
				failedDests = append(failedDests, "Server")
				go sendNotifications(sourceName, "Failed (Server: chưa chọn chỗ lưu - vui lòng chọn đường dẫn)")
				continue
			}
			destDir := sanitizeServerPath(trimmedDest, "")
			if destDir == "" {
				log.Printf("Đường dẫn lưu không hợp lệ sau khi chuẩn hóa: %q", destPath)
				recFail := BackupRecord{
					Source:    sourceName,
					Destination: "Server",
					FileName:  fileName,
					FilePath:  fileName,
					Status:    "Failed",
					SizeMB:    0,
					CreatedAt: time.Now(),
				}
				DB.Create(&recFail)
				failedDests = append(failedDests, "Server")
				go sendNotifications(sourceName, "Failed (Server: đường dẫn không hợp lệ)")
				continue
			}
			if err := os.MkdirAll(destDir, 0755); err != nil {
				log.Printf("Không tạo được thư mục đích: %v", err)
				// Lưu bản ghi Failed để user thấy và nhận thông báo
				recFail := BackupRecord{
					Source:    sourceName,
					Destination: "Server",
					FileName:  fileName,
					FilePath:  filepath.Join(destDir, fileName),
					Status:    "Failed",
					SizeMB:    0,
					CreatedAt: time.Now(),
				}
				if err2 := DB.Create(&recFail).Error; err2 != nil {
					log.Printf("Lỗi tạo bản ghi Failed Server Mkdir: %v", err2)
				} else {
					log.Printf("Đã tạo bản ghi Failed cho Server Mkdir: %s", fileName)
				}
				failedDests = append(failedDests, "Server")
				go sendNotifications(sourceName, "Failed (Server Mkdir: "+fileName+" - "+err.Error()+")")
				continue
			}
			fullPath := filepath.Join(destDir, fileName)
			if err := copyLocal(tmpPath, fullPath); err != nil {
				log.Printf("Lưu file trên server thất bại: %v", err)
				recFail := BackupRecord{
					Source:    sourceName,
					Destination: "Server",
					FileName:  fileName,
					FilePath:  fullPath,
					Status:    "Failed",
					SizeMB:    0,
					CreatedAt: time.Now(),
				}
				DB.Create(&recFail)
				failedDests = append(failedDests, "Server")
				go sendNotifications(sourceName, "Failed (Server: "+fileName+" - "+err.Error()+")")
				continue
			}
			displayDest = "Server"
			fileRef = fullPath
		}

		var sizeBytes int64
		// Lấy dung lượng từ file tạm đã nén (áp dụng cho cả Server/Drive/NAS)
		// thay vì os.Stat(fileRef) vì fileRef với Drive/NAS là remote path (gdrive:...) không stat được -> luôn 0.
		if st, e := os.Stat(tmpPath); e == nil {
			sizeBytes = st.Size()
		} else if st, e := os.Stat(fileRef); e == nil {
			// Fallback cho Server nếu tmp đã xóa
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
		// Đã gửi Failed cho từng dest ở trên, gửi thêm tổng hợp nếu chưa có
		if len(failedDests) == 0 {
			go sendNotifications(sourceName, "Failed (tạo thủ công: "+fileName+")")
		} else {
			// Đã gửi cho từng dest, không gửi trùng nếu chỉ 1 dest (tránh spam)
			if len(failedDests) > 1 {
				go sendNotifications(sourceName, "Failed (tạo thủ công: "+fileName+" - thất bại tại "+strings.Join(failedDests, ", ")+")")
			}
		}
		return "", failedDests, fmt.Errorf("Tạo bản backup thất bại cho mọi nơi cất giữ đã chọn (%s)", strings.Join(failedDests, ", "))
	}

	// Có một phần thất bại, thông báo thêm để user biết
	if len(failedDests) > 0 {
		go sendNotifications(sourceName, "Failed (tạo thủ công một phần: "+fileName+" - thất bại tại "+strings.Join(failedDests, ", ")+", thành công tại "+strings.Join(okDests, ", ")+")")
	}

	// Kiểm tra và cập nhật schedule nếu backup thủ công thỏa mãn lịch trình
	CheckAndAdvanceSchedulesForBackup(sourceName, fileName)

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
			// Nếu file đã không tồn tại (vd lưu ở /tmp bị xóa khi reboot),
			// vẫn cho phép xóa bản ghi DB thay vì báo lỗi.
			if err != nil && os.IsNotExist(err) {
				log.Printf("File không tồn tại, vẫn xóa bản ghi DB: %s", rec.FilePath)
				err = nil
			}
		}
	default:
		// Với bản ghi "Missed" hoặc nơi cất giữ không xác định, chỉ xóa bản ghi DB.
		if rec.FilePath == "" || rec.Destination == "Missed" {
			err = nil
		} else {
			err = fmt.Errorf("không hỗ trợ xóa cho nơi cất giữ %q", rec.Destination)
		}
	}
	if err != nil {
		// Với Drive/NAS, nếu file không tồn tại trên remote (rclone báo lỗi),
		// vẫn cho phép xóa bản ghi DB.
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not found") || strings.Contains(lower, "no such") || strings.Contains(lower, "doesn't exist") || strings.Contains(lower, "not exist") {
			log.Printf("File remote không tồn tại, vẫn xóa bản ghi DB: %s (%v)", rec.FilePath, err)
		} else {
			http.Error(w, "Xóa file thất bại: "+err.Error(), http.StatusInternalServerError)
			return
		}
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
	var destPath, sourceName, serverPath, customFileName string
	if v := r.FormValue("destinations"); v != "" {
		_ = json.Unmarshal([]byte(v), &destinations)
	}
	destPath = strings.TrimSpace(r.FormValue("dest_path"))
	sourceName = strings.TrimSpace(r.FormValue("source_name"))
	customFileName = strings.TrimSpace(r.FormValue("custom_file_name"))
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
		// Go's fh.Filename strips directory for security, so extract raw filename with path from header
		rawName := fh.Filename
		if cd := fh.Header.Get("Content-Disposition"); cd != "" {
			if extracted := extractFilename(cd); extracted != "" {
				rawName = extracted
			}
		}
		rel := strings.TrimPrefix(rawName, "/")
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

	// Nếu không có bất kỳ nguồn nào (file upload hay đường dẫn server) -> lỗi.
	if len(files) == 0 && serverPath == "" {
		http.Error(w, "Chưa có nguồn dữ liệu nào được chọn (file/thư mục hoặc đường dẫn server).", http.StatusBadRequest)
		return
	}

	// Chọn nguồn nén: ưu tiên nén cả thư mục nếu chọn thư mục.
	// Nếu tmpRoot chỉ chứa 1 thư mục con duy nhất (trường hợp chọn 1 thư mục qua webkitdirectory
	// hoặc serverPath là thư mục) thì nén chính thư mục đó để giữ tên thư mục gốc,
	// thay vì nén tmpRoot ngẫu nhiên (manual_upload_bkup_...).
	var sourcePath string
	if entries, err := os.ReadDir(tmpRoot); err == nil && len(entries) == 1 && entries[0].IsDir() {
		// Chỉ có 1 thư mục gốc -> nén cả thư mục đó
		sourcePath = filepath.Join(tmpRoot, entries[0].Name())
	} else if len(files) == 1 && serverPath == "" {
		rawName := files[0].Filename
		if cd := files[0].Header.Get("Content-Disposition"); cd != "" {
			if extracted := extractFilename(cd); extracted != "" {
				rawName = extracted
			}
		}
		rel := strings.TrimPrefix(rawName, "/")
		rel = strings.ReplaceAll(rel, "\\", "/")
		sourcePath = filepath.Join(tmpRoot, rel)
	} else {
		sourcePath = tmpRoot
	}

	// Gọi logic tạo backup tới các đích đã chọn.
	msg, okDests, err := createBackupToDests(sourcePath, destinations, destPath, sourceName, customFileName)
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

// getDefaultServerBackupDir trả về thư mục mặc định có thể ghi được cho Server
// Ưu tiên /var/backups, fallback /tmp/backups hoặc ~/backups
func getDefaultServerBackupDir() string {
	if unixWritable("/var/backups") {
		return "/var/backups"
	}
	if home, err := os.UserHomeDir(); err == nil {
		cand := filepath.Join(home, "backups")
		if err := os.MkdirAll(cand, 0755); err == nil && unixWritable(cand) {
			return cand
		}
	}
	dir := "/tmp/backups"
	_ = os.MkdirAll(dir, 0755)
	return dir
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

// nativePickerHandler mở hộp thoại chọn thư mục/file NATIVE của hệ điều hành (Ubuntu/GNOME)
// bằng công cụ zenity trên chính máy chủ. Do backend chạy trên cùng máy với người dùng
// (DISPLAY của user hiện tại), cửa sổ file hệ điều hành sẽ hiện lên màn hình.
//
//	GET /api/native-picker?start=/home/ddnguyen&kind=dir|file
//
// kind=dir  → chọn THƯ MỤC (mặc định)
// kind=file → chọn FILE hoặc thư mục (GTK cho phép chọn cả hai ở chế độ mở)
//
// Trả về: {"path":"/home/ddnguyen/backups"} hoặc {"cancelled":true} khi user bấm Hủy.
func nativePickerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Chỉ hỗ trợ GET", http.StatusMethodNotAllowed)
		return
	}
	start := strings.TrimSpace(r.URL.Query().Get("start"))
	if start == "" {
		start = "/home/ddnguyen"
	}
	info, err := os.Stat(start)
	if err != nil || !info.IsDir() {
		start = "/home/ddnguyen" // fallback nếu khởi điểm không hợp lệ
	}

	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	if kind == "" {
		kind = "dir"
	}

	zenityPath, err := exec.LookPath("zenity")
	if err != nil {
		http.Error(w, "Không tìm thấy zenity. Cài bằng: sudo apt install zenity", http.StatusNotImplemented)
		return
	}

	// Chạy zenity: cửa sổ chọn thư mục/file native (GTK) hiện lên trên màn hình user.
	args := []string{"--file-selection", "--filename=" + start + "/", "--title=Chọn thư mục lưu backup trên Server"}
	if kind == "dir" {
		args = append(args, "--directory")
	}
	cmd := exec.Command(zenityPath, args...)
	// Editor cho đời: chuyển DISPLAY/XAUTH từ env hiện tại của backend.
	out, runErr := cmd.Output()
	if runErr != nil {
		// zenity exit 1 = user hủy
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"cancelled": true})
			return
		}
		// exit 5 = không kết nối được DISPLAY
		log.Printf("⚠️ zenity lỗi: %v", runErr)
		http.Error(w, "Không mở được cửa sổ chọn: "+runErr.Error(), http.StatusInternalServerError)
		return
	}

	path := strings.TrimSpace(string(out))
	if path == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"cancelled": true})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"path": path})
}

// findDirHandler tìm kiếm thư mục theo tên trong các thư mục phổ biến,
// trả về danh sách đường dẫn đầy đủ khớp với tên được cung cấp.
func findDirHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Chỉ hỗ trợ GET", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "Thiếu tham số name", http.StatusBadRequest)
		return
	}

	// Các thư mục gốc để tìm kiếm
	searchRoots := []string{
		"/home",
		"/var",
		"/tmp",
		"/opt",
		"/srv",
	}

	type foundDir struct {
		Path  string `json:"path"`
		Write bool   `json:"write"`
	}
	results := []foundDir{}

	for _, root := range searchRoots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		// Tìm ở độ sâu 2 (vd /home/user/backups)
		entries, _ := os.ReadDir(root)
		for _, e1 := range entries {
			if !e1.IsDir() {
				continue
			}
			p1 := filepath.Join(root, e1.Name())
			if strings.EqualFold(e1.Name(), name) {
				results = append(results, foundDir{Path: p1, Write: unixWritable(p1)})
			}
			// Độ sâu 1 thêm
			sub1, _ := os.ReadDir(p1)
			for _, e2 := range sub1 {
				if !e2.IsDir() {
					continue
				}
				p2 := filepath.Join(p1, e2.Name())
				if strings.EqualFold(e2.Name(), name) {
					results = append(results, foundDir{Path: p2, Write: unixWritable(p2)})
				}
				// Độ sâu 2
				sub2, _ := os.ReadDir(p2)
				for _, e3 := range sub2 {
					if !e3.IsDir() {
						continue
					}
					p3 := filepath.Join(p2, e3.Name())
					if strings.EqualFold(e3.Name(), name) {
						results = append(results, foundDir{Path: p3, Write: unixWritable(p3)})
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":    name,
		"results": results,
	})
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
	cmd := exec.Command("rclone", "copyto", "--log-level", "ERROR", src, remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func deleteFromDrive(remote string) error {
	// Dùng deletefile cho file đơn lẻ (chính xác hơn delete)
	cmd := exec.Command("rclone", "deletefile", "--log-level", "ERROR", remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: thử delete nếu deletefile không hỗ trợ (bản rclone cũ)
		if strings.Contains(strings.ToLower(string(output)), "unknown") {
			cmd2 := exec.Command("rclone", "delete", "--log-level", "ERROR", remote)
			output2, err2 := cmd2.CombinedOutput()
			if err2 != nil {
				return fmt.Errorf("%v (%s)", err2, strings.TrimSpace(string(output2)))
			}
			return nil
		}
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
	cmd := exec.Command("rclone", "copyto", "--log-level", "ERROR", src, remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func deleteFromNas(remote string) error {
	cmd := exec.Command("rclone", "deletefile", "--log-level", "ERROR", remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "unknown") {
			cmd2 := exec.Command("rclone", "delete", "--log-level", "ERROR", remote)
			output2, err2 := cmd2.CombinedOutput()
			if err2 != nil {
				return fmt.Errorf("%v (%s)", err2, strings.TrimSpace(string(output2)))
			}
			return nil
		}
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// extractFilename parses Content-Disposition header to get filename with path preserved
// (Go's FileHeader.Filename strips directory for security)
func extractFilename(cd string) string {
	lower := strings.ToLower(cd)
	idx := strings.Index(lower, "filename=")
	if idx == -1 {
		return ""
	}
	rest := cd[idx+len("filename="):]
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	if strings.HasPrefix(rest, "\"") {
		end := strings.Index(rest[1:], "\"")
		if end != -1 {
			return rest[1 : 1+end]
		}
		return rest[1:]
	}
	if idx := strings.Index(rest, ";"); idx != -1 {
		return strings.TrimSpace(rest[:idx])
	}
	return strings.TrimSpace(rest)
}
