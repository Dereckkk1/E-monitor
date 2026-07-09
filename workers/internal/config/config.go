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

	// ── Retenção local de evidência (§11.4 variante de prod, incidente
	// 2026-07-02) ──
	// EvidenceRetentionDays: idade máxima (dias) de um clipe de evidência no
	// MinIO antes do prune apagar o objeto e marcar a detecção 'expired'.
	// Default 30. EvidencePruneDryRun: quando true, o prune só LOGA o que
	// apagaria, sem tocar no storage (válvula de segurança do 1º rollout).
	EvidenceRetentionDays int  // EVIDENCE_RETENTION_DAYS
	EvidencePruneDryRun   bool // EVIDENCE_PRUNE_DRY_RUN ("true" só simula)

	// ── Notificações por email (emails diários de alerta de campanha) ──
	NotificationsEnabled  bool   // NOTIFICATIONS_ENABLED ("true" liga o job)
	NotificationsBaseURL  string // NOTIFICATIONS_BASE_URL (base dos links/CTA)
	NotificationsSendHour int    // NOTIFICATIONS_SEND_HOUR (hora local BRT de corte)
	SMTPHost              string // SMTP_HOST
	SMTPPort              int    // SMTP_PORT
	SMTPUser              string // SMTP_USER
	SMTPPass              string // SMTP_PASS
	MailFrom              string // MAIL_FROM (default = SMTPUser)

	// ── Central de Sugestões ──
	// SuggestionsDevEmail: email do dev que enxerga a "Central de Comando"
	// (todas as sugestões, triage, notas privadas). Qualquer outro admin/operator
	// vira "Autor" (só as próprias). Configurável por env pra ser testável sem
	// recompilar; cai no default seguro quando ausente.
	SuggestionsDevEmail string // SUGGESTIONS_DEV_EMAIL
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

	cfg.EvidenceRetentionDays = 30
	if v := os.Getenv("EVIDENCE_RETENTION_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.EvidenceRetentionDays = d
		}
	}
	cfg.EvidencePruneDryRun = os.Getenv("EVIDENCE_PRUNE_DRY_RUN") == "true"

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

	cfg.SuggestionsDevEmail = os.Getenv("SUGGESTIONS_DEV_EMAIL")
	if cfg.SuggestionsDevEmail == "" {
		cfg.SuggestionsDevEmail = "tatico3@hubradios.com"
	}

	return cfg, nil
}
