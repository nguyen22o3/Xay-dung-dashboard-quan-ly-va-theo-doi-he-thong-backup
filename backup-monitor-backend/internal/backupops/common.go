// Package backupops implements the root-only backup operations used by the
// dashboard and shell jobs. No Python interpreter or arbitrary command input.
package backupops

import (
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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const BinaryPath = "/usr/local/libexec/backup-monitor/backup-manager"
const Version = "backup-manager-go-v5"
const RetentionDays = 14
const LayoutMarker = "# backup-monitor daily layout v1"

var Categories = []string{"site", "database", "panel"}
var ScriptNames = []string{"backup-site.sh", "backup-database.sh", "backup-panel.sh", "auto_backup.sh", "cleanup-panel-backups.sh", "check_integrity.sh", "realtime_monitor.sh", "backup-monitor-run.sh"}
var LogNames = []string{"backup-site.log", "backup-database.log", "backup-panel.log", "auto_backup.log", "cleanup-panel-backups.log", "check_integrity.log", "backup-monitor-runs.log"}

type Settings struct {
	BackupRoot      string     `json:"backupRoot"`
	ScriptsDir      string     `json:"scriptsDir"`
	LogsDir         string     `json:"logsDir"`
	DriveRemote     string     `json:"driveRemote"`
	DriveFolder     string     `json:"driveFolder"`
	DriveHistoryLog string     `json:"driveHistoryLog"`
	SiteSource      SourceSpec `json:"siteSource,omitzero"`
	DatabaseSource  SourceSpec `json:"databaseSource,omitzero"`
	PanelSource     SourceSpec `json:"panelSource,omitzero"`
}

func DefaultSettings() Settings {
	return Settings{BackupRoot: "/www/backup", ScriptsDir: "/root/scripts", LogsDir: "/root", DriveRemote: "gdrive", DriveFolder: "Backup", DriveHistoryLog: "/var/log/aapanel_backup.log"}
}

type Request struct {
	Operation       string          `json:"operation"`
	Config          *Settings       `json:"config,omitempty"`
	ExpectedVersion string          `json:"expectedVersion,omitempty"`
	PreviewToken    string          `json:"previewToken,omitempty"`
	Schedule        *ScheduleChange `json:"schedule,omitempty"`
	Purpose         string          `json:"purpose,omitempty"`
	Location        string          `json:"location,omitempty"`
	Path            string          `json:"path,omitempty"`
	Remote          string          `json:"remote,omitempty"`
}
type State struct {
	Config             Settings              `json:"config"`
	Version            string                `json:"version"`
	Remotes            []string              `json:"remotes"`
	LocalRetentionDays int                   `json:"localRetentionDays"`
	DriveRetentionDays int                   `json:"driveRetentionDays"`
	AllowedRoots       map[string][]string   `json:"allowedRoots"`
	Ready              bool                  `json:"ready"`
	Applied            bool                  `json:"applied"`
	RecoveryDir        string                `json:"recoveryDir,omitempty"`
	Warnings           []string              `json:"warnings"`
	Sources            map[string]SourceSpec `json:"sources"`
}
type Runner func(args []string, input []byte) ([]byte, error)
type Controller struct {
	ConfigPath, RcloneConfig, RecoveryRoot, Binary string
	Defaults                                       Settings
	Roots                                          map[string][]string
	Locks                                          []string
	Run                                            Runner
	RestartMonitor                                 func(Settings, Settings) ([]string, error)
	// Injectable for failure/rollback tests. Production always uses AtomicWrite.
	Write func(string, []byte, os.FileMode) error
}

func NewController() *Controller {
	locks := []string{"/root/.backup-monitor-system-config.lock", "/root/.backup-monitor-crontab.lock", "/run/backup-monitor-layout.lock", "/run/backup-monitor-versions.lock", "/run/backup-monitor-site.lock", "/run/backup-monitor-database.lock", "/run/backup-monitor-drive.lock", "/run/backup-monitor-panel-retention.lock"}
	for _, name := range []string{"backup-site", "backup-database", "backup-panel", "drive-sync", "cleanup-panel", "integrity-check"} {
		locks = append(locks, "/root/.backup-monitor-job-"+name+".lock")
	}
	return &Controller{ConfigPath: "/root/backup-monitor/system-config.json", RcloneConfig: "/root/.config/rclone/rclone.conf", RecoveryRoot: "/root/backup-layout-migrations", Binary: BinaryPath,
		Defaults: DefaultSettings(), Roots: map[string][]string{"backup": {"/"}, "scripts": {"/"}, "logs": {"/"}, "source-site": {"/"}, "source-database": {"/"}, "source-panel": {"/"}},
		Locks: locks, Run: runCommand, Write: AtomicWrite, RestartMonitor: restartMonitor}
}
func runCommand(args []string, input []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	if input != nil {
		command.Stdin = strings.NewReader(string(input))
	}
	out, err := command.Output()
	if err != nil {
		return nil, errors.New("Không hoàn tất được lệnh hệ thống: " + filepath.Base(args[0]))
	}
	return out, nil // Never expose stderr or credential-bearing script contents.
}
func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func Digest(path string) (string, error) {
	if err := SafeParents(path); err != nil {
		return "", err
	}
	file, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
func readRegular(path string) ([]byte, error) {
	if err := SafeParents(path); err != nil {
		return nil, err
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
func SafeParents(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Không chấp nhận liên kết tượng trưng: %s", p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
	}
	return nil
}
func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimRight(root, string(filepath.Separator))+string(filepath.Separator))
}
func exists(path string) bool { _, err := os.Lstat(path); return err == nil }
func nearest(path string) (os.FileInfo, error) {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err == nil {
			return info, nil
		}
		if !os.IsNotExist(err) || filepath.Dir(p) == p {
			return nil, err
		}
	}
}
func mkdirPrivate(path string) error {
	if err := SafeParents(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return SafeParents(path)
}
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := SafeParents(path); err != nil {
		return err
	}
	if err := mkdirPrivate(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".backup-manager-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = SafeParents(path); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func copyExclusive(source, target string) error {
	data, err := readRegular(source)
	if err != nil {
		return err
	}
	if err = SafeParents(target); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		_ = os.Remove(target)
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		_ = os.Remove(target)
		return err
	}
	if err = os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}
func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data[:])
}
func DecodeRequest(reader io.Reader) (Request, error) {
	var request Request
	decoder := json.NewDecoder(io.LimitReader(reader, 32769))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, errors.New("Dữ liệu cấu hình không hợp lệ")
	}
	if decoder.Decode(new(interface{})) != io.EOF {
		return request, errors.New("Chỉ chấp nhận một yêu cầu JSON")
	}
	return request, nil
}

var localChars = regexp.MustCompile(`^[A-Za-z0-9_./:\\-]+$`)
var remoteChars = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var driveChars = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

func (c *Controller) LocalPath(value, purpose string) error {
	roots, ok := c.Roots[purpose]
	if !ok || !localChars.MatchString(value) || !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) {
		return errors.New("Đường dẫn phải tuyệt đối, chỉ dùng chữ không dấu, số, /, -, _, . và không chứa ..")
	}
	if filepath.Separator != '\\' && (strings.Contains(value, "\\") || strings.Contains(value, ":")) {
		return errors.New("Đường dẫn Linux không chứa dấu \\ hoặc :")
	}
	permitted := false
	for _, root := range roots {
		if within(value, root) {
			permitted = true
		}
	}
	if !permitted {
		return errors.New("Đường dẫn nằm ngoài vùng được phép cho " + purpose)
	}
	if filepath.Separator == '/' {
		for _, root := range []string{"/proc", "/sys", "/dev", "/run"} {
			if within(value, root) {
				return errors.New("Không dùng filesystem hệ thống tạm thời làm nguồn hoặc nơi lưu backup")
			}
		}
	}
	if purpose == "scripts" && (value == "/root" || value == "/opt/backup-monitor") {
		return errors.New("Chọn thư mục script con, ví dụ /root/scripts")
	}
	if err := SafeParents(value); err != nil {
		return err
	}
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		return errors.New("Đường dẫn không phải thư mục")
	}
	if purpose == "scripts" || purpose == "logs" {
		info, err := nearest(value)
		if err != nil {
			return err
		}
		if !privateRootOwned(info) {
			return errors.New("Thư mục script/log phải do root sở hữu và không cho người khác ghi")
		}
	}
	return nil
}
func DrivePath(value string) error {
	if value == "" {
		return nil
	}
	if !driveChars.MatchString(value) || strings.HasPrefix(value, "/") {
		return errors.New("Thư mục Drive dùng chữ không dấu, số, -, _, . và /")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == ".." || part == "." {
			return errors.New("Thư mục Drive không chứa thành phần rỗng, . hoặc ..")
		}
	}
	return nil
}
func (c *Controller) Remotes() ([]string, error) {
	data, err := readRegular(c.RcloneConfig)
	if err != nil {
		return nil, errors.New("Không đọc được cấu hình kết nối rclone")
	}
	result := []string{}
	name := ""
	isDrive := false
	flush := func() {
		if isDrive && remoteChars.MatchString(name) {
			result = append(result, name)
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			name = strings.TrimSpace(line[1 : len(line)-1])
			isDrive = false
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "type" {
			isDrive = strings.TrimSpace(value) == "drive"
		}
	}
	flush()
	sort.Strings(result)
	return result, nil
}
func (c *Controller) Validate(s Settings) error {
	for _, v := range []struct{ value, purpose string }{{s.BackupRoot, "backup"}, {s.ScriptsDir, "scripts"}, {s.LogsDir, "logs"}} {
		if err := c.LocalPath(v.value, v.purpose); err != nil {
			return err
		}
	}
	remotes, err := c.Remotes()
	if err != nil {
		return err
	}
	found := false
	for _, r := range remotes {
		if r == s.DriveRemote {
			found = true
		}
	}
	if !found {
		return errors.New("Chỉ chọn kết nối Google Drive đã cấu hình trong rclone")
	}
	if err = DrivePath(s.DriveFolder); err != nil {
		return err
	}
	if s.DriveFolder == "" {
		return errors.New("Không dùng toàn bộ Drive làm thư mục backup")
	}
	if within(s.ScriptsDir, s.BackupRoot) || within(s.LogsDir, s.BackupRoot) {
		return errors.New("Thư mục script/log phải tách khỏi thư mục backup")
	}
	if s.DriveHistoryLog != "/var/log/aapanel_backup.log" && s.DriveHistoryLog != filepath.Join(s.LogsDir, "aapanel_backup.log") {
		return errors.New("Đường dẫn nhật ký Drive không hợp lệ")
	}
	for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
		// Also protect default sources when the form only changes storage.
		if within(effectiveSource(s, id).Path, s.BackupRoot) || within(s.BackupRoot, effectiveSource(s, id).Path) {
			return errors.New("Nguồn dữ liệu phải tách khỏi nơi lưu backup")
		}
		if spec := configuredSource(s, id); spec != (SourceSpec{}) {
			if err := c.ValidateSource(s, id, spec); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *Controller) Active() (Settings, string, error) {
	if err := SafeParents(c.ConfigPath); err != nil {
		return Settings{}, "", err
	}
	if !exists(c.ConfigPath) {
		return c.Defaults, hashBytes([]byte("backup-system-default-v1")), nil
	}
	data, err := readRegular(c.ConfigPath)
	if err != nil {
		return Settings{}, "", err
	}
	var cfg Settings
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, "", errors.New("Cấu hình máy chủ không hợp lệ")
	}
	if err = c.Validate(cfg); err != nil {
		return cfg, "", err
	}
	return cfg, hashBytes(data), nil
}
func (c *Controller) Get() (State, error) {
	cfg, version, err := c.Active()
	if err != nil {
		return State{}, err
	}
	remotes, err := c.Remotes()
	if err != nil {
		return State{}, err
	}
	info, err := os.Stat(c.Binary)
	ready := err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0100 != 0 && SafeParents(c.Binary) == nil
	return State{Config: cfg, Version: version, Remotes: remotes, LocalRetentionDays: RetentionDays, DriveRetentionDays: RetentionDays, AllowedRoots: c.Roots, Ready: ready, Warnings: []string{}, Sources: sourceSelections(cfg)}, nil
}

type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type FolderList struct {
	Path    string   `json:"path"`
	Exists  bool     `json:"exists"`
	Parent  *string  `json:"parent"`
	Entries []Folder `json:"entries"`
}

func (c *Controller) List(r Request) (FolderList, error) {
	result := FolderList{Path: r.Path, Entries: []Folder{}}
	_, ok := c.Roots[r.Purpose]
	if !ok {
		return result, errors.New("Loại thư mục không hợp lệ")
	}
	if r.Location == "local" {
		// Browsing is independent of the selected task and has no write side
		// effects. Selection/apply still uses LocalPath and ValidateSource.
		if !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path {
			return result, errors.New("Đường dẫn duyệt phải tuyệt đối và không chứa ..")
		}
		if err := SafeParents(r.Path); err != nil {
			return result, err
		}
		parent := filepath.Dir(r.Path)
		if parent != r.Path {
			result.Parent = &parent
		}
		info, err := os.Stat(r.Path)
		if os.IsNotExist(err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		if !info.IsDir() {
			return result, errors.New("Đường dẫn duyệt không phải thư mục")
		}
		entries, err := os.ReadDir(r.Path)
		if os.IsNotExist(err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		result.Exists = true
		for _, entry := range entries {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				result.Entries = append(result.Entries, Folder{entry.Name(), filepath.Join(r.Path, entry.Name())})
			}
		}
		return result, nil
	}
	if strings.HasPrefix(r.Purpose, "source-") {
		return result, errors.New("Chỉ chọn thư mục nguồn trên máy chủ")
	}
	if r.Location != "drive" {
		return result, errors.New("Nơi lưu trữ không hợp lệ")
	}
	if err := c.validateRemote(r.Remote); err != nil {
		return result, err
	}
	if err := DrivePath(r.Path); err != nil {
		return result, err
	}
	out, err := c.Run(c.rcloneArgs("lsf", "--dirs-only", r.Remote+":"+r.Path), nil)
	if err != nil {
		return result, errors.New("Không đọc được thư mục Drive; kiểm tra kết nối hoặc tạo thư mục trước")
	}
	result.Exists = true
	parent := ""
	if index := strings.LastIndex(r.Path, "/"); index >= 0 {
		parent = r.Path[:index]
	}
	result.Parent = &parent
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSuffix(strings.TrimSpace(line), "/")
		if name != "" && DrivePath(name) == nil && !strings.Contains(name, "/") {
			p := name
			if r.Path != "" {
				p = r.Path + "/" + name
			}
			result.Entries = append(result.Entries, Folder{name, p})
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Name < result.Entries[j].Name })
	return result, nil
}
func (c *Controller) validateRemote(remote string) error {
	remotes, err := c.Remotes()
	if err != nil {
		return err
	}
	for _, r := range remotes {
		if r == remote {
			return nil
		}
	}
	return errors.New("Kết nối Drive không hợp lệ")
}
func (c *Controller) rcloneArgs(args ...string) []string {
	return append(append([]string{"/usr/bin/rclone"}, args...), "--config", c.RcloneConfig, "--contimeout", "5s", "--timeout", "15s")
}
func (c *Controller) Create(r Request) error {
	if strings.HasPrefix(r.Purpose, "source-") {
		return errors.New("Thư mục nguồn phải tồn tại; không tạo thư mục dữ liệu từ dashboard")
	}
	if r.Location == "local" {
		if err := c.LocalPath(r.Path, r.Purpose); err != nil {
			return err
		}
		return mkdirPrivate(r.Path)
	}
	if r.Location != "drive" {
		return errors.New("Nơi lưu trữ không hợp lệ")
	}
	if err := c.validateRemote(r.Remote); err != nil {
		return err
	}
	if err := DrivePath(r.Path); err != nil {
		return err
	}
	if r.Path == "" {
		return errors.New("Thư mục Drive không hợp lệ")
	}
	_, err := c.Run(c.rcloneArgs("mkdir", r.Remote+":"+r.Path), nil)
	if err != nil {
		return errors.New("Không tạo được thư mục Drive")
	}
	return nil
}
func (c *Controller) Dispatch(r Request) (interface{}, error) {
	switch r.Operation {
	case "get":
		return c.Get()
	case "list":
		return c.List(r)
	case "create":
		if err := c.Create(r); err != nil {
			return nil, err
		}
		return map[string]interface{}{"created": true, "path": r.Path}, nil
	case "preview":
		p, err := c.Proposal(r)
		if err != nil {
			return nil, err
		}
		return p.Summary, nil
	case "apply":
		return c.Apply(r)
	}
	return nil, errors.New("Thao tác không hợp lệ")
}
