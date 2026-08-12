package attribution

import (
	"testing"

	"github.com/navjyotnishant/whodunit/internal/linehash"
)

const sampleDiff = `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -1,2 +1,4 @@
 package main
+
+func main() {
+}
diff --git a/other.go b/other.go
index 111..222 100644
--- a/other.go
+++ b/other.go
@@ -0,0 +1,2 @@
+package other
+var x = 1
`

func TestStagedLineHashesSkipsInsubstantialLines(t *testing.T) {
	// The sample adds: "", "func main() {", "}", "package other", "var x = 1".
	// The blank line and the lone brace carry no attribution evidence.
	hashes := stagedLineHashes(sampleDiff, "/repo")
	if len(hashes) != 3 {
		t.Fatalf("want 3 substantive added lines, got %d", len(hashes))
	}
}

func TestStagedLineHashesScopesToFile(t *testing.T) {
	// The same line added to two different files must hash differently, or
	// a common import would manufacture attribution across unrelated files.
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1,1 @@\n+import foo\n" +
		"diff --git a/y b/y\n--- a/y\n+++ b/y\n@@ -0,0 +1,1 @@\n+import foo\n"

	hashes := stagedLineHashes(diff, "/repo")
	if len(hashes) != 2 {
		t.Fatalf("want 2 hashes, got %d", len(hashes))
	}
	if hashes[0] == hashes[1] {
		t.Error("the same line in two files produced the same hash")
	}
}

func TestStagedLineHashesMatchesTheAgentSideEncoding(t *testing.T) {
	// The staged side and the journal side must agree on the unit, or
	// nothing ever matches — the failure NAV-52 was opened for.
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -0,0 +1,1 @@\n+\tdoWork()\n"

	staged := stagedLineHashes(diff, "/repo")
	if len(staged) != 1 {
		t.Fatalf("want 1 hash, got %d", len(staged))
	}

	// What the adapter would record for the same line.
	agent := linehash.OfText("/repo/main.go", "\tdoWork()")
	if len(agent) != 1 {
		t.Fatalf("agent side produced %d hashes, want 1", len(agent))
	}

	if staged[0] != agent[0] {
		t.Error("staged and agent sides hashed the same line differently")
	}
}

func TestStagedLineHashesIgnoresDiffMetadata(t *testing.T) {
	// "+++ b/file" starts with '+' but is a header, not content.
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -0,0 +1,1 @@\n+realLine()\n"
	hashes := stagedLineHashes(diff, "/repo")
	if len(hashes) != 1 {
		t.Fatalf("want 1 hash for the one real added line, got %d", len(hashes))
	}
	if hashes[0] != linehash.Of("/repo/x.go", "realLine()") {
		t.Error("hashed something other than the added line")
	}
}

func TestStagedLineHashesEmptyDiff(t *testing.T) {
	if got := stagedLineHashes("", "/repo"); len(got) != 0 {
		t.Errorf("want 0 hashes for an empty diff, got %d", len(got))
	}
}

func TestStagedLineHashesSkipsDeletions(t *testing.T) {
	// Only added lines are evidence of what the commit contains.
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,1 @@\n-removedLine()\n+addedLine()\n"
	hashes := stagedLineHashes(diff, "/repo")
	if len(hashes) != 1 {
		t.Fatalf("want 1 hash (the added line only), got %d", len(hashes))
	}
	if hashes[0] != linehash.Of("/repo/x.go", "addedLine()") {
		t.Error("hashed the removed line instead of the added one")
	}
}
