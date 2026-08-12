package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestDaemonRunCommandStopsOnContextCancel(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"daemon", "run"})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("daemon run = %v, want nil after context times out", err)
	}
	if !strings.Contains(buf.String(), "watching") {
		t.Errorf("output = %q, want a startup message", buf.String())
	}
}
