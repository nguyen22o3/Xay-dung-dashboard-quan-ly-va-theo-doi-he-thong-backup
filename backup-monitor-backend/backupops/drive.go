package backupops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const legacyDriveArchiveIncludes = `--include '*.tar.gz' --include '*.sql.gz' --include '*.zip'`

// Ordered, first-match-wins rules: private metadata is never uploaded and all
// non-archive files remain excluded. Do not mix include/exclude flag families.
const DriveArchiveFilters = `--filter '- .backup-monitor-versions.json' --filter '+ *.tar.gz' --filter '+ *.sql.gz' --filter '+ *.zip' --filter '- **'`

const driveResultMarker = "# backup-monitor drive exit status v1"
const driveResultEcho = `echo "==== HOAN TAT voi trang thai: $STATUS ===="`
const driveResultExit = driveResultMarker + `
if [[ "$STATUS" != "Successful" ]]; then
    exit 1
fi
exit 0
`

func PatchDriveExitStatus(source string) (string, error) {
	if strings.Contains(source, driveResultMarker) {
		if strings.HasSuffix(source, driveResultEcho+"\n"+driveResultExit) {
			return source, nil
		}
		return "", errors.New("Unexpected Drive result handling")
	}
	if !strings.HasSuffix(strings.TrimSpace(source), driveResultEcho) || strings.Count(source, driveResultEcho) != 1 {
		return "", errors.New("Unexpected Drive completion line; refusing to append an unreachable exit")
	}
	// Keep the existing history path and record format. Failure to persist the
	// result is a failed run too, not a successful exit after an echo error.
	prefix := `echo "$START_DATE|$START_TIME|$DURATION|$STATUS" >> `
	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")
	count := 0
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			count++
			lines[i] = "if ! " + line + `; then
    STATUS="Failed"
    echo 'Cannot record Drive execution history' >&2
fi`
		}
	}
	if count != 1 || !strings.Contains(source, `STATUS="Successful"`) {
		return "", errors.New("Unexpected Drive history or successful status")
	}
	return strings.Join(lines, "\n") + "\n" + driveResultExit, nil
}

func PatchDriveReliability(source string) (string, error) {
	updated, err := PatchDriveArchiveOnly(source)
	if err != nil {
		return "", err
	}
	return PatchDriveExitStatus(updated)
}

// Repair only auto_backup.sh. No remote access, backup execution, archive
// rotation, cron rewrite or rclone configuration change is performed.
func (c *Controller) RepairDriveScript(apply bool, out io.Writer) (resultErr error) {
	if apply {
		locks, err := acquireAll(c.Locks)
		if err != nil {
			return err
		}
		defer releaseAll(locks)
	}
	cfg, _, err := c.Active()
	if err != nil {
		return err
	}
	if err = c.LocalPath(cfg.ScriptsDir, "scripts"); err != nil {
		return err
	}
	path := filepath.Join(cfg.ScriptsDir, "auto_backup.sh")
	before, err := readRegular(path)
	if err != nil {
		return err
	}
	after, err := PatchDriveReliability(string(before))
	if err != nil {
		return err
	}
	if after == string(before) {
		fmt.Fprintln(out, "Drive filters and exit status already repaired.")
		return nil
	}
	if !apply {
		fmt.Fprintln(out, "DRIVE_SCRIPT_PREVIEW: 1 script; ordered archive filters and truthful exit status; no writes.")
		return nil
	}
	temp, err := os.MkdirTemp("", "drive-script-validate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	candidate := filepath.Join(temp, "auto_backup.sh")
	if err = AtomicWrite(candidate, []byte(after), 0600); err != nil {
		return err
	}
	if _, err = c.Run([]string{"/bin/bash", "-n", candidate}, nil); err != nil {
		return errors.New("Drive script syntax rejected; no script changed")
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-drive-script-")
	if err != nil {
		return err
	}
	if err = AtomicWrite(filepath.Join(recovery, "auto_backup.sh"), before, 0600); err != nil {
		return err
	}
	current, err := readRegular(path)
	if err != nil || !bytes.Equal(current, before) {
		return errors.New("Drive script changed; refusing overwrite")
	}
	defer func() {
		if resultErr == nil {
			return
		}
		current, readErr := readRegular(path)
		if readErr == nil && bytes.Equal(current, before) {
			return
		}
		if readErr != nil || !bytes.Equal(current, []byte(after)) || c.Write(path, before, 0700) != nil {
			resultErr = fmt.Errorf("Drive script rollback needs review; recovery=%s", recovery)
			return
		}
		resultErr = fmt.Errorf("Drive script update failed; original restored; recovery=%s", recovery)
	}()
	if err = c.Write(path, []byte(after), 0700); err != nil {
		return err
	}
	current, err = readRegular(path)
	if err != nil || string(current) != after {
		return errors.New("Drive script readback failed")
	}
	if _, err = c.Run([]string{"/bin/bash", "-n", path}, nil); err != nil {
		return errors.New("Installed Drive script syntax rejected")
	}
	fmt.Fprintf(out, "DRIVE_SCRIPT_REPAIRED: ordered archive filters; failed runs exit nonzero; recovery=%s; no backup, cleanup or cron run.\n", recovery)
	return nil
}
