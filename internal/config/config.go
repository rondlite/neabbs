// Package config reads all configuration from environment variables.
package config

import "os"

// Config holds runtime configuration. Env vars only, per spec.
type Config struct {
	Listen     string // NEABBS_LISTEN
	DBPath     string // NEABBS_DB
	HostKey    string // NEABBS_HOSTKEY
	ContentDir string // NEABBS_CONTENT
	BaudOff    bool   // NEABBS_BAUD=0 disables baud emulation (dev)

	LLMBaseURL string // LLM_BASE_URL; empty = LLM disabled
	LLMModel   string // LLM_MODEL
	LLMAPIKey  string // LLM_API_KEY
}

// FromEnv builds a Config from the environment with spec defaults.
func FromEnv() Config {
	return Config{
		Listen:     envOr("NEABBS_LISTEN", ":2222"),
		DBPath:     envOr("NEABBS_DB", "./neabbs.db"),
		HostKey:    envOr("NEABBS_HOSTKEY", "./hostkey"),
		ContentDir: envOr("NEABBS_CONTENT", "./content"),
		BaudOff:    os.Getenv("NEABBS_BAUD") == "0",
		LLMBaseURL: os.Getenv("LLM_BASE_URL"),
		LLMModel:   os.Getenv("LLM_MODEL"),
		LLMAPIKey:  os.Getenv("LLM_API_KEY"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
