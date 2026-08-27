package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// NotificationConfig holds all notification platform settings
type NotificationConfig struct {
	DiscordWebhookURL string `json:"discord_webhook_url"`
	DiscordEnabled    bool   `json:"discord_enabled"`

	GmailSMTPHost    string `json:"gmail_smtp_host"`
	GmailSMTPPort    string `json:"gmail_smtp_port"`
	GmailEmail       string `json:"gmail_email"`
	GmailAppPassword string `json:"gmail_app_password"`
	GmailTo          string `json:"gmail_to"`
	GmailEnabled     bool   `json:"gmail_enabled"`

	TelegramBotToken string `json:"telegram_bot_token"`
	TelegramChatID   string `json:"telegram_chat_id"`
	TelegramEnabled  bool   `json:"telegram_enabled"`
}

// envKeys maps config keys to .env variable names
var envKeys = map[string]string{
	"discord_webhook_url":  "DISCORD_WEBHOOK_URL",
	"discord_enabled":      "DISCORD_ENABLED",
	"gmail_smtp_host":      "GMAIL_SMTP_HOST",
	"gmail_smtp_port":      "GMAIL_SMTP_PORT",
	"gmail_email":          "GMAIL_EMAIL",
	"gmail_app_password":   "GMAIL_APP_PASSWORD",
	"gmail_to":             "GMAIL_TO",
	"gmail_enabled":        "GMAIL_ENABLED",
	"telegram_bot_token":   "TELEGRAM_BOT_TOKEN",
	"telegram_chat_id":     "TELEGRAM_CHAT_ID",
	"telegram_enabled":     "TELEGRAM_ENABLED",
}

var appEnvPath = ".env"

// loadNotificationConfig reads notification settings from environment variables
func loadNotificationConfig() NotificationConfig {
	return NotificationConfig{
		DiscordWebhookURL: os.Getenv("DISCORD_WEBHOOK_URL"),
		DiscordEnabled:    os.Getenv("DISCORD_ENABLED") == "true",

		GmailSMTPHost:    os.Getenv("GMAIL_SMTP_HOST"),
		GmailSMTPPort:    os.Getenv("GMAIL_SMTP_PORT"),
		GmailEmail:       os.Getenv("GMAIL_EMAIL"),
		GmailAppPassword: os.Getenv("GMAIL_APP_PASSWORD"),
		GmailTo:          os.Getenv("GMAIL_TO"),
		GmailEnabled:     os.Getenv("GMAIL_ENABLED") == "true",

		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
		TelegramEnabled:  os.Getenv("TELEGRAM_ENABLED") == "true",
	}
}

// saveNotificationConfig writes notification settings into the .env file
func saveNotificationConfig(cfg NotificationConfig) error {
	lines := make(map[string]string)
	for _, v := range []string{"discord", "gmail", "telegram"} {
		_ = v
	}
	lines["DISCORD_WEBHOOK_URL"] = cfg.DiscordWebhookURL
	lines["DISCORD_ENABLED"] = fmt.Sprintf("%v", cfg.DiscordEnabled)
	lines["GMAIL_SMTP_HOST"] = cfg.GmailSMTPHost
	lines["GMAIL_SMTP_PORT"] = cfg.GmailSMTPPort
	lines["GMAIL_EMAIL"] = cfg.GmailEmail
	lines["GMAIL_APP_PASSWORD"] = cfg.GmailAppPassword
	lines["GMAIL_TO"] = cfg.GmailTo
	lines["GMAIL_ENABLED"] = fmt.Sprintf("%v", cfg.GmailEnabled)
	lines["TELEGRAM_BOT_TOKEN"] = cfg.TelegramBotToken
	lines["TELEGRAM_CHAT_ID"] = cfg.TelegramChatID
	lines["TELEGRAM_ENABLED"] = fmt.Sprintf("%v", cfg.TelegramEnabled)

	updated := updateEnvFile(lines)
	if !updated {
		// If file doesn't exist or update failed, append all
		f, err := os.OpenFile(appEnvPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()

		// Ensure newline before appending
		if fi, _ := f.Stat(); fi.Size() > 0 {
			f.WriteString("\n")
		}

		w := bufio.NewWriter(f)
		for k, v := range lines {
			fmt.Fprintf(w, "%s=%s\n", k, v)
		}
		w.Flush()
	}

	// Reload env values into runtime
	for k, v := range lines {
		os.Setenv(k, v)
	}

	return nil
}

// updateEnvFile updates existing keys in the .env file. Returns true if file was modified.
func updateEnvFile(updates map[string]string) bool {
	f, err := os.Open(appEnvPath)
	if err != nil {
		return false
	}
	defer f.Close()

	var newLines []string
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			newLines = append(newLines, line)
			continue
		}
		key := strings.SplitN(trimmed, "=", 2)[0]
		key = strings.TrimSpace(key)
		if _, ok := updates[key]; ok {
			newLines = append(newLines, key+"="+updates[key])
			seen[key] = true
		} else {
			newLines = append(newLines, line)
		}
	}

	// Append keys not present in file
	for k, v := range updates {
		if !seen[k] {
			newLines = append(newLines, k+"="+v)
		}
	}

	content := strings.Join(newLines, "\n") + "\n"
	if err := os.WriteFile(appEnvPath, []byte(content), 0644); err != nil {
		return false
	}
	return true
}
