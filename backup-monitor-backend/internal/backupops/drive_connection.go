package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type DriveConnection struct {
	Remote       string `json:"remote"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Token        string `json:"token"`
}
type DriveConnectionStatus struct {
	Remotes         []string `json:"remotes"`
	RcloneInstalled bool     `json:"rcloneInstalled"`
}

func (c *Controller) DriveStatus() (DriveConnectionStatus, error) {
	remotes, err := c.Remotes()
	info, statErr := os.Stat("/usr/bin/rclone")
	return DriveConnectionStatus{Remotes: remotes, RcloneInstalled: statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0}, err
}

var oauthClientID = regexp.MustCompile(`^[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$`)
var oauthClientSecret = regexp.MustCompile(`^[A-Za-z0-9_-]{8,256}$`)

// Only explicit Google Drive fields are accepted; never import arbitrary
// rclone backends, paths, commands or environment-variable configuration.
func (c *Controller) ConnectDrive(drive *DriveConnection) (DriveConnectionStatus, error) {
	if drive == nil || !remoteChars.MatchString(drive.Remote) || len(drive.Remote) > 64 ||
		!oauthClientID.MatchString(drive.ClientID) || !oauthClientSecret.MatchString(drive.ClientSecret) {
		return DriveConnectionStatus{}, errors.New("Tên kết nối hoặc thông tin OAuth Google Drive không hợp lệ")
	}
	if len(drive.Token) > 16384 {
		return DriveConnectionStatus{}, errors.New("Token Google Drive quá lớn")
	}
	var token map[string]interface{}
	if json.Unmarshal([]byte(drive.Token), &token) != nil {
		return DriveConnectionStatus{}, errors.New("Token phải là JSON do rclone authorize trả về")
	}
	refresh, ok := token["refresh_token"].(string)
	if !ok || strings.TrimSpace(refresh) == "" {
		return DriveConnectionStatus{}, errors.New("Token phải có refresh_token")
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return DriveConnectionStatus{}, errors.New("Token không hợp lệ")
	}
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return DriveConnectionStatus{}, err
	}
	defer releaseAll(locks)
	if err := SafeParents(c.RcloneConfig); err != nil {
		return DriveConnectionStatus{}, err
	}
	original, err := readRegular(c.RcloneConfig)
	if err != nil && !os.IsNotExist(err) {
		return DriveConnectionStatus{}, errors.New("Không đọc được cấu hình rclone hiện tại; không thay đổi tập tin")
	}
	if strings.Contains(string(original), "RCLONE_ENCRYPT_V0:") {
		return DriveConnectionStatus{}, errors.New("Cấu hình rclone đang mã hóa; không tự ghi đè")
	}
	for _, line := range strings.Split(string(original), "\n") {
		if strings.TrimSpace(line) == "["+drive.Remote+"]" {
			return DriveConnectionStatus{}, errors.New("Tên kết nối đã tồn tại; chọn tên khác. Kết nối cũ được giữ nguyên")
		}
	}
	section := fmt.Sprintf("\n[%s]\ntype = drive\nclient_id = %s\nclient_secret = %s\nscope = drive\ntoken = %s\n", drive.Remote, drive.ClientID, drive.ClientSecret, encoded)
	if err := mkdirPrivate(filepath.Dir(c.RcloneConfig)); err != nil {
		return DriveConnectionStatus{}, err
	}
	// Test the new account with a temporary private configuration. Existing
	// tokens are untouched until authorization is proven usable.
	test, err := os.CreateTemp(filepath.Dir(c.RcloneConfig), ".drive-connection-*")
	if err != nil {
		return DriveConnectionStatus{}, errors.New("Không tạo được cấu hình kiểm tra")
	}
	defer os.Remove(test.Name())
	_, writeErr := test.WriteString(section)
	closeErr := test.Close()
	if writeErr != nil || closeErr != nil {
		return DriveConnectionStatus{}, errors.New("Không ghi được cấu hình kiểm tra")
	}
	if _, err := c.Run([]string{"/usr/bin/rclone", "about", drive.Remote + ":", "--config", test.Name(), "--json", "--timeout", "15s", "--contimeout", "5s"}, nil); err != nil {
		return DriveConnectionStatus{}, errors.New("Không xác thực được Google Drive; kiểm tra client ID, client secret, token và mạng máy chủ")
	}
	// Keep the refreshed token saved by rclone during the connectivity check.
	verified, err := readRegular(test.Name())
	if err != nil {
		return DriveConnectionStatus{}, errors.New("Không đọc được cấu hình đã kiểm tra")
	}
	if len(original) > 0 {
		backup := c.RcloneConfig + ".before-dashboard-connect"
		if !exists(backup) {
			if err := copyExclusive(c.RcloneConfig, backup); err != nil {
				return DriveConnectionStatus{}, errors.New("Không tạo được bản dự phòng cấu hình rclone")
			}
		}
	}
	if err := c.Write(c.RcloneConfig, append(append(original, '\n'), verified...), 0600); err != nil {
		return DriveConnectionStatus{}, errors.New("Không lưu được kết nối Google Drive")
	}
	return c.DriveStatus()
}
