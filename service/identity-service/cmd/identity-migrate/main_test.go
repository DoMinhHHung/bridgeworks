package main

import "testing"

func TestParseCommand(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"up", "status", "version"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			got, err := parseCommand([]string{command})
			if err != nil {
				t.Fatalf("parse command: %v", err)
			}
			if got != command {
				t.Fatalf("command = %q", got)
			}
		})
	}
}

func TestParseCommandRejectsUnsupportedCommands(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		nil,
		{},
		{"down"},
		{"reset"},
		{"up", "extra"},
	}

	for _, args := range tests {
		args := args
		t.Run(testName(args), func(t *testing.T) {
			t.Parallel()

			if _, err := parseCommand(args); err == nil {
				t.Fatalf("expected error for args %v", args)
			}
		})
	}
}

func testName(args []string) string {
	if len(args) == 0 {
		return "missing"
	}
	return args[0]
}
