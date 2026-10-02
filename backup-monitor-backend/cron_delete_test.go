package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCronDeletionCommand(t *testing.T) {
	task, _ := managedCronTaskByID("backup-database")
	command, ok := commandForCronDeletion(task, "30 4 * * *", true)
	if !ok {
		t.Fatal("valid managed task rejected")
	}
	for _, required := range []string{"flock -x 9", "flock -n 8", "CRON_ALREADY_RUNNING", "cron-recovery", `cp -- "$TMP_DIR/current"`, `crontab "$TMP_DIR/updated"`, "DELETED|"} {
		if !strings.Contains(command, required) {
			t.Errorf("missing safeguard %q", required)
		}
	}
	if strings.Index(command, `cp -- "$TMP_DIR/current"`) > strings.Index(command, `crontab "$TMP_DIR/updated"`) {
		t.Fatal("crontab installed before recovery copy")
	}
	for _, invalid := range []string{"", "@daily", "30 4 * *", "30  4 * * *", "30 4 * * *\n", strings.Repeat("x", 101)} {
		if _, ok := commandForCronDeletion(task, invalid, true); ok {
			t.Errorf("accepted malformed snapshot %q", invalid)
		}
	}
	forged := task
	forged.Script = "/tmp/not-managed.sh"
	if _, ok := commandForCronDeletion(forged, "30 4 * * *", true); ok {
		t.Fatal("accepted unallowlisted script")
	}
	forged.ID = "unknown"
	if _, ok := commandForCronDeletion(forged, "30 4 * * *", true); ok {
		t.Fatal("accepted unallowlisted ID")
	}
}

// Execute only the pure filter on fixture text, never crontab or SSH.
func TestCronDeletionFilter(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if err != nil && runtime.GOOS == "windows" {
		awk = `C:\Program Files\Git\usr\bin\awk.exe`
		_, err = os.Stat(awk)
	}
	if err != nil {
		t.Skip("awk unavailable")
	}
	preserved := "# keep comment\nMAILTO=root\n25 22 * * * /bin/bash /www/server/panel/ssl.sh\n"
	direct := "30 4 * * * /bin/bash /root/scripts/backup-database.sh --flag >> /root/db.log 2>&1\n"
	wrapped := "30 4 * * * /bin/bash /root/scripts/backup-monitor-run.sh backup-database --flag\n"
	other := "0 5 * * * /bin/bash /root/scripts/backup-monitor-run.sh backup-site\n"
	for _, tc := range []struct{ name, input, expectedError string }{
		{"direct", preserved + direct + other, ""},
		{"wrapped", preserved + wrapped + other, ""},
		{"changed", preserved + strings.Replace(direct, "30 4", "40 4", 1) + other, "CRON_SCHEDULE_CHANGED"},
		{"duplicate", preserved + direct + wrapped + other, "CRON_DUPLICATE"},
		{"missing", preserved + other, "CRON_NOT_SCHEDULED"},
		{"commented", preserved + "# " + direct + other, "CRON_NOT_SCHEDULED"},
		{"prefix", preserved + strings.Replace(direct, ".sh", ".sh.extra", 1) + other, "CRON_NOT_SCHEDULED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(awk, "-v", "script=/root/scripts/backup-database.sh", "-v", "id=backup-database", "-v", "wrapper=/root/scripts/backup-monitor-run.sh", "-v", "expected=30 4 * * *", "-v", "expected_enabled=1", "-v", "operation=delete", managedCronMutationAwk)
			cmd.Stdin = strings.NewReader(tc.input)
			output, err := cmd.CombinedOutput()
			if tc.expectedError != "" {
				if err == nil || !strings.Contains(string(output), tc.expectedError) {
					t.Fatalf("expected %s, got %s (%v)", tc.expectedError, output, err)
				}
			} else if err != nil || string(output) != preserved+other {
				t.Fatalf("unrelated lines changed: %s (%v)", output, err)
			}
		})
	}
}

func TestCronPauseResumeAndEditFilter(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if err != nil && runtime.GOOS == "windows" {
		awk = `C:\Program Files\Git\usr\bin\awk.exe`
		_, err = os.Stat(awk)
	}
	if err != nil {
		t.Skip("awk unavailable")
	}
	line := "30 4 * * * /bin/bash /root/scripts/backup-database.sh --flag >> /root/db.log 2>&1\n"
	other := "# untouched\n0 5 * * * /bin/bash /root/scripts/backup-site.sh\n"
	paused := pausedCronPrefix + line
	for _, tc := range []struct{ name, input, operation, expectedState, want, marker string }{
		{"pause", line + other, "pause", "1", paused + other, ""},
		{"resume", paused + other, "enable", "0", line + other, ""},
		{"delete-paused", paused + other, "delete", "0", other, ""},
		{"edit-paused", paused + other, "schedule", "0", pausedCronPrefix + strings.Replace(line, "30 4", "20 7", 1) + other, ""},
		{"edit-enabled", line + other, "schedule", "1", strings.Replace(line, "30 4", "20 7", 1) + other, ""},
		{"stale-enabled", paused + other, "delete", "1", "", "CRON_SCHEDULE_CHANGED"},
		{"stale-paused", line + other, "enable", "0", "", "CRON_SCHEDULE_CHANGED"},
		{"active-and-paused-duplicate", line + paused + other, "delete", "1", "", "CRON_DUPLICATE"},
		{"paused-other-kept", line + pausedCronPrefix + strings.Replace(line, "backup-database.sh", "backup-site.sh", 1), "pause", "1", paused + pausedCronPrefix + strings.Replace(line, "backup-database.sh", "backup-site.sh", 1), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(awk, "-v", "script=/root/scripts/backup-database.sh", "-v", "id=backup-database", "-v", "wrapper=/root/scripts/backup-monitor-run.sh", "-v", "expected=30 4 * * *", "-v", "expected_enabled="+tc.expectedState, "-v", "operation="+tc.operation, "-v", "hour=7", "-v", "minute=20", managedCronMutationAwk)
			cmd.Stdin = strings.NewReader(tc.input)
			output, err := cmd.CombinedOutput()
			if tc.marker != "" {
				if err == nil || !strings.Contains(string(output), tc.marker) {
					t.Fatalf("expected %s, got %s (%v)", tc.marker, output, err)
				}
			} else if err != nil || string(output) != tc.want {
				t.Fatalf("filter mismatch: %s (%v)", output, err)
			}
		})
	}
}

func TestParseEnabledAndPausedCron(t *testing.T) {
	jobs := parseManagedCronJobs("__CRONTAB__\n30 4 * * * /bin/bash /root/scripts/backup-database.sh\n" + pausedCronPrefix + "0 5 * * * /bin/bash /root/scripts/backup-monitor-run.sh backup-site --flag\n# 10 5 * * * /bin/bash /root/scripts/backup-panel.sh\n__LOGS__\n")
	if len(jobs) != 2 || !jobs[0].Enabled || jobs[1].Enabled || !jobs[1].ScheduleTracked || jobs[1].Schedule != "0 5 * * *" {
		t.Fatalf("wrong enabled/paused parsing: %+v", jobs)
	}
}
