package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL  string
	RedisURL     string
	NATSURL      string
	S3Endpoint   string
	S3Bucket     string
	S3AccessKey  string
	S3SecretKey  string
	S3Region     string
	MastersPath  string
	APIPort      string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		NATSURL:     os.Getenv("NATS_URL"),
		S3Endpoint:  os.Getenv("S3_ENDPOINT"),
		S3Bucket:    os.Getenv("S3_BUCKET"),
		S3AccessKey: os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey: os.Getenv("S3_SECRET_KEY"),
		S3Region:    os.Getenv("S3_REGION"),
		MastersPath: os.Getenv("MASTERS_PATH"),
		APIPort:     os.Getenv("API_PORT"),
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
	return cfg, nil
}
