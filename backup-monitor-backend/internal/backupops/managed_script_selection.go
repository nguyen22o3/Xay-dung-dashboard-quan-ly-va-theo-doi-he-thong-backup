package backupops

import (
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// Only selectors registered in configuration are accepted, never a user path.
type ManagedScriptSelection struct {
	Selector string `json:"selector"`
	Hash     string `json:"hash"`
}
type ManagedScriptOption struct {
	Selector string `json:"selector"`
	Path     string `json:"path"`
	Hash     string `json:"hash"`
}
type ManagedScriptOptions struct {
	Version string                `json:"version"`
	Scripts []ManagedScriptOption `json:"scripts"`
}

func (c *Controller) managedScript(cfg Settings, taskID, selector string) (ManagedScriptOption, Settings, []byte, error) {
	var option ManagedScriptOption
	id, key, hasKey := strings.Cut(selector, "~")
	if id != taskID || TaskScripts[id] == "" || hasKey && (key == "" || strings.Contains(key, "~")) {
		return option, cfg, nil, errors.New("Chỉ chọn script được quản lý cùng loại tác vụ")
	}
	local, err := CronSettings(cfg, id, key)
	if err != nil {
		return option, cfg, nil, err
	}
	if err = c.LocalPath(local.ScriptsDir, "scripts"); err != nil {
		return option, cfg, nil, err
	}
	file := scriptPath(local, TaskScripts[id])
	if err = SafeParents(file); err != nil {
		return option, cfg, nil, err
	}
	f, err := openRegular(file)
	if err != nil {
		return option, cfg, nil, errors.New("Script đã chọn không tồn tại hoặc không phải tệp thường")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > MaxScriptBytes || !privateRootOwned(info) || info.Mode().Perm()&0100 == 0 {
		return option, cfg, nil, errors.New("Script phải do root sở hữu, root có quyền thực thi, không cho người khác ghi và tối đa 128 KB")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxScriptBytes+1))
	if err != nil || len(data) > MaxScriptBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || !strings.HasPrefix(string(data), "#!") {
		return option, cfg, nil, errors.New("Script đã chọn không có nội dung shell UTF-8 hợp lệ")
	}
	option = ManagedScriptOption{Selector: selector, Path: file, Hash: hashBytes(data)}
	return option, local, data, nil
}

func (c *Controller) ManagedScriptOptions(r Request) (ManagedScriptOptions, error) {
	result := ManagedScriptOptions{Scripts: []ManagedScriptOption{}}
	if r.Script == nil || TaskScripts[r.Script.ID] == "" {
		return result, errors.New("Loại tác vụ không hợp lệ")
	}
	cfg, version, err := c.Active()
	if err != nil {
		return result, err
	}
	result.Version = version
	selectors := []string{r.Script.ID}
	for key, loc := range cfg.CronLocations {
		if loc.ID == r.Script.ID {
			selectors = append(selectors, loc.ID+"~"+key)
		}
	}
	sort.Strings(selectors)
	for _, selector := range selectors {
		option, _, _, e := c.managedScript(cfg, r.Script.ID, selector)
		if e == nil {
			result.Scripts = append(result.Scripts, option)
		}
	}
	return result, nil
}
