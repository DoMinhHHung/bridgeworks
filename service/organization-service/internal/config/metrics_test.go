package config

import (
	"strings"
	"testing"
)

func TestLoadMetricsAddr(t *testing.T) {
	tests := []struct {
		name string
		env map[string]string
		httpAddr string
		want string
		wantError string
	}{
		{name: "default", env: map[string]string{}, httpAddr: ":8080", want: ":9090"},
		{name: "override", env: map[string]string{"ORGANIZATION_METRICS_ADDR": "127.0.0.1:9191"}, httpAddr: ":8080", want: "127.0.0.1:9191"},
		{name: "empty", env: map[string]string{"ORGANIZATION_METRICS_ADDR": " "}, httpAddr: ":8080", wantError: "ORGANIZATION_METRICS_ADDR must not be empty"},
		{name: "scheme", env: map[string]string{"ORGANIZATION_METRICS_ADDR": "http://127.0.0.1:9090"}, httpAddr: ":8080", wantError: "ORGANIZATION_METRICS_ADDR must be a valid host:port"},
		{name: "missing port", env: map[string]string{"ORGANIZATION_METRICS_ADDR": "127.0.0.1"}, httpAddr: ":8080", wantError: "ORGANIZATION_METRICS_ADDR must be a valid host:port"},
		{name: "same address", env: map[string]string{"ORGANIZATION_METRICS_ADDR": "0.0.0.0:8080"}, httpAddr: ":8080", wantError: "ORGANIZATION_METRICS_ADDR must differ from HTTP_ADDR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadMetricsAddr(mapLookup(tt.env), tt.httpAddr)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) { t.Fatalf("error = %v", err) }
				return
			}
			if err != nil || got != tt.want { t.Fatalf("got %q, err %v", got, err) }
		})
	}
}
