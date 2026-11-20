package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MySQLConfig holds the MySQL connection parameters. The password is only ever
// read from the environment (or a local .env), never from source code.
type MySQLConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DB       string
}

// Config is the fully resolved runtime configuration.
type Config struct {
	Addr              string
	DataDir           string
	MySQL             MySQLConfig
	Secret            []byte
	AllowRegistration bool
	TTSBaseURL        string
	TTSTimeout        time.Duration
	FFmpegPath        string
	ZlibDomains       []string
	MaxDownloadBytes  int64
	MaxJobChars       int
	SessionTTL        time.Duration
	TokenTTL          time.Duration
}

// Load reads .env (if present), then environment variables, validates the
// result and prepares the data directory and encryption secret.
func Load() (*Config, error) {
	_ = loadDotEnv(".env")

	cfg := &Config{
		Addr:              env("ECHO_ADDR", "127.0.0.1:8090"),
		DataDir:           env("ECHO_DATA_DIR", "data"),
		AllowRegistration: envBool("ECHO_ALLOW_REGISTRATION", true),
		TTSBaseURL:        env("TTS_BASE_URL", "http://127.0.0.1:8123"),
		TTSTimeout:        envDuration("TTS_TIMEOUT", 300*time.Second),
		FFmpegPath:        env("FFMPEG_PATH", ""),
		MaxDownloadBytes:  int64(envInt("MAX_DOWNLOAD_MB", 500)) * 1024 * 1024,
		MaxJobChars:       envInt("MAX_JOB_CHARS", 500000),
		SessionTTL:        envDuration("SESSION_TTL", 720*time.Hour),
		TokenTTL:          envDuration("ECHO_TOKEN_TTL", 720*time.Hour),
		MySQL: MySQLConfig{
			Host:     env("ECHO_MYSQL_HOST", "127.0.0.1"),
			Port:     envInt("ECHO_MYSQL_PORT", 3306),
			User:     env("ECHO_MYSQL_USER", "echo"),
			Password: env("ECHO_MYSQL_PASSWORD", ""),
			DB:       env("ECHO_MYSQL_DB", "echo_service"),
		},
	}
	if cfg.MySQL.Password == "" {
		return nil, errors.New("ECHO_MYSQL_PASSWORD 未设置：请写入 .env 或环境变量")
	}
	if cfg.MySQL.DB == "" {
		return nil, errors.New("ECHO_MYSQL_DB 不能为空")
	}

	rawDomains := env("ZLIB_DOMAINS",
		"https://z-lib.gd,https://z-lib.gl,https://z-library.ec,https://zlib.bz")
	for _, d := range strings.Split(rawDomains, ",") {
		if d = strings.TrimSpace(d); d != "" {
			cfg.ZlibDomains = append(cfg.ZlibDomains, strings.TrimRight(d, "/"))
		}
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}
	secret, err := loadSecret(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	cfg.Secret = secret
	return cfg, nil
}

// loadSecret returns a 32-byte AES key from ECHO_SECRET (base64 or hex),
// or from <data>/secret.key, generating the file on first start.
func loadSecret(dataDir string) ([]byte, error) {
	if raw := os.Getenv("ECHO_SECRET"); raw != "" {
		key, err := decodeKey(raw)
		if err != nil {
			return nil, fmt.Errorf("ECHO_SECRET 无效: %w", err)
		}
		return key, nil
	}
	path := filepath.Join(dataDir, "secret.key")
	if data, err := os.ReadFile(path); err == nil {
		return decodeKey(strings.TrimSpace(string(data)))
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("写入 %s: %w", path, err)
	}
	return key, nil
}

func decodeKey(raw string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("需要 32 字节（base64 或 hex）")
}

// loadDotEnv parses a minimal KEY=VALUE file and sets unset variables.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return sc.Err()
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}
