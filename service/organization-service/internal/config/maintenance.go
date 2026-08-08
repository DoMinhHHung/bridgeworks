package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

const (
	defaultMaintenanceDatabaseConnectTimeout       = 5 * time.Second
	defaultMaintenanceCommandTimeout               = 30 * time.Second
	maximumMaintenanceCommandTimeout               = 2 * time.Minute
	defaultInvitationRetention                     = 30 * 24 * time.Hour
	minimumInvitationRetention                     = 24 * time.Hour
	defaultRemovalReconcileAfter                   = 15 * time.Minute
	minimumRemovalReconcileAfter                   = time.Minute
	defaultMaintenanceBatchSize              int32 = 100
	maximumMaintenanceBatchSize              int32 = 500
)

type MaintenanceConfig struct {
	DatabaseURL            string
	DatabaseConnectTimeout time.Duration
	CommandTimeout         time.Duration
	InvitationRetention    time.Duration
	RemovalReconcileAfter  time.Duration
	BatchSize              int32
	LogLevel               slog.Level
}

type MaintenanceProviderConfig struct {
	SecretKey string
	APIURL    string
	Timeout   time.Duration
}

func LoadMaintenance() (MaintenanceConfig, error) {
	return loadMaintenance(os.LookupEnv)
}

func LoadMaintenanceProvider() (MaintenanceProviderConfig, error) {
	return loadMaintenanceProvider(os.LookupEnv)
}

func loadMaintenance(lookup lookupEnvFunc) (MaintenanceConfig, error) {
	databaseURL, err := requiredValue(lookup, "ORGANIZATION_MAINTENANCE_DATABASE_URL")
	if err != nil {
		return MaintenanceConfig{}, err
	}
	connectTimeout, err := durationValue(lookup, "ORGANIZATION_MAINTENANCE_DATABASE_CONNECT_TIMEOUT", defaultMaintenanceDatabaseConnectTimeout)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	commandTimeout, err := durationValue(lookup, "ORGANIZATION_MAINTENANCE_COMMAND_TIMEOUT", defaultMaintenanceCommandTimeout)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	if commandTimeout > maximumMaintenanceCommandTimeout {
		return MaintenanceConfig{}, fmt.Errorf("ORGANIZATION_MAINTENANCE_COMMAND_TIMEOUT must be less than or equal to %s", maximumMaintenanceCommandTimeout)
	}
	retention, err := durationValue(lookup, "ORGANIZATION_INVITATION_INTENT_RETENTION", defaultInvitationRetention)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	if retention < minimumInvitationRetention {
		return MaintenanceConfig{}, fmt.Errorf("ORGANIZATION_INVITATION_INTENT_RETENTION must be at least %s", minimumInvitationRetention)
	}
	reconcileAfter, err := durationValue(lookup, "ORGANIZATION_REMOVAL_RECONCILE_AFTER", defaultRemovalReconcileAfter)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	if reconcileAfter < minimumRemovalReconcileAfter {
		return MaintenanceConfig{}, fmt.Errorf("ORGANIZATION_REMOVAL_RECONCILE_AFTER must be at least %s", minimumRemovalReconcileAfter)
	}
	batchSize, err := int32Value(lookup, "ORGANIZATION_MAINTENANCE_BATCH_SIZE", defaultMaintenanceBatchSize)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	if batchSize <= 0 || batchSize > maximumMaintenanceBatchSize {
		return MaintenanceConfig{}, fmt.Errorf("ORGANIZATION_MAINTENANCE_BATCH_SIZE must be between 1 and %d", maximumMaintenanceBatchSize)
	}
	logLevel, err := logLevelValue(lookup, "LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	return MaintenanceConfig{
		DatabaseURL:            databaseURL,
		DatabaseConnectTimeout: connectTimeout,
		CommandTimeout:         commandTimeout,
		InvitationRetention:    retention,
		RemovalReconcileAfter:  reconcileAfter,
		BatchSize:              batchSize,
		LogLevel:               logLevel,
	}, nil
}

func loadMaintenanceProvider(lookup lookupEnvFunc) (MaintenanceProviderConfig, error) {
	secretKey, err := requiredValue(lookup, "CLERK_SECRET_KEY")
	if err != nil {
		return MaintenanceProviderConfig{}, err
	}
	apiURL, err := serviceURLValue(lookup, "CLERK_BACKEND_API_URL", "https://api.clerk.com")
	if err != nil {
		return MaintenanceProviderConfig{}, err
	}
	timeout, err := durationValue(lookup, "CLERK_BACKEND_API_TIMEOUT", 3*time.Second)
	if err != nil {
		return MaintenanceProviderConfig{}, err
	}
	if timeout > 5*time.Second {
		return MaintenanceProviderConfig{}, fmt.Errorf("CLERK_BACKEND_API_TIMEOUT must be less than or equal to 5s")
	}
	return MaintenanceProviderConfig{SecretKey: secretKey, APIURL: apiURL, Timeout: timeout}, nil
}
