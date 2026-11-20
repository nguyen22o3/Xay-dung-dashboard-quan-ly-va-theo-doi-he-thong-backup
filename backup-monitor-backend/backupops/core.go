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
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

const BinaryPath = "/usr/local/libexec/backup-monitor/backup-manager"
const Version = "backup-manager-go-v26"
const VersionMetadataDir = "/root/backup-monitor/metadata/versions"
const RetentionDays = 14
const LayoutMarker = "# backup-monitor daily layout v1"

var Categories = []string{"site", "database", "panel"}
var ScriptNames = []string{"backup-site.sh", "backup-database.sh", "backup-panel.sh", "auto_backup.sh", "cleanup-panel-backups.sh", "check_integrity.sh", "realtime_monitor.sh", "backup-monitor-run.sh"}
var LogNames = []string{"backup-site.log", "backup-database.log", "backup-panel.log", "auto_backup.log", "cleanup-panel-backups.log", "check_integrity.log", "backup-monitor-runs.log"}

type Settings struct {
	fileKey         string                  // Effective runtime namespace; never serialized in configuration.
	CronLocations   map[string]CronLocation `json:"cronLocations,omitempty"`
	BackupRoot      string                  `json:"backupRoot"`
	ScriptsDir      string                  `json:"scriptsDir"`
	LogsDir         string                  `json:"logsDir"`
	DriveRemote     string                  `json:"driveRemote"`
	DriveFolder     string                  `json:"driveFolder"`
	DriveHistoryLog string                  `json:"driveHistoryLog"`
	SiteSource      SourceSpec              `json:"siteSource,omitzero"`
	DatabaseSource  SourceSpec              `json:"databaseSource,omitzero"`
	PanelSource     SourceSpec              `json:"panelSource,omitzero"`
}

func DefaultSettings() Settings {
	return Settings{BackupRoot: "/www/backup", ScriptsDir: "/root/scripts", LogsDir: "/root", DriveRemote: "gdrive", DriveFolder: "Backup", DriveHistoryLog: "/var/log/aapanel_backup.log"}
}

type Request struct {
	// Internal planning hint; never accepted from JSON or used to skip cron dependencies.
	creatingTask    string
	Operation       string           `json:"operation"`
	Config          *Settings        `json:"config,omitempty"`
	ExpectedVersion string           `json:"expectedVersion,omitempty"`
	PreviewToken    string           `json:"previewToken,omitempty"`
	Schedule        *ScheduleChange  `json:"schedule,omitempty"`
	Script          *ScriptRequest   `json:"script,omitempty"`
	Log             *LogRequest      `json:"log,omitempty"`
	Task            *TaskRequest     `json:"task,omitempty"`
	Restore         *RestoreRequest  `json:"restore,omitempty"`
	Purpose         string           `json:"purpose,omitempty"`
	Location        string           `json:"location,omitempty"`
	Path            string           `json:"path,omitempty"`
	Remote          string           `json:"remote,omitempty"`
	Drive           *DriveConnection `json:"drive,omitempty"`
}
type State struct {
	Installation                   InstallationPaths     `json:"installation"`
	SupportsManagedScriptSelection bool                  `json:"supportsManagedScriptSelection"`
	SupportsFlatCronFiles          bool                  `json:"supportsFlatCronFiles"`
	SupportsCronBackupMigration    bool                  `json:"supportsCronBackupMigration"`
	SupportsCronIsolation          bool                  `json:"supportsCronIsolation"`
	SupportsCronDirectories        bool                  `json:"supportsCronDirectories"`
	SupportsMultipleSchedules      bool                  `json:"supportsMultipleSchedules"`
	Config                         Settings              `json:"config"`
	Version                        string                `json:"version"`
	Remotes                        []string              `json:"remotes"`
	LocalRetentionDays             int                   `json:"localRetentionDays"`
	DriveRetentionDays             int                   `json:"driveRetentionDays"`
	AllowedRoots                   map[string][]string   `json:"allowedRoots"`
	Ready                          bool                  `json:"ready"`
	Applied                        bool                  `json:"applied"`
	RecoveryDir                    string                `json:"recoveryDir,omitempty"`
	Warnings                       []string              `json:"warnings"`
	Sources                        map[string]SourceSpec `json:"sources"`
}
type Runner func(args []string, input []byte) ([]byte, error)
type Controller struct {
	Installation      InstallationPaths
	InstallationError error
	LocatorPath       string
	// Only allowlisted CLI runtime operations may set this selector.
	CronSelector                                   string
	ConfigPath, RcloneConfig, RecoveryRoot, Binary string
	RotationMarkers                                string
	VersionMetadataDir                             string
	Defaults                                       Settings
	Roots                                          map[string][]string
	Locks                                          []string
	Run                                            Runner
	RestartMonitor                                 func(Settings, Settings) ([]string, error)
	// Injectable for failure/rollback tests. Production always uses AtomicWrite.
	Write func(string, []byte, os.FileMode) error
}

func NewController() *Controller {
	installation, installationErr := loadInstallation(InstallationFile)
	locks := []string{installation.LockPrefix + "system-config.lock", installation.LockPrefix + "crontab.lock", "/run/backup-monitor-layout.lock", "/run/backup-monitor-versions.lock", "/run/backup-monitor-site.lock", "/run/backup-monitor-database.lock", "/run/backup-monitor-drive.lock", "/run/backup-monitor-panel-retention.lock"}
	for _, name := range []string{"backup-site", "backup-database", "backup-panel", "drive-sync", "cleanup-panel", "integrity-check"} {
		locks = append(locks, installation.LockPrefix+"job-"+name+".lock")
	}
	defaults := DefaultSettings()
	if installation.DataDir != "" {
		defaults.ScriptsDir, defaults.LogsDir = path.Join(installation.DataDir, "scripts"), path.Join(installation.DataDir, "logs")
		defaults.DriveHistoryLog = path.Join(defaults.LogsDir, "aapanel_backup.log")
	}
	return &Controller{Installation: installation, InstallationError: installationErr, LocatorPath: InstallationFile, ConfigPath: path.Join(installation.ConfigDir, "system-config.json"), RcloneConfig: installation.RcloneConfig, RecoveryRoot: installation.RecoveryRoot, Binary: BinaryPath,
		RotationMarkers: "/run/backup-monitor-rotations", VersionMetadataDir: installation.MetadataDir, Defaults: defaults, Roots: map[string][]string{"backup": {"/"}, "scripts": {"/"}, "logs": {"/"}, "source-site": {"/"}, "source-database": {"/"}, "source-panel": {"/"}},
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
	decoder := json.NewDecoder(io.LimitReader(reader, 1024*1024+1))
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
	if os.IsNotExist(err) {
		return []string{}, nil
	}
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
	if err := c.validateCronLocations(s); err != nil {
		return err
	}
	if c.VersionMetadataDir != "" && (within(c.VersionMetadataDir, s.BackupRoot) || within(s.BackupRoot, c.VersionMetadataDir)) {
		return errors.New("Kho metadata quản lý phải tách khỏi thư mục backup")
	}
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
	if s.DriveHistoryLog != "/var/log/aapanel_backup.log" && s.DriveHistoryLog != logPath(s, "aapanel_backup.log") {
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
	if c.InstallationError != nil {
		return Settings{}, "", c.InstallationError
	}
	if c.LocatorPath == InstallationFile {
		current, err := loadInstallation(InstallationFile)
		if err != nil {
			return Settings{}, "", err
		}
		if current != c.Installation {
			return Settings{}, "", errors.New("Vị trí cài đặt vừa thay đổi; hãy chạy lại thao tác")
		}
	}
	if err := SafeParents(c.ConfigPath); err != nil {
		return Settings{}, "", err
	}
	if !exists(c.ConfigPath) {
		cfg, err := c.scopeSettings(c.Defaults)
		return cfg, hashBytes([]byte("backup-system-default-v1")), err
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
	cfg, err = c.scopeSettings(cfg)
	return cfg, hashBytes(data), err
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
	return State{Installation: c.InstallationPaths(), SupportsManagedScriptSelection: true, SupportsFlatCronFiles: true, SupportsCronBackupMigration: true, SupportsCronIsolation: true, SupportsCronDirectories: true, SupportsMultipleSchedules: true, Config: cfg, Version: version, Remotes: remotes, LocalRetentionDays: RetentionDays, DriveRetentionDays: RetentionDays, AllowedRoots: c.Roots, Ready: ready, Warnings: []string{}, Sources: sourceSelections(cfg)}, nil
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
	if c.CronSelector != "" && r.Operation != "get" {
		return nil, errors.New("Cron thực thi chỉ được đọc cấu hình riêng")
	}
	switch r.Operation {
	case "restore-preview":
		return c.RestorePreview(r)
	case "restore-start":
		return c.RestoreStart(r)
	case "restore-status":
		return c.RestoreStatus(r)
	case "restore-check-status":
		entries, err := c.RestoreCheckStatus()
		return map[string]interface{}{"entries": entries}, err
	case "log-read":
		return c.LogPage(r)
	case "task-status", "task-cancel":
		return c.TaskOperation(r)
	case "get":
		return c.Get()
	case "drive-status":
		return c.DriveStatus()
	case "drive-connect":
		return c.ConnectDrive(r.Drive)
	case "script-read":
		return c.ReadScript(r)
	case "script-options":
		return c.ManagedScriptOptions(r)
	case "script-save":
		return c.SaveScript(r)
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

type Preview struct {
	SourceScript    *ManagedScriptOption `json:"sourceScript,omitempty"`
	CronFiles       *CronFilePreview     `json:"cronFiles,omitempty"`
	BackupMigration bool                 `json:"backupMigration"`
	CronDirectories *CronDirectories     `json:"cronDirectories,omitempty"`
	Token           string               `json:"token"`
	Changed         bool                 `json:"changed"`
	FilesToMove     int                  `json:"filesToMove"`
	BytesToMove     int64                `json:"bytesToMove"`
	DaysToMove      int                  `json:"daysToMove"`
	ScriptsToUpdate int                  `json:"scriptsToUpdate"`
	ScriptsToCreate []string             `json:"scriptsToCreate"`
	LogsToMove      int                  `json:"logsToMove"`
	DriveChanged    bool                 `json:"driveChanged"`
	Config          Settings             `json:"config"`
	Version         string               `json:"version"`
	Schedule        *SchedulePreview     `json:"schedule,omitempty"`
	Sources         []SourcePreview      `json:"sources,omitempty"`
}
type scriptChange struct {
	Source, Target             string
	Original, Updated          []byte
	TargetOriginal             []byte
	TargetExistedBeforePreview bool
}
type logChange struct{ Source, Target, Hash string }
type fileCheck struct {
	Relative string
	Size     int64
	Hash     string
}
type dayChange struct {
	Source, Target string
	Files          []fileCheck
}
type Proposal struct {
	Archives      []archiveTransfer
	Ledgers       []ledgerTransfer
	Summary       Preview
	Old, New      Settings
	Scripts       []scriptChange
	Logs          []logChange
	Days          []dayChange
	Cron, NewCron []byte
}

func settingsPairs(old, new Settings) [][2]string {
	pairs := [][2]string{{old.ScriptsDir, new.ScriptsDir}, {old.BackupRoot, new.BackupRoot}, {old.DriveRemote + ":" + old.DriveFolder, new.DriveRemote + ":" + new.DriveFolder}, {old.DriveHistoryLog, new.DriveHistoryLog}}
	for _, name := range ScriptNames {
		pairs = append(pairs, [2]string{scriptPath(old, name), scriptPath(new, name)})
	}
	for _, name := range LogNames {
		pairs = append(pairs, [2]string{logPath(old, name), logPath(new, name)})
	}
	result := [][2]string{}
	changed := false
	for _, p := range pairs {
		if p[0] != p[1] {
			changed = true
		}
	}
	if !changed {
		return result
	}
	// Longest-match identity mappings protect unchanged script/log paths inside
	// the old backup root when only the archive destination changes.
	result = append(result, pairs...)
	if old.LogsDir == new.LogsDir {
		result = append(result, [2]string{old.LogsDir, new.LogsDir})
	}
	return result
}
func Rewrite(text string, pairs [][2]string) string {
	if len(pairs) == 0 {
		return text
	}
	mapping := map[string]string{}
	for _, p := range pairs {
		mapping[p[0]] = p[1]
		if strings.Contains(p[0], "/") {
			mapping[strings.ReplaceAll(p[0], "/", `\/`)] = strings.ReplaceAll(p[1], "/", `\/`)
		}
	}
	keys := []string{}
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for i, k := range keys {
		keys[i] = regexp.QuoteMeta(k)
	}
	return regexp.MustCompile(strings.Join(keys, "|")).ReplaceAllStringFunc(text, func(value string) string { return mapping[value] })
}
func (c *Controller) Proposal(r Request) (*Proposal, error) {
	if r.Schedule != nil && (r.Schedule.Directories != nil || r.Schedule.InstanceKey != "") {
		return c.cronProposal(r)
	}
	old, version, err := c.Active()
	if err != nil {
		return nil, err
	}
	if r.ExpectedVersion != version {
		return nil, errors.New("Cấu hình đã thay đổi; hãy tải lại")
	}
	if r.Config == nil {
		return nil, errors.New("Thiếu cấu hình mới")
	}
	new := *r.Config
	if !sameCronLocations(old.CronLocations, new.CronLocations) {
		return nil, errors.New("Đường dẫn cron chỉ được đổi qua tác vụ đang chọn")
	}
	if r.Schedule != nil {
		before, after := old, new
		before.CronLocations, after.CronLocations = nil, nil
		if !reflect.DeepEqual(before, after) {
			return nil, errors.New("Không sửa cấu hình chung từ cron; cập nhật dashboard và gửi cấu hình riêng của tác vụ")
		}
	}
	new.DriveHistoryLog = old.DriveHistoryLog
	if new.LogsDir != old.LogsDir {
		new.DriveHistoryLog = filepath.Join(new.LogsDir, "aapanel_backup.log")
	}
	if err = c.Validate(new); err != nil {
		return nil, err
	}
	pairs := settingsPairs(old, new)
	sources := []SourcePreview{}
	for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
		if configuredSource(old, id) != configuredSource(new, id) {
			if configuredSource(new, id) == (SourceSpec{}) {
				return nil, errors.New("Chọn nguồn mới rõ ràng; không xóa cấu hình nguồn đã lưu")
			}
			sources = append(sources, SourcePreview{id, effectiveSource(old, id), effectiveSource(new, id)})
		}
	}
	p := &Proposal{Old: old, New: new, Scripts: []scriptChange{}, Logs: []logChange{}, Days: []dayChange{}}
	planned := map[string]bool{}
	missing := map[string]bool{}
	creating := r.Schedule != nil && r.Schedule.Create
	if creating {
		if _, ok := TaskScripts[r.Schedule.ID]; !ok {
			return nil, errors.New("Loại tác vụ không hợp lệ")
		}
	}
	for _, name := range ScriptNames {
		source, target := filepath.Join(old.ScriptsDir, name), filepath.Join(new.ScriptsDir, name)
		// Only genuinely absent files may be optional. Do not turn unreadable
		// paths, directories or dangling symlinks into missing-script exceptions.
		if err := SafeParents(source); err != nil {
			return nil, err
		}
		_, statErr := os.Lstat(source)
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, errors.New("Không kiểm tra được script được quản lý: " + name)
		}
		if os.IsNotExist(statErr) && creating {
			isTask := name == TaskScripts[r.Schedule.ID]
			isRunner := name == "backup-monitor-run.sh"
			if isTask || isRunner {
				if exists(target) || SafeParents(target) != nil {
					return nil, errors.New("Script đích đã tồn tại hoặc không an toàn: " + target)
				}
				var data []byte
				if isRunner {
					data = []byte(ScheduledRunner(new))
				} else {
					if r.Schedule.ID == "backup-site" || r.Schedule.ID == "backup-database" || r.Schedule.ID == "backup-panel" {
						if err = c.ValidateSource(new, r.Schedule.ID, effectiveSource(new, r.Schedule.ID)); err != nil {
							return nil, err
						}
					}
					data, err = GenerateTaskScript(r.Schedule.ID, new)
					if err != nil {
						return nil, err
					}
				}
				p.Scripts = append(p.Scripts, scriptChange{Source: source, Target: target, Updated: data})
				planned[source] = true
				continue
			}
		}
		if os.IsNotExist(statErr) {
			for _, change := range sources {
				if name == TaskScripts[change.ID] && r.creatingTask != change.ID {
					return nil, errors.New("Thiếu script để cập nhật nguồn: " + name)
				}
			}
			if r.Schedule != nil && name == TaskScripts[r.Schedule.ID] {
				return nil, errors.New("Thiếu script của tác vụ đang sửa: " + name)
			}
			// Partial installations remain editable. Cron dependencies, including
			// the task dispatched by a wrapper, are checked below in every mode.
			missing[name] = true
			continue
		}
		original, err := readRegular(source)
		if err != nil {
			return nil, errors.New("Thiếu hoặc không đọc được script được quản lý: " + name)
		}
		if source != target && exists(target) {
			return nil, errors.New("Script đích đã tồn tại: " + target)
		}
		if name == "backup-site.sh" && !strings.Contains(string(original), LayoutMarker) {
			return nil, errors.New("Cần cài cấu trúc backup theo ngày trước khi áp dụng cấu hình")
		}
		if strings.Contains(string(original), "daily_backup_layout.py") {
			return nil, errors.New("Script vẫn dùng helper Python; hãy cài công cụ Go trước khi đổi cấu hình")
		}
		updated := []byte(Rewrite(string(original), pairs))
		for _, source := range sources {
			if name == source.ID+".sh" {
				patched, err := PatchSource(string(updated), source.ID, source.Proposed)
				if err != nil {
					return nil, err
				}
				updated = []byte(patched)
			}
		}
		p.Scripts = append(p.Scripts, scriptChange{Source: source, Target: target, Original: original, Updated: updated})
	}
	if err = c.updatePrivateScripts(p); err != nil {
		return nil, err
	}
	logs := [][2]string{{old.DriveHistoryLog, new.DriveHistoryLog}}
	for _, name := range LogNames {
		logs = append(logs, [2]string{filepath.Join(old.LogsDir, name), filepath.Join(new.LogsDir, name)})
	}
	for _, pair := range logs {
		if pair[0] == pair[1] || !exists(pair[0]) {
			continue
		}
		if err = SafeParents(pair[1]); err != nil {
			return nil, err
		}
		if exists(pair[1]) {
			return nil, errors.New("Log đích đã tồn tại: " + pair[1])
		}
		sum, err := Digest(pair[0])
		if err != nil {
			return nil, err
		}
		p.Logs = append(p.Logs, logChange{pair[0], pair[1], sum})
	}
	if old.BackupRoot != new.BackupRoot {
		for _, loc := range old.CronLocations {
			if loc.Options != nil && loc.Options.BackupRoot == old.BackupRoot {
				return nil, errors.New("Nơi lưu chung đang được cron riêng sử dụng; đổi nơi lưu qua từng cron, không chuyển cả kho chung")
			}
		}
		if within(new.BackupRoot, old.BackupRoot) || within(old.BackupRoot, new.BackupRoot) {
			return nil, errors.New("Thư mục backup mới không được lồng trong hoặc chứa thư mục cũ")
		}
		original, err := os.Stat(old.BackupRoot)
		if err != nil {
			return nil, err
		}
		targetInfo, err := nearest(new.BackupRoot)
		if err != nil {
			return nil, err
		}
		if device(original) != device(targetInfo) {
			return nil, errors.New("Bản đầu tiên chỉ hỗ trợ đổi thư mục trên cùng filesystem; chưa chuyển giữa hai ổ đĩa")
		}
		entries, err := os.ReadDir(old.BackupRoot)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !ValidDay(entry.Name()) {
				continue
			}
			source, target := filepath.Join(old.BackupRoot, entry.Name()), filepath.Join(new.BackupRoot, entry.Name())
			if err = SafeParents(source); err != nil {
				return nil, err
			}
			if !entry.IsDir() {
				continue
			}
			if err = SafeParents(target); err != nil {
				return nil, err
			}
			if exists(target) {
				return nil, errors.New("Thư mục ngày đích đã tồn tại: " + target)
			}
			day := dayChange{Source: source, Target: target, Files: []fileCheck{}}
			err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := SafeParents(path); err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return errors.New("Tệp không hợp lệ trong thư mục backup")
				}
				sum, err := Digest(path)
				if err != nil {
					return err
				}
				relative, err := filepath.Rel(source, path)
				if err != nil {
					return err
				}
				day.Files = append(day.Files, fileCheck{relative, info.Size(), sum})
				return nil
			})
			if err != nil {
				return nil, err
			}
			p.Days = append(p.Days, day)
		}
	}
	p.Cron, err = c.readStorageCron()
	if err != nil {
		return nil, err
	}
	if err := validateMissingCronScripts(p.Cron, old.ScriptsDir, missing); err != nil {
		return nil, err
	}
	for key, loc := range old.CronLocations {
		local, _ := CronSettings(old, loc.ID, key)
		missing := map[string]bool{}
		for _, name := range []string{TaskScripts[loc.ID], "backup-monitor-run.sh"} {
			script := scriptPath(local, name)
			if err := SafeParents(script); err != nil {
				return nil, err
			}
			_, err := os.Lstat(script)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			missing[name] = os.IsNotExist(err)
		}
		if err := validateMissingCronSettings(p.Cron, local, missing); err != nil {
			return nil, err
		}
	}
	scheduledCron, schedulePreview, err := proposeSchedulePlanned(p.Cron, old.ScriptsDir, r.Schedule, planned, old.LogsDir)
	if err != nil {
		return nil, err
	}
	managed := map[string]bool{}
	for _, name := range ScriptNames {
		managed[filepath.Join(old.ScriptsDir, name)] = true
	}
	lines := strings.Split(strings.TrimSuffix(string(scheduledCron), "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") && !strings.HasPrefix(line, "# backup-monitor-paused ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if managed[field] {
				lines[i] = Rewrite(line, pairs)
				break
			}
		}
	}
	p.NewCron = []byte(strings.Join(lines, "\n") + "\n")
	fingerprints := []interface{}{Version, version, new, hashBytes(p.Cron), r.Schedule, hashBytes(p.NewCron)}
	for _, script := range p.Scripts {
		fingerprints = append(fingerprints, []string{script.Source, script.Target, hashBytes(script.Original), hashBytes(script.Updated), hashBytes(script.TargetOriginal), fmt.Sprint(script.TargetExistedBeforePreview)})
	}
	for _, log := range p.Logs {
		fingerprints = append(fingerprints, []string{log.Source, log.Hash})
	}
	for _, day := range p.Days {
		fingerprints = append(fingerprints, day)
	}
	raw, err := json.Marshal(fingerprints)
	if err != nil {
		return nil, err
	}
	p.Summary = Preview{Token: hashBytes(raw), Changed: len(pairs) > 0 || len(sources) > 0 || (schedulePreview != nil && schedulePreview.Changed), DaysToMove: len(p.Days), LogsToMove: len(p.Logs), DriveChanged: old.DriveRemote != new.DriveRemote || old.DriveFolder != new.DriveFolder, Config: new, Version: version, Schedule: schedulePreview, Sources: sources}
	p.Summary.ScriptsToCreate = []string{}
	for _, script := range p.Scripts {
		if script.Original == nil {
			p.Summary.ScriptsToCreate = append(p.Summary.ScriptsToCreate, script.Target)
			p.Summary.Changed = true
		} else if script.Source != script.Target || string(script.Original) != string(script.Updated) {
			p.Summary.ScriptsToUpdate++
		}
	}
	for _, day := range p.Days {
		for _, file := range day.Files {
			p.Summary.FilesToMove++
			p.Summary.BytesToMove += file.Size
		}
	}
	return p, nil
}

// Paused jobs still depend on their scripts, but ordinary cron comments do not.
// Wrapped cron lines name the runner rather than the dispatched task's file;
// checking only literal script paths would miss a deleted backup task.
func validateMissingCronScripts(cron []byte, scriptsDir string, missing map[string]bool) error {
	return validateMissingCronSettings(cron, Settings{ScriptsDir: scriptsDir}, missing)
}
func validateMissingCronSettings(cron []byte, cfg Settings, missing map[string]bool) error {
	paths := map[string]string{}
	for _, name := range ScriptNames {
		paths[scriptPath(cfg, name)] = name
	}
	wrapper := scriptPath(cfg, "backup-monitor-run.sh")
	for _, line := range strings.Split(string(cron), "\n") {
		body := strings.TrimSpace(line)
		if strings.HasPrefix(body, pausedSchedulePrefix) {
			body = strings.TrimPrefix(body, pausedSchedulePrefix)
		} else if strings.HasPrefix(body, "#") {
			continue
		}
		fields := strings.Fields(body)
		for i, field := range fields {
			if strings.HasPrefix(field, "#") {
				break
			}
			if name := paths[field]; missing[name] {
				return errors.New("Cron hiện có trỏ tới script bị thiếu: " + name)
			}
			if field == wrapper && i+1 < len(fields) {
				if name := TaskScripts[fields[i+1]]; missing[name] {
					return errors.New("Cron hiện có trỏ tới script bị thiếu: " + name)
				}
			}
		}
	}
	return nil
}

func verifyDay(day dayChange) error {
	for _, file := range day.Files {
		path := filepath.Join(day.Target, file.Relative)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		sum, err := Digest(path)
		if err != nil {
			return err
		}
		if info.Size() != file.Size || sum != file.Hash {
			return errors.New("Kiểm tra dữ liệu sau chuyển thất bại")
		}
	}
	return nil
}
func acquireAll(paths []string) ([]*os.File, error) {
	locks := []*os.File{}
	for _, path := range paths {
		f, err := acquire(path, false, true)
		if err != nil {
			releaseAll(locks)
			return nil, err
		}
		locks = append(locks, f)
	}
	return locks, nil
}
func releaseAll(locks []*os.File) {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = locks[i].Close()
	}
}
func (c *Controller) Apply(r Request) (state State, resultErr error) {
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return state, err
	}
	defer func() { releaseAll(locks) }()
	p, err := c.Proposal(r)
	if err != nil {
		return state, err
	}
	if r.PreviewToken != p.Summary.Token {
		return state, errors.New("Dữ liệu đã thay đổi sau khi xem trước; hãy xem trước lại")
	}
	if !p.Summary.Changed {
		return c.Get()
	}
	temporary, err := os.MkdirTemp("", "backup-system-validate-*")
	if err != nil {
		return state, err
	}
	defer os.RemoveAll(temporary)
	for _, script := range p.Scripts {
		target := filepath.Join(temporary, hashBytes([]byte(script.Source))[:16]+"-"+filepath.Base(script.Source))
		if err = AtomicWrite(target, script.Updated, 0600); err != nil {
			return state, err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", target}, nil); err != nil {
			return state, errors.New("Script cập nhật không hợp lệ: " + filepath.Base(script.Source))
		}
	}
	if p.Summary.DriveChanged && r.Schedule == nil {
		if err = c.Create(Request{Location: "drive", Remote: p.New.DriveRemote, Path: p.New.DriveFolder}); err != nil {
			return state, err
		}
	}
	for _, path := range []string{p.New.BackupRoot, p.New.ScriptsDir, p.New.LogsDir, c.RecoveryRoot} {
		if err = mkdirPrivate(path); err != nil {
			return state, err
		}
	}
	for _, script := range p.Scripts {
		if err = mkdirPrivate(filepath.Dir(script.Target)); err != nil {
			return state, err
		}
	}
	for _, log := range p.Logs {
		if err = mkdirPrivate(filepath.Dir(log.Target)); err != nil {
			return state, err
		}
	}
	for key, loc := range p.New.CronLocations {
		cfg, _ := CronSettings(p.New, loc.ID, key)
		if err = mkdirPrivate(cfg.BackupRoot); err != nil {
			return state, err
		}
		if err = mkdirPrivate(cfg.LogsDir); err != nil {
			return state, err
		}
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "system-config-go-*")
	if err != nil {
		return state, err
	}
	previous, _ := json.MarshalIndent(p.Old, "", "  ")
	if err = c.Write(filepath.Join(recovery, "previous.json"), previous, 0600); err != nil {
		return state, err
	}
	if err = c.Write(filepath.Join(recovery, "crontab.txt"), p.Cron, 0600); err != nil {
		return state, err
	}
	for _, script := range p.Scripts {
		if script.Original != nil {
			if err = c.Write(filepath.Join(recovery, hashBytes([]byte(script.Source))[:16]+"-"+filepath.Base(script.Source)), script.Original, 0600); err != nil {
				return state, err
			}
		}
		if script.TargetExistedBeforePreview {
			if err = c.Write(filepath.Join(recovery, hashBytes([]byte(script.Target))[:16]+"-"+filepath.Base(script.Target)+".target-before"), script.TargetOriginal, 0600); err != nil {
				return state, err
			}
		}
	}
	manifest, _ := json.MarshalIndent(struct {
		Days     []dayChange
		Logs     []logChange
		Archives []archiveTransfer
		Ledgers  []ledgerTransfer
	}{p.Days, p.Logs, p.Archives, p.Ledgers}, "", "  ")
	if err = c.Write(filepath.Join(recovery, "moves.json"), manifest, 0600); err != nil {
		return state, err
	}
	movedDays := []dayChange{}
	movedLogs := []logChange{}
	installed := []scriptChange{}
	stagedArchives := []archiveTransfer{}
	installedLedgers := []ledgerTransfer{}
	cronAttempted := false
	committed := false
	defer func() {
		if resultErr == nil || committed {
			return
		}
		rollbackErrors := []string{}
		for i := len(installedLedgers) - 1; i >= 0; i-- {
			ledger := installedLedgers[i]
			if sum, err := Digest(ledger.Target); err != nil || sum != hashBytes(ledger.Updated) {
				rollbackErrors = append(rollbackErrors, "metadata đã thay đổi")
				continue
			}
			var err error
			if ledger.Original == nil {
				err = os.Remove(ledger.Target)
			} else {
				err = c.Write(ledger.Target, ledger.Original, 0600)
			}
			if err != nil {
				rollbackErrors = append(rollbackErrors, "metadata")
			}
		}
		for i := len(stagedArchives) - 1; i >= 0; i-- {
			move := stagedArchives[i]
			if verifyTransferFile(move.Source, move) != nil || verifyTransferFile(move.Target, move) != nil {
				rollbackErrors = append(rollbackErrors, "backup đã thay đổi; giữ cả hai bản")
				continue
			}
			if err := os.Remove(move.Target); err != nil {
				rollbackErrors = append(rollbackErrors, "backup đích")
			}
		}
		if cronAttempted {
			if _, err := c.Run([]string{"crontab", "-"}, p.Cron); err != nil {
				rollbackErrors = append(rollbackErrors, "crontab")
			}
		}
		for i := len(installed) - 1; i >= 0; i-- {
			script := installed[i]
			var err error
			if script.TargetExistedBeforePreview {
				if sum, readErr := Digest(script.Target); readErr != nil || sum != hashBytes(script.Updated) {
					err = errors.New("Script đích đã thay đổi; không khôi phục")
				} else {
					err = c.Write(script.Target, script.TargetOriginal, 0700)
				}
			} else if script.Original == nil {
				sum, readErr := Digest(script.Target)
				if readErr != nil || sum != hashBytes(script.Updated) {
					err = errors.New("Script mới đã thay đổi; không xóa")
				} else {
					err = os.Remove(script.Target)
				}
			} else if script.Source == script.Target {
				err = c.Write(script.Source, script.Original, 0700)
			} else {
				if sum, readErr := Digest(script.Target); readErr != nil || sum != hashBytes(script.Updated) {
					err = errors.New("Script đích đã thay đổi; không xóa")
				} else {
					err = os.Remove(script.Target)
				}
			}
			if err != nil {
				rollbackErrors = append(rollbackErrors, filepath.Base(script.Source))
			}
		}
		for i := len(movedLogs) - 1; i >= 0; i-- {
			if err := os.Remove(movedLogs[i].Target); err != nil {
				rollbackErrors = append(rollbackErrors, "log")
			}
		}
		for i := len(movedDays) - 1; i >= 0; i-- {
			day := movedDays[i]
			if exists(day.Source) {
				rollbackErrors = append(rollbackErrors, "thư mục gốc đã xuất hiện lại")
				continue
			}
			if err := os.Rename(day.Target, day.Source); err != nil {
				rollbackErrors = append(rollbackErrors, "backup")
			}
		}
		if len(rollbackErrors) > 0 {
			resultErr = fmt.Errorf("Không hoàn tất khôi phục (%s); bản dự phòng: %s", strings.Join(rollbackErrors, ", "), recovery)
		}
	}()
	for _, move := range p.Archives {
		if err = stageBackupTransfer(move); err != nil {
			return state, err
		}
		stagedArchives = append(stagedArchives, move)
	}
	for _, ledger := range p.Ledgers {
		if err = SafeParents(ledger.Target); err != nil {
			return state, err
		}
		if ledger.Original == nil {
			if exists(ledger.Target) {
				return state, errors.New("Metadata đích đã xuất hiện sau xem trước")
			}
		} else if sum, readErr := Digest(ledger.Target); readErr != nil || sum != hashBytes(ledger.Original) {
			return state, errors.New("Metadata đích đã thay đổi sau xem trước")
		}
		if err = c.Write(ledger.Target, ledger.Updated, 0600); err != nil {
			return state, err
		}
		installedLedgers = append(installedLedgers, ledger)
	}
	for _, day := range p.Days {
		if err = SafeParents(day.Target); err != nil {
			return state, err
		}
		if exists(day.Target) {
			return state, errors.New("Đích đã xuất hiện sau xem trước")
		}
		if err = os.Rename(day.Source, day.Target); err != nil {
			return state, err
		}
		movedDays = append(movedDays, day)
		if err = verifyDay(day); err != nil {
			return state, err
		}
	}
	for _, log := range p.Logs {
		if err = copyExclusive(log.Source, log.Target); err != nil {
			return state, err
		}
		movedLogs = append(movedLogs, log)
		sum, err := Digest(log.Target)
		if err != nil {
			return state, err
		}
		if sum != log.Hash {
			return state, errors.New("Kiểm tra log sau chuyển thất bại")
		}
	}
	for _, script := range p.Scripts {
		if script.Original == nil {
			if script.TargetExistedBeforePreview {
				currentTarget, targetErr := readRegular(script.Target)
				if targetErr != nil || hashBytes(currentTarget) != hashBytes(script.TargetOriginal) {
					return state, errors.New("Script đích đã thay đổi sau xem trước; chưa ghi đè")
				}
				err = c.Write(script.Target, script.Updated, 0700)
			} else {
				err = writeScriptExclusive(script.Target, script.Updated)
			}
			if err != nil {
				return state, err
			}
			installed = append(installed, script)
			continue
		}
		if script.Source == script.Target && string(script.Original) == string(script.Updated) {
			continue
		}
		if script.Source != script.Target {
			if script.TargetExistedBeforePreview {
				currentTarget, targetErr := readRegular(script.Target)
				if targetErr != nil || hashBytes(currentTarget) != hashBytes(script.TargetOriginal) {
					return state, errors.New("Script đích đã thay đổi sau xem trước; chưa ghi đè")
				}
			} else if exists(script.Target) {
				return state, errors.New("Script đích đã xuất hiện sau xem trước")
			}
		}
		current, readErr := readRegular(script.Source)
		if readErr != nil || hashBytes(current) != hashBytes(script.Original) {
			return state, errors.New("Script đã thay đổi sau xem trước; chưa ghi đè")
		}
		if script.Source != script.Target && !script.TargetExistedBeforePreview {
			err = writeScriptExclusive(script.Target, script.Updated)
		} else {
			err = c.Write(script.Target, script.Updated, 0700)
		}
		if err != nil {
			return state, err
		}
		installed = append(installed, script)
	}
	// Never point crontab at a clone that did not survive installation.
	for _, script := range p.Scripts {
		if sum, readErr := Digest(script.Target); readErr != nil || sum != hashBytes(script.Updated) {
			return state, errors.New("Script đích thiếu hoặc thay đổi; chưa cập nhật cron: " + filepath.Base(script.Target))
		}
	}
	cronAttempted = true
	if _, err = c.Run([]string{"crontab", "-"}, p.NewCron); err != nil {
		return state, err
	}
	raw, _ := json.MarshalIndent(p.New, "", "  ")
	for _, move := range stagedArchives {
		if err = verifyTransferFile(move.Source, move); err != nil {
			return state, err
		}
		if err = verifyTransferFile(move.Target, move); err != nil {
			return state, err
		}
	}
	if err = c.Write(c.ConfigPath, raw, 0600); err != nil {
		return state, err
	}
	committed = true
	transferWarnings := []string{}
	for _, move := range stagedArchives {
		if verifyTransferFile(move.Source, move) != nil || verifyTransferFile(move.Target, move) != nil {
			transferWarnings = append(transferWarnings, "Nơi lưu mới đã áp dụng; bản gốc thay đổi nên được giữ lại: "+move.Source)
			continue
		}
		if err := os.Remove(move.Source); err != nil {
			transferWarnings = append(transferWarnings, "Backup đã có ở nơi mới; chưa xóa được bản gốc: "+move.Source)
		}
	}
	state, err = c.Get()
	if err != nil {
		return state, errors.New("Cấu hình đã áp dụng nhưng chưa xác nhận được trạng thái; hãy tải lại")
	}
	state.Applied = true
	state.RecoveryDir = recovery
	state.Warnings = append(state.Warnings, transferWarnings...)
	state.Warnings = append(state.Warnings, cleanupFlattenedCron(p, r.Schedule)...)
	// Unlock before restarting an existing monitor. Never start a monitor that
	// was stopped before this operation; never return failure after commit just
	// because restarting the monitor failed.
	releaseAll(locks)
	locks = nil
	if c.RestartMonitor != nil && r.Schedule == nil {
		warnings, err := c.RestartMonitor(p.Old, p.New)
		state.Warnings = warnings
		if err != nil {
			state.Warnings = []string{"Cấu hình đã áp dụng; chưa xác nhận được việc khởi động lại trình giám sát"}
		}
	}
	if state.Warnings == nil {
		state.Warnings = []string{}
	}
	return state, nil
}

// Managed backup operations scan day directories and legacy category roots,
// not arbitrary top-level folders. Keep auxiliary files outside those namespaces.
// Reserve even malformed date-shaped names: shell sync uses the same broad glob.
var backupDayNamespace = regexp.MustCompile(`^20..-..-..$`)

func validateAuxiliaryPath(root, dir string) error {
	if within(root, dir) {
		return errors.New("Thư mục script/log không được trùng hoặc chứa nơi lưu backup; chọn thư mục con riêng như Scripts hoặc Logs")
	}
	if !within(dir, root) {
		return nil
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	if backupDayNamespace.MatchString(first) || validCategory(first) {
		return errors.New("Thư mục script/log không được nằm trong thư mục backup theo ngày hoặc site/database/panel; chọn thư mục con riêng như Scripts hoặc Logs")
	}
	return nil
}

func validateAuxiliaryLocations(cfg Settings) error {
	roots := []string{cfg.BackupRoot}
	dirs := []string{cfg.ScriptsDir, cfg.LogsDir}
	for _, loc := range cfg.CronLocations {
		// Validate the chosen parents, not only their effective cron-ID children:
		// using the backup root itself as a scripts/logs parent is unsafe.
		dirs = append(dirs, loc.ScriptsDir, loc.LogsDir)
		if loc.Options != nil {
			roots = append(roots, loc.Options.BackupRoot)
		}
	}
	for _, root := range roots {
		for _, dir := range dirs {
			if err := validateAuxiliaryPath(root, dir); err != nil {
				return err
			}
		}
	}
	return nil
}
