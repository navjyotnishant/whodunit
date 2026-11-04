package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/config"
)

// sync.commit_lines is off unless someone turns it on, and off means the
// payload carries nothing new.
func TestSyncPayloadCommitLinesOnlyWhenEnabled(t *testing.T) {
	chdirToTestRepo(t)

	p, err := buildPayload(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.CommitLines) != 0 {
		t.Fatalf("default payload carries %d commit lines", len(p.CommitLines))
	}
	var out bytes.Buffer
	describePayload(&out, p)
	if strings.Contains(out.String(), "commit lines") {
		t.Fatalf("default dry run mentions commit lines:\n%s", out.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := scalarByKey("sync.commit_lines")
	if err := s.Set(&cfg, "on"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	p, err = buildPayload(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.CommitLines) != 1 || p.CommitLines[0].FilePath != ".seed" {
		t.Fatalf("enabled payload commit lines = %+v, want the seed commit's one line", p.CommitLines)
	}
	if p.CommitLines[0].CommitSHA == "" || p.CommitLines[0].RepoID != p.Repo.RepoID {
		t.Fatalf("row not tied to its commit and repo: %+v", p.CommitLines[0])
	}
	out.Reset()
	describePayload(&out, p)
	if !strings.Contains(out.String(), "with file paths") {
		t.Fatalf("dry run does not disclose file paths:\n%s", out.String())
	}
}

func TestCommitLinesSettingRoundTrip(t *testing.T) {
	s, ok := scalarByKey("sync.commit_lines")
	if !ok {
		t.Fatal("sync.commit_lines is not a setting")
	}
	var c config.Config
	if s.Get(c) != "off" {
		t.Fatal("default is not off")
	}
	if err := s.Set(&c, "off"); err != nil || c.Sync != nil {
		t.Fatalf("turning off an unset value created sync config: %+v, %v", c.Sync, err)
	}
	if err := s.Set(&c, "maybe"); err == nil {
		t.Fatal("accepted maybe")
	}
	if err := s.Set(&c, "on"); err != nil || s.Get(c) != "on" {
		t.Fatalf("on: %v, %s", err, s.Get(c))
	}
	if c.Sync.Configured() {
		t.Fatal("enabling commit lines must not count as a configured sync target")
	}
}
