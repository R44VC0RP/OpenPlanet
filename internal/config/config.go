package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	Host             string
	Port             string
	HostKeyPath      string
	DatabaseURL      string
	SampleGameURL    string
	BlobfieldGameURL string
	GGPIssuer        string
	GGPSessionSecret string
}

func Load() Config {
	loadDotEnv(".env")

	return Config{
		Host:             env("GATEWAY_HOST", "0.0.0.0"),
		Port:             env("GATEWAY_PORT", "2222"),
		HostKeyPath:      env("HOST_KEY_PATH", ".ssh/id_ed25519"),
		DatabaseURL:      env("DATABASE_URL", ""),
		SampleGameURL:    env("SAMPLE_GAME_URL", "ws://localhost:8081/ggp"),
		BlobfieldGameURL: env("BLOBFIELD_GAME_URL", "ws://localhost:8082/ggp"),
		GGPIssuer:        env("GGP_ISSUER", "gamegateway"),
		GGPSessionSecret: env("GGP_SESSION_SECRET", ""),
	}
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
