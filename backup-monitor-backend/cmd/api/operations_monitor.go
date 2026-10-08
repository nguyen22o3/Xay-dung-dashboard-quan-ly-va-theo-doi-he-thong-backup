package main

import (
	"backup-monitor-backend/internal/backupops"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var operationMarker = regexp.MustCompile(`^MONITOR_RUN\|([a-z-]+)\|(\d{4}-\d{2}-\d{2})\|(\d{2}:\d{2}:\d{2})\|(running|success|failed)\|(\d+)(?:\|([0-9]{10,30}-[0-9]{1,10}))?$`)

type operationMetric struct {
	DiskPercent   float64 `json:"diskPercent"`
	DriveFolders  *int    `json:"driveFolders"`
	DriveReadable bool    `json:"driveReadable"`
}

func collectOperationSnapshot() (operationMetric, error) {
	cfg := backupSystemSettings()
	var command strings.Builder
	command.WriteString(`set -e
disk=$(df -P / | awk 'NR==2{gsub(/%/,"",$5);print $5}')
`)
	command.WriteString(`printf 'METRIC|{"diskPercent":%s}\n' "$disk"
`)
	fmt.Fprintf(&command, "if dirs=$(/usr/bin/rclone lsf --dirs-only %q --config /root/.config/rclone/rclone.conf --contimeout 5s --timeout 10s 2>/dev/null); then printf 'DRIVE|'; printf '%%s\n' \"$dirs\" | grep -Ec '^20[0-9]{2}-[0-9]{2}-[0-9]{2}/$' || true; else printf 'DRIVE|unavailable\n'; fi\n", cfg.DriveRemote+":"+cfg.DriveFolder)
	output, err := executeSSHCommandRaw(command.String())
	var metric operationMetric
	parsed := false
	if err != nil {
		return metric, err
	}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 2 {
			continue
		}
		switch parts[0] {
		case "METRIC":
			if err = json.Unmarshal([]byte(parts[1]), &metric); err != nil {
				return metric, err
			}
			parsed = true
		case "DRIVE":
			if count, e := strconv.Atoi(parts[1]); e == nil {
				metric.DriveFolders = &count
				metric.DriveReadable = true
			}
		}
	}
	if !parsed || metric.DiskPercent < 0 || metric.DiskPercent > 100 {
		return metric, errors.New("invalid disk snapshot")
	}
	return metric, nil
}
func alertChannels(settings AlertSettings) []string {
	channels := []string{}
	safe := safeAlertSettings(settings)
	if safe.TelegramEnabled {
		channels = append(channels, "telegram")
	}
	if safe.DiscordEnabled {
		channels = append(channels, "discord")
	}
	if safe.SmtpEnabled {
		channels = append(channels, "email")
	}
	return channels
}

// Only lifecycle markers are read, never archive task output in a database.
type observedRun struct {
	JobID, RunID, Status string
	StartedAt            time.Time
}

func parseObservedRuns(raw string, location *time.Location) []observedRun {
	runs := map[string]observedRun{}
	cancelled := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) == 3 && parts[0] == "MONITOR_CANCEL" {
			cancelled[parts[1]+":"+parts[2]] = true
			continue
		}
		m := operationMarker.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if _, ok := managedCronTaskByID(m[1]); !ok {
			continue
		}
		date, err := time.ParseInLocation("2006-01-02 15:04:05", m[2]+" "+m[3], location)
		if err != nil {
			continue
		}
		id := m[6]
		if id == "" {
			id = m[2] + "T" + m[3]
		}
		key := m[1] + ":" + id
		old, exists := runs[key]
		if exists && old.Status != "running" && m[4] == "running" {
			continue
		}
		runs[key] = observedRun{JobID: m[1], RunID: id, StartedAt: date, Status: m[4]}
	}
	result := []observedRun{}
	for key, run := range runs {
		if cancelled[key] {
			run.Status = "cancelled"
		}
		result = append(result, run)
	}
	return result
}
func collectObservedRuns(location *time.Location) ([]observedRun, map[string]bool, error) {
	var cmd strings.Builder
	cmd.WriteString("set -euo pipefail\n")
	paths := []string{backupRunHistoryPath()}
	for _, task := range managedCronTasks() {
		paths = append(paths, task.Log)
	}
	for _, path := range paths {
		fmt.Fprintf(&cmd, "if [ -e %q ]; then [ -f %q ] && [ ! -L %q ] || exit 1; { grep -E '^MONITOR_(RUN|CANCEL)\\|' -- %q || [ \"$?\" -eq 1 ]; } | tail -n 2000; fi\n", path, path, path, path)
	}
	for _, task := range managedCronTasks() {
		fmt.Fprintf(&cmd, "if [ -f %q ] && [ ! -L %q ]; then printf 'LOG_AVAILABLE|%s\\n'; fi\n", task.Log, task.Log, task.ID)
	}
	raw, err := executeSSHCommandRaw(cmd.String())
	if err != nil {
		return nil, nil, err
	}
	available := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		if id, ok := strings.CutPrefix(line, "LOG_AVAILABLE|"); ok {
			if _, known := managedCronTaskByID(id); known {
				available[id] = true
			}
		}
	}
	return parseObservedRuns(raw, location), available, nil
}
func (p *operationStore) collectOperations(ctx context.Context) error {
	backupSystemMutation.RLock()
	defer backupSystemMutation.RUnlock()
	if !backupSystemLoaded.Load() {
		return errors.New("server settings unavailable")
	}
	s := p.snapshot()
	opts, since := s.Options, s.EnabledAt
	if !opts.Enabled {
		return nil
	}
	settings, err := readAlertSettings("alert_settings.json")
	if err != nil {
		return err
	}
	emit := func(key, message string) error { return p.enqueue(ctx, key, message, alertChannels(settings)) }
	runs, available, err := collectObservedRuns(p.location)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	recent := time.Now().AddDate(0, 0, -30)
	for _, run := range runs {
		if run.Status == "failed" && !run.StartedAt.Before(since) && !run.StartedAt.Before(recent) && ((strings.HasPrefix(run.JobID, "backup-") && opts.BackupFailed) || (run.JobID == "drive-sync" && opts.DriveFailed)) {
			if err = emit("failed:"+run.JobID+":"+run.RunID, "Tác vụ "+run.JobID+" thất bại lúc "+run.StartedAt.Format("02/01/2006 15:04:05")+". Mở Log trong Cron để kiểm tra."); err != nil {
				return err
			}
		}
	}
	metric, err := collectOperationSnapshot()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	today := time.Now().In(p.location).Format("2006-01-02")
	if metric.DiskPercent >= float64(opts.DiskThreshold) {
		if err = emit("disk:"+today, fmt.Sprintf("Ổ đĩa máy chủ đã dùng %.1f%% (ngưỡng %d%%).", metric.DiskPercent, opts.DiskThreshold)); err != nil {
			return err
		}
	}
	if metric.DriveReadable && metric.DriveFolders != nil && *metric.DriveFolders < settings.Threshold {
		if err = emit("drive-count:"+today, fmt.Sprintf("Google Drive có %d thư mục sao lưu, dưới ngưỡng %d.", *metric.DriveFolders, settings.Threshold)); err != nil {
			return err
		}
	}
	if !metric.DriveReadable && opts.DriveFailed {
		if err = emit("drive-unreachable:"+today, "Không đọc được Google Drive. Kiểm tra kết nối và xác thực rclone; không dùng số liệu cũ để kết luận backup bị mất."); err != nil {
			return err
		}
	}
	if opts.MissedSchedule {
		raw, e := fetchManagedCronJobs()
		if e != nil {
			return e
		}
		var jobs []managedCronJobResponse
		if json.Unmarshal(raw, &jobs) != nil {
			return errors.New("invalid cron snapshot")
		}
		now := time.Now().In(p.location)
		for _, job := range jobs {
			if !job.Enabled || !job.ScheduleTracked || !available[job.ID] {
				continue
			}
			due, ok := lastScheduledTime(job.Schedule, now.Add(-time.Duration(opts.GraceMinutes)*time.Minute))
			if !ok || due.Before(since) {
				continue
			}
			found := false
			for _, run := range runs {
				if run.JobID == job.ID && !run.StartedAt.Before(due) {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if err = emit("missed:"+job.ID+":"+due.Format(time.RFC3339), "Tác vụ "+job.ID+" chưa ghi nhận lần chạy theo lịch "+due.Format("02/01/2006 15:04")+" sau thời gian cho phép."); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := backupSystemOperation("restore-check-status", nil)
	if err != nil {
		return err
	}
	var checks struct {
		Entries []backupops.RestoreCheck `json:"entries"`
	}
	if json.Unmarshal(raw, &checks) != nil {
		return errors.New("invalid restore checks")
	}
	for _, check := range checks.Entries {
		if check.Status == "failed" && !check.StartedAt.Before(since) && !check.StartedAt.Before(recent) {
			if err = emit("restore-check:"+check.Token, "Kiểm tra khôi phục định kỳ thất bại. Kiểm tra log dịch vụ restore-check; dữ liệu đang chạy không bị thay đổi."); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// Full five-field cron match, including ranges/lists/steps and standard OR
// semantics for day-of-month/day-of-week. Unsupported expressions fail closed.
func cronField(value string, n, lo, hi int) bool {
	matched := false
	for _, part := range strings.Split(value, ",") {
		base, stepText, _ := strings.Cut(part, "/")
		step := 1
		if stepText != "" {
			v, e := strconv.Atoi(stepText)
			if e != nil || v < 1 {
				return false
			}
			step = v
		}
		start, end := lo, hi
		if base != "*" {
			a, b, rangePart := strings.Cut(base, "-")
			v, e := strconv.Atoi(a)
			if e != nil {
				return false
			}
			start = v
			end = v
			if rangePart {
				v, e = strconv.Atoi(b)
				if e != nil {
					return false
				}
				end = v
			} else if stepText != "" {
				end = hi
			}
		}
		if start < lo || end > hi || start > end {
			return false
		}
		if n >= start && n <= end && (n-start)%step == 0 {
			matched = true
		}
	}
	return matched
}
func lastScheduledTime(expression string, now time.Time) (time.Time, bool) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return time.Time{}, false
	}
	// Eight days covers supported daily/weekly schedules. Monthly schedules scan
	// up to 32 days, capped and using server-local calendar time through DST.
	candidate := now.Truncate(time.Minute)
	for i := 0; i < 32*24*60; i++ {
		week := int(candidate.Weekday())
		weekday := cronField(fields[4], week, 0, 7) || (week == 0 && cronField(fields[4], 7, 0, 7))
		dom := cronField(fields[2], candidate.Day(), 1, 31)
		dayMatch := dom && weekday
		if fields[2] != "*" && fields[4] != "*" {
			dayMatch = dom || weekday
		}
		if cronField(fields[0], candidate.Minute(), 0, 59) && cronField(fields[1], candidate.Hour(), 0, 23) && cronField(fields[3], int(candidate.Month()), 1, 12) && dayMatch {
			return candidate, true
		}
		candidate = candidate.Add(-time.Minute)
	}
	return time.Time{}, false
}

var notificationClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func sendNotification(ctx context.Context, channel, message string, settings AlertSettings) error {
	if len(message) > 3500 {
		message = message[:3500]
	}
	if validateAlertSettings(settings) != nil {
		return errors.New("invalid channel settings")
	}
	endpoint := ""
	var payload any
	switch channel {
	case "telegram":
		if !safeAlertSettings(settings).TelegramEnabled {
			return errors.New("channel disabled")
		}
		endpoint = "https://api.telegram.org/bot" + settings.TelegramToken + "/sendMessage"
		payload = map[string]string{"chat_id": settings.TelegramChat, "text": message}
	case "discord":
		if !safeAlertSettings(settings).DiscordEnabled {
			return errors.New("channel disabled")
		}
		endpoint = settings.DiscordWebhook
		payload = map[string]any{"content": message, "allowed_mentions": map[string]any{"parse": []string{}}}
	case "email":
		if !safeAlertSettings(settings).SmtpEnabled {
			return errors.New("channel disabled")
		}
		return sendAlertEmail(ctx, settings, message)
	default:
		return errors.New("invalid channel")
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := notificationClient.Do(req)
	if err != nil {
		return errors.New("notification network failure")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 65536))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("notification rejected")
	}
	if channel == "telegram" {
		var result struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal(body, &result) != nil || !result.OK {
			return errors.New("notification rejected")
		}
	}
	return nil
}
func sendAlertEmail(ctx context.Context, settings AlertSettings, message string) error {
	dialer := net.Dialer{Timeout: 15 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", "smtp.gmail.com:587")
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(connection, "smtp.gmail.com")
	if err != nil {
		return errors.New("SMTP unavailable")
	}
	defer client.Close()
	if err = client.StartTLS(&tls.Config{ServerName: "smtp.gmail.com", MinVersion: tls.VersionTLS12}); err != nil {
		return errors.New("SMTP TLS failed")
	}
	if err = client.Auth(smtp.PlainAuth("", settings.SmtpEmail, settings.SmtpPassword, "smtp.gmail.com")); err != nil {
		return errors.New("SMTP authentication failed")
	}
	if err = client.Mail(settings.SmtpEmail); err != nil {
		return errors.New("SMTP sender rejected")
	}
	if err = client.Rcpt(settings.TargetEmail); err != nil {
		return errors.New("SMTP recipient rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP message rejected")
	}
	body := "From: " + settings.SmtpEmail + "\r\nTo: " + settings.TargetEmail + "\r\nSubject: Backup Monitor alert\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte(message)) + "\r\n"
	if _, err = io.WriteString(writer, body); err != nil {
		return errors.New("SMTP write failed")
	}
	if err = writer.Close(); err != nil {
		return errors.New("SMTP send failed")
	}
	return client.Quit()
}
func startOperations(ctx context.Context, p *operationStore) {
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if !p.snapshot().Options.Enabled {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					continue
				}
			}
			err := p.collectOperations(ctx)
			if err != nil {
				log.Print("Monitoring failed; JSON state preserved. Check server connectivity.")
			}
			if ctx.Err() == nil {
				if p.update(ctx, func(s *operationsState) error {
					s.CollectionFailed = err != nil
					if err == nil {
						now := time.Now().UTC()
						s.CollectedAt = &now
					}
					return nil
				}) != nil {
					log.Print("Cannot persist monitoring status")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	go func() {
		defer p.wg.Done()
		timer := time.NewTicker(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				for i := 0; i < 10; i++ {
					taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					d, err := p.claimDelivery(taskCtx)
					if err != nil {
						cancel()
						break
					}
					settings, err := readAlertSettings("alert_settings.json")
					if err == nil {
						err = sendNotification(taskCtx, d.Channel, d.Message, settings)
					}
					// Record a completed send even when shutdown has cancelled the network context.
					if p.finishDelivery(context.Background(), d, err) != nil {
						log.Print("Notification outcome unconfirmed; an interrupted send may be retried")
					}
					cancel()
				}
			}
		}
	}()
}
