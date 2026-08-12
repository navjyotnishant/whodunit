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

	buf := &strings.Builder{}
	cmd.SetOut(buf)
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

	absOut, _ := filepath.Abs(out)
	if !strings.Contains(buf.String(), "file://"+absOut) {
		t.Errorf("output missing pastable file:// URL: %s", buf.String())
	}
}

func TestReportCommandDefaultOutputPath(t *testing.T) {
	chdirToTestRepo(t)

	// A stray dun-report.html left in the repo tree from a prior run of
	// this test (or a real `dun report`) shouldn't make this test pass
	// vacuously — start from a clean slate for the default path.
	defaultOut := filepath.Join(os.TempDir(), "dun-report.html")
	os.Remove(defaultOut)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"report"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("report command: %v", err)
	}

	if _, err := os.Stat(defaultOut); err != nil {
		t.Errorf("default report path (%s) not written: %v", defaultOut, err)
	}
	os.Remove(defaultOut)
}
