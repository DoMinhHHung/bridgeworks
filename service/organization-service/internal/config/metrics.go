package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const defaultMetricsAddr = ":9090"

func LoadMetricsAddr(httpAddr string) (string, error) {
	return loadMetricsAddr(os.LookupEnv, httpAddr)
}

func loadMetricsAddr(lookup lookupEnvFunc, httpAddr string) (string, error) {
	metricsAddr, err := nonEmptyValue(lookup, "ORGANIZATION_METRICS_ADDR", defaultMetricsAddr)
	if err != nil {
		return "", err
	}
	metricsEndpoint, err := listenEndpoint("ORGANIZATION_METRICS_ADDR", metricsAddr)
	if err != nil {
		return "", err
	}
	httpEndpoint, err := listenEndpoint("HTTP_ADDR", httpAddr)
	if err != nil {
		return "", err
	}
	if metricsEndpoint == httpEndpoint {
		return "", fmt.Errorf("ORGANIZATION_METRICS_ADDR must differ from HTTP_ADDR")
	}
	return metricsAddr, nil
}

func listenEndpoint(key, value string) (string, error) {
	if strings.Contains(value, "://") {
		return "", fmt.Errorf("%s must be a valid host:port", key)
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("%s must be a valid host:port", key)
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return "", fmt.Errorf("%s must be a valid host:port", key)
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "*"
	}
	return net.JoinHostPort(host, strconv.FormatUint(portNumber, 10)), nil
}
