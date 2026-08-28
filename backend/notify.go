package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// sendNotifications dispatches a backup alert to all enabled platforms
func sendNotifications(fileName string, status string) {
	fmt.Printf("[notify] sendNotifications: fileName=%q status=%q isSuccess=%v\n", fileName, status, isSuccessStatus(status))

	cfg := loadNotificationConfig()

	go sendDiscord(fileName, status, cfg)
	go sendTelegram(fileName, status, cfg)
	go sendGmail(fileName, status, cfg)
}

// sendTestNotifications sends a test alert using the provided config (no save needed)
func sendTestNotifications(cfg NotificationConfig) {
	sendDiscord("test-file", "Success", cfg)
	sendTelegram("test-file", "Success", cfg)
	sendGmail("test-file", "Success", cfg)
}

// buildMessage returns platform-agnostic text for an alert
func buildMessage(fileName string, status string) string {
	if isSuccessStatus(status) {
		return fmt.Sprintf("✅ Backup THÀNH CÔNG: file %s đã được sao lưu an toàn.", fileName)
	}
	return fmt.Sprintf("🚨 Backup THẤT BẠI: file %s không thể sao lưu. Vui lòng kiểm tra ngay!", fileName)
}

// isSuccessStatus đánh giá một status có phải là thành công hay không.
// Status có thể kèm ghi chú (vd "Success (tạo thủ công: xxx)") nên dùng prefix.
func isSuccessStatus(status string) bool {
	s := strings.TrimSpace(status)
	return s == "Success" || strings.HasPrefix(s, "Success")
}

// ---------- Discord ----------
func sendDiscord(fileName string, status string, cfg NotificationConfig) {
	fmt.Printf("[notify] sendDiscord: enabled=%v webhook_set=%v isSuccess=%v\n", cfg.DiscordEnabled, cfg.DiscordWebhookURL != "", isSuccessStatus(status))

	if !cfg.DiscordEnabled {
		fmt.Println("[notify] sendDiscord: Discord disabled, skipping")
		return
	}
	if cfg.DiscordWebhookURL == "" {
		fmt.Println("[notify] sendDiscord: Webhook URL empty, skipping")
		return
	}

	color := 16711680 // red default
	if isSuccessStatus(status) {
		color = 65280 // green
	}

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       "Thông báo Backup",
				"description": buildMessage(fileName, status),
				"color":       color,
				"timestamp":   time.Now().Format(time.RFC3339),
			},
		},
	}
	jsonPayload, _ := json.Marshal(payload)
	resp, err := http.Post(cfg.DiscordWebhookURL, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		fmt.Println("Lỗi khi gửi Discord:", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Printf("Discord trả về lỗi %d: %s\n", resp.StatusCode, string(body))
	} else {
		fmt.Printf("Discord gửi thành công (HTTP %d)\n", resp.StatusCode)
	}
}

// ---------- Telegram ----------
func sendTelegram(fileName string, status string, cfg NotificationConfig) {
	if !cfg.TelegramEnabled {
		return
	}
	if cfg.TelegramBotToken == "" || cfg.TelegramChatID == "" {
		return
	}

	message := buildMessage(fileName, status)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.TelegramBotToken)
	form := url.Values{}
	form.Set("chat_id", cfg.TelegramChatID)
	form.Set("text", message)
	form.Set("parse_mode", "HTML")

	resp, err := http.PostForm(apiURL, form)
	if err != nil {
		fmt.Println("Lỗi khi gửi Telegram:", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}

// ---------- Gmail (SMTP) ----------
func sendGmail(fileName string, status string, cfg NotificationConfig) {
	if !cfg.GmailEnabled {
		return
	}
	if cfg.GmailEmail == "" || cfg.GmailAppPassword == "" || cfg.GmailTo == "" {
		return
	}

	host := cfg.GmailSMTPHost
	if host == "" {
		host = "smtp.gmail.com"
	}
	port := cfg.GmailSMTPPort
	if port == "" {
		port = "587"
	}

	subject := "Thông báo Backup: " + strings.ToUpper(status)
	body := buildMessage(fileName, status) + "\nThời gian: " + time.Now().Format("15:04:05 02/01/2006")

	msg := "To: " + cfg.GmailTo + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-version: 1.0;\r\n" +
		"Content-Type: text/plain; charset=\"UTF-8\";\r\n" +
		"\r\n" + body

	addr := host + ":" + port
	auth := smtp.PlainAuth("", cfg.GmailEmail, cfg.GmailAppPassword, host)

	// Send to both From and To per common practice
	to := []string{cfg.GmailTo}
	err := smtp.SendMail(addr, auth, cfg.GmailEmail, to, []byte(msg))
	if err != nil {
		fmt.Println("Lỗi khi gửi Gmail:", err)
		return
	}
}
