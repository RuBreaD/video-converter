package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr      string
	DataDir   string
	FFmpeg    string
	Workers   int
	QueueSize int
	MaxUpload int64
	Timeout   time.Duration
}

func FromEnv() (Config, error) {
	c := Config{Addr: env("ADDR", ":8080"), DataDir: env("DATA_DIR", "data"), FFmpeg: env("FFMPEG_PATH", "ffmpeg"), Workers: 2, QueueSize: 20, MaxUpload: 1 << 30, Timeout: 10 * time.Minute}
	var err error
	if c.Workers, err = intEnv("WORKERS", c.Workers); err != nil {
		return c, err
	}
	if c.QueueSize, err = intEnv("QUEUE_SIZE", c.QueueSize); err != nil {
		return c, err
	}
	if c.MaxUpload, err = int64Env("MAX_UPLOAD_BYTES", c.MaxUpload); err != nil {
		return c, err
	}
	seconds, err := intEnv("JOB_TIMEOUT_SECONDS", int(c.Timeout.Seconds()))
	if err != nil {
		return c, err
	}
	c.Timeout = time.Duration(seconds) * time.Second
	if c.Workers < 1 || c.QueueSize < 1 || c.MaxUpload < 1 || seconds < 1 {
		return c, errors.New("WORKERS, QUEUE_SIZE, MAX_UPLOAD_BYTES and JOB_TIMEOUT_SECONDS must be positive")
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	n, err := int64Env(key, int64(fallback))
	return int(n), err
}

func int64Env(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}
