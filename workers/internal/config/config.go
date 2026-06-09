package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL      string
	RedisURL         string
	NATSURL          string
	S3Endpoint       string
	S3PublicEndpoint string
	S3Bucket         string
	S3AccessKey      string
	S3SecretKey      string
	S3Region         string
	MastersPath      string
	// SegmentsPath is the directory under which ffmpeg writes per-station
	// rotating ADTS-AAC evidence segments. The supervisor creates one
	// subdirectory per active station inside this path. Defaults to
	// /data/segments when unset.
	SegmentsPath string
	APIPort      string

	// ── Notificações por email (emails diários de alerta de campanha) ──
	NotificationsEnabled  bool   // NOTIFICATIONS_ENABLED ("true" liga o job)
	NotificationsBaseURL  string // NOTIFICATIONS_BASE_URL (base dos links/CTA)
	NotificationsSendHour int    // NOTIFICATIONS_SEND_HOUR (hora local BRT de corte)
	SMTPHost              string // SMTP_HOST
	SMTPPort              int    // SMTP_PORT
	SMTPUser              string // SMTP_USER
	SMTPPass              string // SMTP_PASS
	MailFrom              string // MAIL_FROM (default = SMTPUser)
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		RedisURL:         os.Getenv("REDIS_URL"),
		NATSURL:          os.Getenv("NATS_URL"),
		S3Endpoint:       os.Getenv("S3_ENDPOINT"),
		S3PublicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
		S3Bucket:         os.Getenv("S3_BUCKET"),
		S3AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:      os.Getenv("S3_SECRET_KEY"),
		S3Region:         os.Getenv("S3_REGION"),
		MastersPath:      os.Getenv("MASTERS_PATH"),
		SegmentsPath:     os.Getenv("SEGMENTS_PATH"),
		APIPort:          os.Getenv("API_PORT"),
	}
	required := map[string]string{
		"DATABASE_URL":  cfg.DatabaseURL,
		"NATS_URL":      cfg.NATSURL,
		"S3_ENDPOINT":   cfg.S3Endpoint,
		"S3_BUCKET":     cfg.S3Bucket,
		"S3_ACCESS_KEY": cfg.S3AccessKey,
		"S3_SECRET_KEY": cfg.S3SecretKey,
		"MASTERS_PATH":  cfg.MastersPath,
	}
	for k, v := range required {
		if v == "" {
			return nil, fmt.Errorf("config: missing required env %s", k)
		}
	}
	if cfg.S3Region == "" {
		cfg.S3Region = "us-east-1"
	}
	if cfg.APIPort == "" {
		cfg.APIPort = "8080"
	}
	if cfg.SegmentsPath == "" {
		cfg.SegmentsPath = "/data/segments"
	}

	cfg.NotificationsEnabled = os.Getenv("NOTIFICATIONS_ENABLED") == "true"
	cfg.NotificationsBaseURL = os.Getenv("NOTIFICATIONS_BASE_URL")
	if cfg.NotificationsBaseURL == "" {
		cfg.NotificationsBaseURL = "https://e-monitor.online"
	}
	cfg.NotificationsSendHour = 8
	if v := os.Getenv("NOTIFICATIONS_SEND_HOUR"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h >= 0 && h <= 23 {
			cfg.NotificationsSendHour = h
		}
	}
	cfg.SMTPHost = os.Getenv("SMTP_HOST")
	if cfg.SMTPHost == "" {
		cfg.SMTPHost = "smtp.gmail.com"
	}
	cfg.SMTPPort = 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.SMTPPort = p
		}
	}
	cfg.SMTPUser = os.Getenv("SMTP_USER")
	cfg.SMTPPass = os.Getenv("SMTP_PASS")
	cfg.MailFrom = os.Getenv("MAIL_FROM")
	if cfg.MailFrom == "" {
		cfg.MailFrom = cfg.SMTPUser
	}

	return cfg, nil
}
