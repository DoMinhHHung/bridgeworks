package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func requiredValue(lookup lookupEnvFunc, key string) (string, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return strings.TrimSpace(value), nil
}
func nonEmptyValue(lookup lookupEnvFunc, key, fallback string) (string, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return value, nil
}
func durationValue(lookup lookupEnvFunc, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration", key)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return value, nil
}
func int32Value(lookup lookupEnvFunc, key string, fallback int32) (int32, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid 32-bit integer", key)
	}
	return int32(value), nil
}
func int64Value(lookup lookupEnvFunc, key string, fallback int64) (int64, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer", key)
	}
	return value, nil
}
func originValue(lookup lookupEnvFunc, key string) (string, error) {
	value, err := requiredValue(lookup, key)
	if err != nil {
		return "", err
	}
	if err := validateOrigin(key, value); err != nil {
		return "", err
	}
	return value, nil
}
func authorizedPartiesValue(lookup lookupEnvFunc) ([]string, error) {
	raw, err := requiredValue(lookup, "CLERK_AUTHORIZED_PARTIES")
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	parties := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		party := strings.TrimSpace(item)
		if party == "" {
			return nil, fmt.Errorf("CLERK_AUTHORIZED_PARTIES must not contain empty items")
		}
		if _, exists := seen[party]; exists {
			return nil, fmt.Errorf("CLERK_AUTHORIZED_PARTIES must not contain duplicates")
		}
		if err := validateOrigin("CLERK_AUTHORIZED_PARTIES", party); err != nil {
			return nil, err
		}
		seen[party] = struct{}{}
		parties = append(parties, party)
	}
	if len(parties) == 0 {
		return nil, fmt.Errorf("CLERK_AUTHORIZED_PARTIES must contain at least one party")
	}
	return parties, nil
}
func validateOrigin(key, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("%s must be a valid origin URL", key)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return fmt.Errorf("%s must be an origin without path, query, fragment, or user information", key)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		if isLocalHostname(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("%s must use HTTPS outside localhost or 127.0.0.1", key)
	default:
		return fmt.Errorf("%s must use HTTP or HTTPS", key)
	}
}
func serviceURLValue(lookup lookupEnvFunc, key, fallback string) (string, error) {
	value, ok := lookup(key)
	if !ok {
		value = fallback
	}
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return "", fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", key)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%s must not include user information, query, or fragment", key)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("%s must not include a path", key)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s must use HTTP or HTTPS", key)
	}
	if parsed.Scheme == "http" && !isPrivateOrLocalHost(parsed.Hostname()) {
		return "", fmt.Errorf("%s must use HTTPS outside a trusted local or private network", key)
	}
	return strings.TrimRight(value, "/"), nil
}
func isLocalHostname(host string) bool { return host == "localhost" || host == "127.0.0.1" }
func isPrivateOrLocalHost(host string) bool {
	if isLocalHostname(host) || host == "identity-service" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}
func logLevelValue(lookup lookupEnvFunc, key, fallback string) (slog.Level, error) {
	raw, ok := lookup(key)
	if !ok {
		raw = fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%s must be one of debug, info, warn, error", key)
	}
}
