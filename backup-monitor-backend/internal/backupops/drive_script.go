package backupops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
