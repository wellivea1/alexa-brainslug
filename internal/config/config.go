package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerAddr          string
	PublicBaseURL       string
	Provider            string
	OpenAI              OpenAIConfig
	Anthropic           AnthropicConfig
	SQLitePath          string
	AlexaVerifyRequests bool
	AlexaAllowedSkillID string
	LogLevel            string
	SaveHistory         bool
}

type OpenAIConfig struct {
	APIKey          string
	APIKeyFile      string
	Model           string
	BaseURL         string
	MaxOutputTokens int
	Timeout         time.Duration
}

type AnthropicConfig struct {
	APIKey          string
	APIKeyFile      string
	Model           string
	BaseURL         string
	MaxOutputTokens int
	Timeout         time.Duration
}

func LoadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNumber)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, parseDotEnvValue(value)); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, lineNumber, key, err)
		}
	}
	return scanner.Err()
}

func Load() (Config, error) {
	cfg := Config{
		ServerAddr:    envString("SERVER_ADDR", ":8080"),
		PublicBaseURL: envString("PUBLIC_BASE_URL", ""),
		Provider:      strings.ToLower(envString("AI_PROVIDER", "openai")),
		OpenAI: OpenAIConfig{
			APIKey:          envString("OPENAI_API_KEY", ""),
			APIKeyFile:      envString("OPENAI_API_KEY_FILE", ""),
			Model:           strings.TrimSpace(os.Getenv("OPENAI_MODEL")),
			BaseURL:         envString("OPENAI_BASE_URL", ""),
			MaxOutputTokens: envInt("OPENAI_MAX_OUTPUT_TOKENS", 600),
			Timeout:         time.Duration(envInt("OPENAI_TIMEOUT_SECONDS", 12)) * time.Second,
		},
		Anthropic: AnthropicConfig{
			APIKey:          envString("ANTHROPIC_API_KEY", ""),
			APIKeyFile:      envString("ANTHROPIC_API_KEY_FILE", ""),
			Model:           strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")),
			BaseURL:         envString("ANTHROPIC_BASE_URL", ""),
			MaxOutputTokens: envInt("ANTHROPIC_MAX_OUTPUT_TOKENS", 600),
			Timeout:         time.Duration(envInt("ANTHROPIC_TIMEOUT_SECONDS", 12)) * time.Second,
		},
		SQLitePath:          envString("SQLITE_PATH", "./brainslug.db"),
		AlexaVerifyRequests: envBool("ALEXA_VERIFY_REQUESTS", true),
		AlexaAllowedSkillID: envString("ALEXA_ALLOWED_SKILL_ID", ""),
		LogLevel:            envString("LOG_LEVEL", "info"),
		SaveHistory:         envBool("SAVE_HISTORY", true),
	}

	if strings.TrimSpace(cfg.OpenAI.APIKeyFile) != "" {
		secret, err := ReadSecretFile(cfg.OpenAI.APIKeyFile)
		if err != nil {
			return Config{}, fmt.Errorf("OPENAI_API_KEY_FILE: %w", err)
		}
		cfg.OpenAI.APIKey = secret
	}
	if strings.TrimSpace(cfg.Anthropic.APIKeyFile) != "" {
		secret, err := ReadSecretFile(cfg.Anthropic.APIKeyFile)
		if err != nil {
			return Config{}, fmt.Errorf("ANTHROPIC_API_KEY_FILE: %w", err)
		}
		cfg.Anthropic.APIKey = secret
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ServerAddr) == "" {
		return fmt.Errorf("SERVER_ADDR must not be empty")
	}
	if strings.TrimSpace(c.SQLitePath) == "" {
		return fmt.Errorf("SQLITE_PATH must not be empty")
	}
	switch c.Provider {
	case "openai":
		if strings.TrimSpace(c.OpenAI.Model) == "" {
			return fmt.Errorf("OPENAI_MODEL must be set explicitly")
		}
		if c.OpenAI.MaxOutputTokens < 16 {
			return fmt.Errorf("OPENAI_MAX_OUTPUT_TOKENS must be at least 16")
		}
		if c.OpenAI.Timeout <= 0 {
			return fmt.Errorf("OPENAI_TIMEOUT_SECONDS must be positive")
		}
	case "anthropic":
		if strings.TrimSpace(c.Anthropic.Model) == "" {
			return fmt.Errorf("ANTHROPIC_MODEL must be set explicitly")
		}
		if c.Anthropic.MaxOutputTokens < 16 {
			return fmt.Errorf("ANTHROPIC_MAX_OUTPUT_TOKENS must be at least 16")
		}
		if c.Anthropic.Timeout <= 0 {
			return fmt.Errorf("ANTHROPIC_TIMEOUT_SECONDS must be positive")
		}
	default:
		return fmt.Errorf("AI_PROVIDER must be one of openai, anthropic")
	}
	return nil
}

func (c Config) SelectedModel() string {
	switch c.Provider {
	case "anthropic":
		return c.Anthropic.Model
	default:
		return c.OpenAI.Model
	}
}

func (c Config) SelectedMaxOutputTokens() int {
	switch c.Provider {
	case "anthropic":
		return c.Anthropic.MaxOutputTokens
	default:
		return c.OpenAI.MaxOutputTokens
	}
}

func (c Config) SecretValues() []string {
	return []string{c.OpenAI.APIKey, c.Anthropic.APIKey}
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseDotEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return value
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return strings.TrimSuffix(strings.TrimPrefix(value, "'"), "'")
	}
	return value
}

func ReadSecretFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var values []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if value := secretLineValue(line); value != "" {
			values = append(values, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", fmt.Errorf("file contains no secret value")
	}
	if len(values) > 1 {
		return "", fmt.Errorf("file must contain exactly one non-comment secret value")
	}
	return values[0], nil
}

func secretLineValue(line string) string {
	for _, delimiter := range []string{"=", ":"} {
		if idx := strings.Index(line, delimiter); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if isConfigLikeKey(key) {
				return strings.Trim(strings.TrimSpace(line[idx+1:]), `"`)
			}
		}
	}
	return strings.Trim(strings.TrimSpace(line), `"`)
}

func isConfigLikeKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}
