package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type localActivityFixture struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Date     string   `json:"date"`
	Time     string   `json:"time"`
	Duration *float64 `json:"duration"`
	Status   string   `json:"status"`
}

func parseLocalActivityFixture(t *testing.T, filter, log string) []localActivityFixture {
	t.Helper()
	awk, err := exec.LookPath("awk")
	if err != nil && runtime.GOOS == "windows" {
		awk = `C:\Program Files\Git\usr\bin\awk.exe`
		_, err = os.Stat(awk)
	}
	if err != nil {
		t.Skip("awk unavailable")
	}
	// A script file avoids MSYS argument/backslash re-interpretation on Windows.
	filterPath := filepath.Join(t.TempDir(), "activity.awk")
	if err := os.WriteFile(filterPath, []byte(filter), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(awk, "-f", filterPath)
	command.Stdin = strings.NewReader(log)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("local log parser failed: %v: %s", err, output)
	}
	var entries []localActivityFixture
	if err := json.Unmarshal([]byte("["+strings.TrimSuffix(string(output), ",")+"]"), &entries); err != nil {
		t.Fatalf("invalid activity JSON: %v: %s", err, output)
	}
	return entries
}

func TestLocalActivityLegacyAndTaggedVersions(t *testing.T) {
	for _, tc := range []struct{ name, root, file, want string }{
		{"legacy site", "/www/backup/site/shop.example", "web_shop.example_20261003_050001_site.tar.gz", "Backup Website: shop.example"},
		{"cron site", "/www/backup/2026-10-03/site/web4.local", "web_web4.local_20261003_050001_cron_238fd2bfdf502868_site.tar.gz", "Backup Website: web4.local"},
		{"manual site", "/www/backup/2026-10-03/site/shop.example", "web_shop.example_20261003_050001_manual_0123456789abcdef_site.tar.gz", "Backup Website: shop.example"},
		{"legacy database", "/www/backup/database/mysql/crontab_backup/sql_web1_local", "db_sql_web1_local_20261003_050001_mysql_data.sql.gz", "Backup Database: sql_web1_local"},
		{"cron database", "/www/backup/2026-10-03/database/mysql/crontab_backup/store_db", "db_store_db_20261003_050001_cron_0123456789abcdef_mysql_data.sql.gz", "Backup Database: store_db"},
		{"manual database", "/www/backup/2026-10-03/database/mysql/crontab_backup/store_db", "db_store_db_20261003_050001_manual_fedcba9876543210_mysql_data.sql.gz", "Backup Database: store_db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := parseLocalActivityFixture(t, customLocalActivityAwk, "CREATED "+tc.root+"/"+tc.file+" DURATION 0.040\n")
			if len(entries) != 1 {
				t.Fatalf("expected file detail, got %+v", entries)
			}
			entry := entries[0]
			if entry.Kind != "file" || entry.Name != tc.want || entry.Date != "2026-10-03" || entry.Time != "05:00:01" || entry.Duration == nil || *entry.Duration != 0.04 || entry.Status != "Successful" {
				t.Fatalf("incorrect detail: %+v", entry)
			}
		})
	}
}

func TestLocalActivityMissingDurationAndNonFileLogs(t *testing.T) {
	log := `CREATED /www/backup/2026-10-03/site/shop/web_shop_20261003_050001_site.tar.gz
CREATED /www/backup/2026-10-03/site/shop/web_shop_20261003_050002_site.tar.gz DURATION invalid
CREATED /www/backup/2026-10-03/site/shop/web_shop_20261003_050003_site.tar.gz DURATION 0.000
CREATED /tmp/web_shop_20261003_050001_site.tar.gz DURATION 1
CREATED /www/backup/2026-10-03/site/shop/web_shop_20261003_050001_cron_invalid_site.tar.gz DURATION 1
FAILED /www/backup/site/shop/web_shop_20261003_050001_site.tar.gz
MONITOR_RUN|backup-site|2026-10-03|05:00:01|success|26|run-id
`
	entries := parseLocalActivityFixture(t, customLocalActivityAwk, log)
	if len(entries) != 3 || entries[0].Duration != nil || entries[1].Duration != nil || entries[2].Duration == nil || *entries[2].Duration != 0 {
		t.Fatalf("missing duration invented or unrelated record parsed: %+v", entries)
	}
}

func TestLocalActivityJSONEscaping(t *testing.T) {
	for _, name := range []string{"shop\"name", `shop\name`, `shop\"name`} {
		entries := parseLocalActivityFixture(t, customLocalActivityAwk, "CREATED /www/backup/site/shop/web_"+name+"_20261003_050001_site.tar.gz DURATION 1\n")
		if len(entries) != 1 || entries[0].Name != "Backup Website: "+name {
			t.Fatalf("name not JSON escaped: %+v", entries)
		}
	}
}

func TestLocalActivityConfiguredPaths(t *testing.T) {
	previous := backupSystemSettings()
	config := previous
	config.BackupRoot = "/srv/backups/customer.one"
	config.LogsDir = "/root/logs"
	activeBackupSystem.Store(config)
	t.Cleanup(func() { activeBackupSystem.Store(previous) })
	command := configuredBackupCommand(customLocalActivityCommand())
	if !strings.Contains(command, `"/root/logs/backup-site.log"`) || !strings.Contains(command, `"/root/logs/backup-database.log"`) {
		t.Fatalf("configured logs not used: %s", command)
	}
	filter := configuredBackupCommand(customLocalActivityAwk)
	entries := parseLocalActivityFixture(t, filter, "CREATED /srv/backups/customer.one/2026-10-03/site/shop/web_shop_20261003_050001_cron_0123456789abcdef_site.tar.gz DURATION 26\n")
	if len(entries) != 1 || entries[0].Duration == nil || *entries[0].Duration != 26 {
		t.Fatalf("configured backup root not parsed: %+v", entries)
	}
}
