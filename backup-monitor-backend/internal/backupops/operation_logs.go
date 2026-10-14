package backupops

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type LogRequest struct {
	ID     string `json:"id"`
	Cursor string `json:"cursor"`
}

type logCursor struct {
	Offset, Size  int64
	Device, Inode uint64
	HeadLength    int
	HeadHash      string
}
type LogPage struct {
	Exists           bool   `json:"exists"`
	Content          string `json:"content"`
	OlderCursor      string `json:"olderCursor,omitempty"`
	Start, End, Size int64
	PartialLine      bool `json:"partialLine"`
}

func taskLogName(id string) (string, error) {
	if _, ok := TaskScripts[id]; !ok {
		return "", errors.New("Tác vụ không hợp lệ")
	}
	switch id {
	case "drive-sync":
		return "auto_backup.log", nil
	case "integrity-check":
		return "check_integrity.log", nil
	case "cleanup-panel":
		return "cleanup-panel-backups.log", nil
	}
	return id + ".log", nil
}
func (c *Controller) LogPage(r Request) (LogPage, error) {
	var result LogPage
	if r.Log == nil {
		return result, errors.New("Thiếu thông tin log")
	}
	name, err := taskLogName(strings.Split(r.Log.ID, "~")[0])
	if err != nil {
		return result, err
	}
	cfg, _, err := c.Active()
	if err != nil {
		return result, err
	}
	cfg, _, err = taskSettings(cfg, r.Log.ID)
	if err != nil {
		return result, err
	}
	path := logPath(cfg, name)
	if err = SafeParents(path); err != nil {
		return result, err
	}
	f, err := openRegular(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !privateRootOwned(info) {
		return result, errors.New("Log không an toàn")
	}
	cursor := logCursor{Offset: info.Size(), Size: info.Size(), Device: device(info), Inode: inode(info), HeadLength: int(min(info.Size(), 4096))}
	if r.Log.Cursor != "" {
		if len(r.Log.Cursor) > 2048 {
			return result, errors.New("Con trỏ log không hợp lệ")
		}
		raw, err := base64.RawURLEncoding.DecodeString(r.Log.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Offset < 0 || cursor.Offset > cursor.Size || cursor.Size > info.Size() || cursor.HeadLength < 0 || cursor.HeadLength > 4096 || cursor.HeadLength > int(cursor.Size) || cursor.Device != device(info) || cursor.Inode != inode(info) {
			return result, errors.New("Log đã thay đổi; hãy làm mới từ đầu")
		}
	}
	head := make([]byte, cursor.HeadLength)
	if _, err = io.ReadFull(f, head); err != nil {
		return result, err
	}
	sum := sha256.Sum256(head)
	hash := hex.EncodeToString(sum[:])
	if r.Log.Cursor != "" && hash != cursor.HeadHash {
		return result, errors.New("Log đã bị thay thế hoặc cắt ngắn; hãy làm mới")
	}
	cursor.HeadHash = hash
	result.Exists = true
	result.End = cursor.Offset
	result.Start = max(0, result.End-65536)
	// Do not split a UTF-8 character between independently decoded pages.
	for result.Start > 0 && result.Start < result.End {
		first := []byte{0}
		if _, err = f.ReadAt(first, result.Start); err != nil {
			return result, err
		}
		if utf8.RuneStart(first[0]) {
			break
		}
		result.Start++
	}
	result.Size = cursor.Size
	if _, err = f.Seek(result.Start, io.SeekStart); err != nil {
		return result, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, result.End-result.Start))
	if err != nil || int64(len(raw)) != result.End-result.Start {
		return result, errors.New("Log thay đổi trong lúc đọc")
	}
	after, err := f.Stat()
	if err != nil || after.Size() < cursor.Size {
		return result, errors.New("Log thay đổi trong lúc đọc; hãy thử lại")
	}
	result.Content = strings.ToValidUTF8(string(raw), "�")
	if result.Start > 0 {
		one := []byte{0}
		if _, err = f.ReadAt(one, result.Start-1); err != nil {
			return result, err
		}
		result.PartialLine = one[0] != '\n'
		cursor.Offset = result.Start
		encoded, _ := json.Marshal(cursor)
		result.OlderCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return result, nil
}
