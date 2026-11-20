package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Metrics are read alongside the shared local inventory in readServerStatus.
func serverStatusReadCommand() string {
	return `DISK=$(df -h / | awk 'NR==2 {printf "{\"total\":\"%s\",\"used\":\"%s\",\"free\":\"%s\",\"usage\":\"%s\"}", $2, $3, $4, $5}')
RAM=$(free -m | awk 'NR==2 {printf "{\"total\":\"%sMB\",\"used\":\"%sMB\",\"free\":\"%sMB\",\"usage\":\"%.1f%%\"}", $2, $3, $4, $3*100/$2}')
CPU=$(vmstat 1 2 | tail -1 | awk '{printf "%.1f%%",100-$15}')
UPTIME=$(uptime -p | sed 's/up //')
SERVER_TIME=$(date -Iseconds)
WEBSITES=$(find /www/wwwroot -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -v '/default$' | wc -l)
DATABASES=$(find /www/server/data -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -vE '/(mysql|performance_schema|sys|phpmyadmin)$' | wc -l)
printf '__METRICS__\n'
printf '{"server_time":"%s","disk":%s,"ram":%s,"cpu":"%s","uptime":"%s","websites":%s,"databases":%s}\n' "$SERVER_TIME" "$DISK" "$RAM" "$CPU" "$UPTIME" "$WEBSITES" "$DATABASES"
printf '__CRONTAB__\n'
crontab -l 2>/dev/null || true
`
}

func parseServerStatus(output string) ([]byte, error) {
	metrics := map[string]interface{}{}
	var inventory, cron strings.Builder
	hasInventory := false
	section := ""
	for _, line := range strings.Split(output, "\n") {
		switch line {
		case "__LOCAL_INVENTORY__":
			hasInventory = true
			section = line
			continue
		case "__METRICS__", "__CRONTAB__":
			section = line
			continue
		}
		switch section {
		case "__METRICS__":
			if strings.TrimSpace(line) != "" {
				if err := json.Unmarshal([]byte(line), &metrics); err != nil {
					return nil, fmt.Errorf("Thông số máy chủ không hợp lệ")
				}
			}
		case "__LOCAL_INVENTORY__":
			inventory.WriteString(line + "\n")
		case "__CRONTAB__":
			cron.WriteString(line + "\n")
		}
	}
	if len(metrics) == 0 {
		return nil, fmt.Errorf("Thiếu thông số máy chủ")
	}
	serverTime, _ := metrics["server_time"].(string)
	if hasInventory {
		metrics["local_backup"] = localBackupSummary(inventory.String(), serverTime)
	} else {
		metrics["local_backup_error"] = "Không thể đọc bản sao lưu trên máy chủ"
	}
	counts := map[string]int{"all": 0, "local": 0, "drive": 0}
	times := map[string][]string{}
	for _, job := range parseManagedCronJobs("__CRONTAB__\n" + cron.String()) {
		if !job.Enabled {
			continue
		}
		kind := "other"
		if strings.HasPrefix(job.ID, "backup-") {
			kind = "local"
		} else if job.ID == "drive-sync" {
			kind = "drive"
		}
		counts["all"]++
		counts[kind]++
		fields := strings.Fields(job.Schedule)
		if len(fields) < 2 {
			continue
		}
		minute, me := strconv.Atoi(fields[0])
		hour, he := strconv.Atoi(fields[1])
		if me == nil && he == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24 {
			clock := fmt.Sprintf("%02d:%02d", hour, minute)
			times["all"] = append(times["all"], clock)
			times[kind] = append(times[kind], clock)
		}
	}
	for _, kind := range []string{"all", "local", "drive"} {
		sort.Strings(times[kind])
		prefix := ""
		if kind != "all" {
			prefix = kind + "_"
		}
		metrics[prefix+"crons"] = counts[kind]
		metrics[prefix+"cron_times"] = strings.Join(times[kind], ", ")
	}
	return json.Marshal(metrics)
}

func localBackupSummary(inventory, serverTime string) map[string]interface{} {
	files := parseLocalInventory(inventory)
	now, timeErr := time.Parse(time.RFC3339, serverTime)
	firstDay, lastDay := now.AddDate(0, 0, -13).Format(time.DateOnly), now.Format(time.DateOnly)
	days := map[string]*backupHistoryDay{}
	var total int64
	for _, file := range files {
		total += file.Size
		day, _, _ := strings.Cut(file.Date, " ")
		if timeErr != nil || day < firstDay || day > lastDay {
			continue
		}
		if days[day] == nil {
			days[day] = &backupHistoryDay{Date: day}
		}
		days[day].Bytes += file.Size
		days[day].Files++
	}
	latestName, latestDate := "", ""
	if len(files) > 0 {
		latestName, latestDate = files[0].Name, strings.SplitN(files[0].Date, " ", 2)[0]
	}
	localBackup := map[string]interface{}{"count": len(files), "bytes": total, "size": backupSizeLabel(total), "latest_name": latestName, "latest_date": latestDate}
	if timeErr == nil {
		history := make([]backupHistoryDay, 0, len(days))
		for _, day := range days {
			history = append(history, *day)
		}
		sort.Slice(history, func(i, j int) bool { return history[i].Date < history[j].Date })
		localBackup["history"] = history
	}
	return localBackup
}

func backupSizeLabel(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, unit := range []string{"KB", "MB", "GB", "TB", "PB"} {
		value /= 1024
		if value < 1024 || unit == "PB" {
			return fmt.Sprintf("%.2f %s", value, unit)
		}
	}
	return "0 B"
}
