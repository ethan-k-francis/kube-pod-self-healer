// Package config loads the health agent's configuration from environment
// variables. Using env vars follows the 12-factor app methodology — config
// lives in the environment, not in code, making the agent portable across
// clusters without rebuilding.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the health agent.
// Every field maps to an environment variable with a sensible default.
type Config struct {
	// Kubeconfig is the path to a kubeconfig file. Empty string means
	// "use in-cluster config" — which is what you want when running as a pod.
	Kubeconfig string

	// Namespace to watch. Empty string means watch all namespaces.
	Namespace string

	// CheckInterval controls how often the watcher reconciles pod state.
	// The informer provides real-time events, but this interval catches
	// anything that might slip through (belt and suspenders).
	CheckInterval time.Duration

	// RemediationURL is the HTTP endpoint of the Python remediation service.
	// Events are POSTed here when failures are detected.
	RemediationURL string

	// WebhookURL is an optional notification endpoint (Slack, Discord, etc).
	// If empty, webhook notifications are disabled.
	WebhookURL string

	// LogLevel controls verbosity: "debug", "info", "warn", "error"
	LogLevel string
}

// Load reads configuration from environment variables, applying defaults
// where values aren't set. Returns an error if any required parsing fails.
func Load() (*Config, error) {
	// Parse the check interval with a 30-second default. This is frequent
	// enough to catch issues quickly without hammering the API server.
	interval, err := parseDuration("CHECK_INTERVAL", "30s")
	if err != nil {
		return nil, fmt.Errorf("invalid CHECK_INTERVAL: %w", err)
	}

	cfg := &Config{
		Kubeconfig:     getEnv("KUBECONFIG", ""),
		Namespace:      getEnv("NAMESPACE", "kube-remediator"),
		CheckInterval:  interval,
		RemediationURL: getEnv("REMEDIATION_URL", "http://remediation-service:8000"),
		WebhookURL:     getEnv("WEBHOOK_URL", ""),
		LogLevel:       strings.ToLower(getEnv("LOG_LEVEL", "info")),
	}

	return cfg, nil
}

// String returns a human-readable representation of the config,
// useful for startup logging. Sensitive values are masked.
func (c *Config) String() string {
	webhookStatus := "disabled"
	if c.WebhookURL != "" {
		webhookStatus = "enabled"
	}

	kubeconfigSource := "in-cluster"
	if c.Kubeconfig != "" {
		kubeconfigSource = c.Kubeconfig
	}

	return fmt.Sprintf(
		"Config{kubeconfig=%s, namespace=%s, interval=%s, remediation=%s, webhook=%s, log_level=%s}",
		kubeconfigSource,
		c.Namespace,
		c.CheckInterval,
		c.RemediationURL,
		webhookStatus,
		c.LogLevel,
	)
}

// getEnv reads an environment variable with a fallback default.
// This pattern avoids the verbosity of checking os.Getenv + empty string
// throughout the codebase.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// parseDuration reads a duration string from an env var (e.g., "30s", "1m").
// If the env var is a plain integer, it's treated as seconds for convenience.
func parseDuration(key, fallback string) (time.Duration, error) {
	raw := getEnv(key, fallback)

	// Try parsing as a Go duration string first ("30s", "1m", "500ms")
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}

	// Fall back to treating it as an integer number of seconds.
	// This makes it friendlier for users who just want "30" instead of "30s".
	if secs, err := strconv.Atoi(raw); err == nil {
		return time.Duration(secs) * time.Second, nil
	}

	return 0, fmt.Errorf("cannot parse %q=%q as duration or seconds", key, raw)
}
