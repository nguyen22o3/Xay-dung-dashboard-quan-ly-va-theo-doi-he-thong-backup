package main

import (
	"strings"
	"testing"
)

func TestAlertMonitorPathsStayWithinConfiguredBackups(t *testing.T) {
	allowed := []string{"/Backup", "/www/backup"}
	for _, paths := range [][]string{{"/Backup"}, {"/Backup/panel", "/www/backup"}} {
		if err := validateAlertMonitorPathsWithin(paths, allowed); err != nil {
			t.Fatalf("valid backup paths rejected: %v", err)
		}
	}
	for _, paths := range [][]string{{}, {"/"}, {"/etc"}, {"/Backup/../etc"}, {"/Backup//panel"}, {"/Backup", "/Backup"}, {"/Backup;touch /tmp/unsafe"}} {
		if err := validateAlertMonitorPathsWithin(paths, allowed); err == nil {
			t.Errorf("unsafe backup paths accepted: %q", paths)
		}
	}
}

func TestAlertMonitorSelectionIsPersistedAcrossOtherSettingsUpdates(t *testing.T) {
	current := AlertSettings{MonitorPaths: []string{"/Backup"}}
	merged := mergeAlertSecrets(current, AlertSettings{Threshold: 14})
	if got := effectiveMonitorPaths(merged); len(got) != 1 || got[0] != "/Backup" {
		t.Fatalf("monitor selection was lost: %q", got)
	}
	updated := mergeAlertSecrets(current, AlertSettings{MonitorPaths: []string{"/Backup/panel"}})
	if got := effectiveMonitorPaths(updated); len(got) != 1 || got[0] != "/Backup/panel" {
		t.Fatalf("monitor selection did not update: %q", got)
	}
}

func TestRealtimeAlertWatchesConfiguredBackupRoots(t *testing.T) {
	_, monitor := buildAlertScriptsForRoots(AlertSettings{Threshold: 14}, []string{"/Backup", "/www/backup"})
	for _, want := range []string{
		"WATCH_ROOT_CANDIDATES=('/Backup' '/www/backup')",
		`inotifywait -m -r -e delete --format '%w%f' "${WATCH_ROOTS[@]}"`,
		`if is_script_rotation "$FILE" || is_expected_rotation "$FILE" 14; then`,
		`if [[ "$RELATIVE_FILE" =~ ^panel/`,
	} {
		if !strings.Contains(monitor, want) {
			t.Errorf("monitor is missing %q", want)
		}
	}
	if strings.Contains(monitor, "inotifywait -m -r -e delete --format '%w%f' /www/backup/") {
		t.Fatal("monitor is still restricted to the legacy backup root")
	}
}

func TestRealtimeAlertQuotesBackupRoots(t *testing.T) {
	_, monitor := buildAlertScriptsForRoots(AlertSettings{Threshold: 14}, []string{"/Backup/it's-here/"})
	if !strings.Contains(monitor, `WATCH_ROOT_CANDIDATES=('/Backup/it'\''s-here')`) {
		t.Fatal("backup root was not shell quoted")
	}
}

func TestAlertInstallerValidatesFoldersBeforeReplacingScripts(t *testing.T) {
	command := buildAlertInstallCommand("integrity", "realtime", []string{"/Backup"})
	if !strings.Contains(command, "MONITOR_PATHS=('/Backup')") {
		t.Fatal("installer does not receive selected monitor path")
	}
	if strings.Index(command, "realpath -e") > strings.Index(command, "mv -f -- \"$REALTIME_TMP\"") {
		t.Fatal("installer validates the path only after changing the script")
	}
}
