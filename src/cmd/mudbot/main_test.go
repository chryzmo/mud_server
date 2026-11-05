package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIRefusesUnsafeTargetsAndSeedWithoutReset(t *testing.T) {
	t.Setenv("MUD_BOT_PASSWORD", "fixture-only-password")
	for _, args := range [][]string{
		{"-url", "wss://example.com/ws"},
		{"-count", "0"},
		{"seed"},
		{"seed", "-reset"}, // no database URL: reject before opening a connection
	} {
		t.Setenv("MUD_BOT_DATABASE_URL", "")
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), args, &stdout, &stderr)
		if err == nil {
			t.Fatalf("accepted unsafe args %q", args)
		}
		output := stdout.String() + stderr.String() + err.Error()
		if strings.Contains(output, "fixture-only-password") {
			t.Fatal("password leaked in CLI output")
		}
	}
}
