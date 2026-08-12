package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportCommandWritesHTMLFile(t *testing.T) {
	chdirToTestRepo(t)

	out := filepath.Join(t.TempDir(), "report.html")
	cmd := newRootCmd()
	cmd.SetArgs([]string{"report", "--out", out})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("report command: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("report file not written: %v", err)
	}
	if !strings.Contains(string(data), "<!doctype html>") {
		t.Errorf("report file missing doctype: %s", data)
	}
}

func TestReportCommandDefaultOutputPath(t *testing.T) {
	dir := chdirToTestRepo(t)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"report"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("report command: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "dun-report.html")); err != nil {
		t.Errorf("default report path not written: %v", err)
	}
}
