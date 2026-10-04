package backupops

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The scope is explicit: folder archives exactly the chosen directory; children
// produces a separate archive per first-level child (or a logical MySQL dump).
type SourceSpec struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}
type SourcePreview struct {
	ID       string     `json:"id"`
	Previous SourceSpec `json:"previous"`
	Proposed SourceSpec `json:"proposed"`
}

func configuredSource(s Settings, id string) SourceSpec {
	switch id {
	case "backup-site":
		return s.SiteSource
	case "backup-database":
		return s.DatabaseSource
	case "backup-panel":
		return s.PanelSource
	}
	return SourceSpec{}
}
func effectiveSource(s Settings, id string) SourceSpec {
	spec := configuredSource(s, id)
	if spec != (SourceSpec{}) {
		return spec
	}
	switch id {
	case "backup-site":
		return SourceSpec{"/www/wwwroot", "children"}
	case "backup-database":
		return SourceSpec{"/www/server/data", "children"}
	case "backup-panel":
		return SourceSpec{"/www/server/panel", "folder"}
	}
	return spec
}
func sourceSelections(s Settings) map[string]SourceSpec {
	result := map[string]SourceSpec{}
	for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
		result[id] = effectiveSource(s, id)
	}
	return result
}
func (c *Controller) ValidateSource(s Settings, id string, spec SourceSpec) error {
	purpose := map[string]string{"backup-site": "source-site", "backup-database": "source-database", "backup-panel": "source-panel"}[id]
	if purpose == "" || (spec.Scope != "folder" && spec.Scope != "children") || (id == "backup-panel" && spec.Scope != "folder") {
		return errors.New("Loại hoặc phạm vi nguồn sao lưu không hợp lệ")
	}
	if err := c.LocalPath(spec.Path, purpose); err != nil {
		return err
	}
	info, err := os.Stat(spec.Path)
	if err != nil || !info.IsDir() {
		return errors.New("Thư mục dữ liệu nguồn phải tồn tại trên máy chủ")
	}
	for _, other := range []string{s.BackupRoot, s.ScriptsDir, s.LogsDir} {
		if within(spec.Path, other) || within(other, spec.Path) {
			return errors.New("Nguồn dữ liệu phải tách khỏi nơi lưu backup, script và log")
		}
	}
	if spec.Scope == "folder" && !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(filepath.Base(spec.Path)) {
		return errors.New("Tên thư mục nguồn không hợp lệ")
	}
	if id == "backup-database" && spec.Scope == "folder" {
		if systemDatabase(filepath.Base(spec.Path)) || exists(filepath.Join(spec.Path, "mysql")) {
			return errors.New("Chọn thư mục con của data tương ứng database người dùng; không nén thư mục MySQL đang chạy")
		}
	}
	if id == "backup-panel" {
		for _, name := range []string{"data", "config", "vhost"} {
			component := filepath.Join(spec.Path, name)
			if err := SafeParents(component); err != nil {
				return err
			}
			info, err := os.Stat(component)
			if err != nil || !info.IsDir() {
				return errors.New("Nguồn aaPanel phải chứa data, config và vhost")
			}
		}
	}
	return nil
}
func systemDatabase(name string) bool {
	return name == "mysql" || name == "performance_schema" || name == "sys" || name == "phpmyadmin"
}

func sourceReplace(text, pattern, replacement string) (string, error) {
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) != 1 {
		return "", errors.New("Script nguồn không đúng cấu trúc được hỗ trợ; chưa cập nhật")
	}
	p := matches[0]
	return text[:p[0]] + replacement + text[p[1]:], nil
}

// Patch only known source declarations/selection, never commands/credentials or
// archive publication/retention. Original scripts participate in Apply rollback.
func PatchSource(text, id string, spec SourceSpec) (string, error) {
	if !localChars.MatchString(spec.Path) || spec.Path == "" || (spec.Scope != "folder" && spec.Scope != "children") {
		return "", errors.New("Nguồn sao lưu không hợp lệ")
	}
	if id == "backup-panel" {
		return sourceReplace(text, `(?m)^PANEL_ROOT=[^\r\n]+`, "PANEL_ROOT='"+spec.Path+"'")
	}
	array := "SITES"
	if id == "backup-database" {
		array = "DATABASES"
	} else if id != "backup-site" {
		return "", errors.New("Tác vụ không hỗ trợ đổi nguồn")
	}
	var err error
	text, err = sourceReplace(text, `(?m)^SOURCE_ROOT=[^\r\n]+`, "SOURCE_ROOT='"+spec.Path+"'")
	if err != nil {
		return "", err
	}
	selection := array + "=()\n# backup-monitor source selection v1\n"
	if spec.Scope == "folder" {
		selection += array + "=('" + filepath.Base(spec.Path) + "')"
	} else {
		selection += "[[ -d \"$SOURCE_ROOT\" && ! -L \"$SOURCE_ROOT\" ]] || { printf 'Source directory unavailable\\n' >&2; exit 1; }\n"
		selection += "while IFS= read -r -d '' source_path; do\n    source_name=${source_path##*/}\n    [[ \"$source_name\" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]] || continue\n"
		if id == "backup-database" {
			selection += "    case \"$source_name\" in mysql|performance_schema|sys|phpmyadmin) continue ;; esac\n"
		} else {
			selection += "    [[ \"$source_name\" != default ]] || continue\n"
		}
		selection += "    " + array + "+=(\"$source_name\")\ndone < <(find \"$SOURCE_ROOT\" -mindepth 1 -maxdepth 1 -type d -print0 | sort -z)\n(( ${#" + array + "[@]} > 0 )) || { printf 'No backup targets found\\n' >&2; exit 1; }"
	}
	// Replace our complete previous block, or a single original array assignment.
	block := `(?ms)^` + array + `=\(\)\n# backup-monitor source selection v1\n.*?\n# backup-monitor source selection end`
	if strings.Contains(text, "# backup-monitor source selection v1") {
		text, err = sourceReplace(text, block, selection+"\n# backup-monitor source selection end")
	} else {
		text, err = sourceReplace(text, `(?m)^`+array+`=\([^\r\n]*\)`, selection+"\n# backup-monitor source selection end")
	}
	if err != nil {
		return "", err
	}
	if id == "backup-site" {
		value := `    source_dir="$SOURCE_ROOT/$site"`
		if spec.Scope == "folder" {
			value = `    source_dir="$SOURCE_ROOT"`
		}
		text, err = sourceReplace(text, `(?m)^    source_dir="\$SOURCE_ROOT(?:/\$site)?"`, value)
	} else {
		value := `    [[ -d "$SOURCE_ROOT/$database" && ! -L "$SOURCE_ROOT/$database" ]] || { printf 'Database source missing: %s\n' "$database" >&2; return 1; }`
		if spec.Scope == "folder" {
			value = `    [[ -d "$SOURCE_ROOT" && ! -L "$SOURCE_ROOT" ]] || { printf 'Database source missing: %s\n' "$database" >&2; return 1; }`
		}
		text, err = sourceReplace(text, `(?m)^    \[\[ -d "\$SOURCE_ROOT(?:/\$database)?"[^\r\n]+`, value)
	}
	return text, err
}
