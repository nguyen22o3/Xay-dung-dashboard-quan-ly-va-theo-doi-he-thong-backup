package main

import (
	"fmt"
	"strings"
)

type alertScriptUpdate struct {
	Task   managedCronTask
	Script string
}

// Alert settings apply to every existing integrity-check instance, without
// replacing the destination or wrapper selected when its cron was created.
func integrityAlertScriptUpdates(settings AlertSettings) []alertScriptUpdate {
	updates := []alertScriptUpdate{}
	cfg := backupSystemSettings()
	for _, task := range managedCronTasks() {
		if task.ID != "integrity-check" || task.InstanceKey == "" {
			continue
		}
		local, err := remoteCronSettings(cfg, task.ID, task.InstanceKey)
		if err != nil {
			continue
		}
		script := buildIntegrityAlertScript(settings, local)
		script = strings.Replace(script, "#!/bin/bash\n", "#!/bin/bash\nexport BACKUP_MONITOR_CRON="+activityShellQuote(task.selector())+"\n", 1)
		updates = append(updates, alertScriptUpdate{Task: task, Script: script})
	}
	return updates
}

func stageIntegrityAlertUpdates(updates []alertScriptUpdate) string {
	var command strings.Builder
	for _, update := range updates {
		// Read cron only. Both paused entries and instance-specific wrappers
		// retain their schedules; removed tasks must not be recreated by Save.
		fmt.Fprintf(&command, `if printf '%%s\n' "$CURRENT_CRON" | awk -v script=%s -v wrapper=%s -v id=%s '
  { if (index($0, "# backup-monitor-paused ") == 1) sub(/^# backup-monitor-paused /, "") }
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && ($7 == script || ($7 == wrapper && $8 == id)) { found = 1 }
  END { exit !found }
'; then
  stage_alert_script %s %s 1
fi
`, activityShellQuote(update.Task.Script), activityShellQuote(update.Task.wrapper()), activityShellQuote(update.Task.ID),
			activityShellQuote(update.Task.Script), activityShellQuote(encodeForShell(update.Script)))
	}
	return command.String()
}
