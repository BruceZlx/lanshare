package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort = 8612
)

// Config 是持久化在本机的配置。
type Config struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Port        int    `json:"port"`
	DownloadDir string `json:"download_dir"`
	AutoAccept  bool   `json:"auto_accept"`
}

func configDir() (string, error) {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "lanshare"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lanshare"), nil
}

func loadConfig() (*Config, error) {
	cfg := &Config{}
	if dir, err := configDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}
	if cfg.ID == "" {
		cfg.ID = randomID()
	}
	if cfg.Name == "" {
		cfg.Name = defaultName()
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = defaultPort
	}
	if cfg.DownloadDir == "" {
		cfg.DownloadDir = defaultDownloadDir()
	}
	if err := cfg.Save(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) Save() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644)
}

func defaultName() string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		h = "my-" + runtime.GOOS
	}
	h = strings.TrimSuffix(h, ".local")
	h = strings.TrimSpace(h)
	if len(h) > 48 {
		h = h[:48]
	}
	return h
}

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Downloads", "LanShare")
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}
