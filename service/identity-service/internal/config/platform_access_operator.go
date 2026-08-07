package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

const (
	defaultPlatformAccessDatabaseConnectTimeout = 5 * time.Second
	defaultPlatformAccessCommandTimeout         = 5 * time.Second
	maximumPlatformAccessCommandTimeout         = 30 * time.Second
)

type PlatformAccessOperatorConfig struct {
	DatabaseURL            string
	DatabaseConnectTimeout time.Duration
	CommandTimeout         time.Duration
	LogLevel               slog.Level
}

func LoadPlatformAccessOperator() (PlatformAccessOperatorConfig, error) {
	return loadPlatformAccessOperator(os.LookupEnv)
}

func loadPlatformAccessOperator(lookup lookupEnvFunc) (PlatformAccessOperatorConfig, error) {
	databaseURL, err := requiredValue(lookup, "PLATFORM_ACCESS_DATABASE_URL")
	if err != nil {
		return PlatformAccessOperatorConfig{}, err
	}
	connectTimeout, err := durationValue(
		lookup,
		"PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT",
		defaultPlatformAccessDatabaseConnectTimeout,
	)
	if err != nil {
		return PlatformAccessOperatorConfig{}, err
	}
	commandTimeout, err := durationValue(
		lookup,
		"PLATFORM_ACCESS_COMMAND_TIMEOUT",
		defaultPlatformAccessCommandTimeout,
	)
	if err != nil {
		return PlatformAccessOperatorConfig{}, err
	}
	if commandTimeout > maximumPlatformAccessCommandTimeout {
		return PlatformAccessOperatorConfig{}, fmt.Errorf(
			"PLATFORM_ACCESS_COMMAND_TIMEOUT must be less than or equal to %s",
			maximumPlatformAccessCommandTimeout,
		)
	}
	logLevel, err := logLevelValue(lookup, "LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return PlatformAccessOperatorConfig{}, err
	}
	return PlatformAccessOperatorConfig{
		DatabaseURL:            databaseURL,
		DatabaseConnectTimeout: connectTimeout,
		CommandTimeout:         commandTimeout,
		LogLevel:               logLevel,
	}, nil
}
