package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type orderedShutdownServer struct {
	name        string
	order       *[]string
	shutdownErr error
}

func (s *orderedShutdownServer) Shutdown(context.Context) error {
	*s.order = append(*s.order, s.name+":shutdown")
	return s.shutdownErr
}

func (s *orderedShutdownServer) Close() error {
	*s.order = append(*s.order, s.name+":close")
	return nil
}

func TestShutdownRuntimeStopsServersBeforeCacheAndDatabaseClose(t *testing.T) {
	order := make([]string, 0, 4)
	metricsServer := &orderedShutdownServer{name: "metrics", order: &order}
	applicationServer := &orderedShutdownServer{name: "application", order: &order}

	err := shutdownRuntime(
		context.Background(),
		metricsServer,
		applicationServer,
		func() { order = append(order, "cache:close") },
		func() { order = append(order, "database:close") },
	)
	if err != nil {
		t.Fatalf("shutdownRuntime() error = %v", err)
	}
	want := []string{"metrics:shutdown", "application:shutdown", "cache:close", "database:close"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("shutdown order = %#v, want %#v", order, want)
	}
}

func TestShutdownRuntimeContinuesCleanupAfterMetricsShutdownFailure(t *testing.T) {
	order := make([]string, 0, 5)
	metricsServer := &orderedShutdownServer{name: "metrics", order: &order, shutdownErr: errors.New("bind listener stuck")}
	applicationServer := &orderedShutdownServer{name: "application", order: &order}

	err := shutdownRuntime(
		context.Background(),
		metricsServer,
		applicationServer,
		func() { order = append(order, "cache:close") },
		func() { order = append(order, "database:close") },
	)
	if err == nil {
		t.Fatal("shutdownRuntime() error = nil")
	}
	want := []string{"metrics:shutdown", "metrics:close", "application:shutdown", "cache:close", "database:close"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("shutdown order = %#v, want %#v", order, want)
	}
}
