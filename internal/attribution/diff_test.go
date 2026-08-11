package attribution

import "testing"

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

func TestHashAddedHunks(t *testing.T) {
	hashes, err := hashAddedHunks(sampleDiff, "/repo")
	if err != nil {
		t.Fatalf("hashAddedHunks: %v", err)
	}
	if len(hashes) != 2 {
		t.Fatalf("want 2 hunk hashes (one per file's added block), got %d", len(hashes))
	}
}

func TestHashAddedHunksMatchesSameTextSameFile(t *testing.T) {
	// The same file gaining the same text across two independently-produced
	// diffs (e.g. the journal's view vs a later staged diff) must hash equal —
	// this is the actual mechanism method=intersected depends on.
	diffA := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1,1 @@\n+hello world\n"
	diffB := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1,1 @@\n+hello world\n"

	ha, err := hashAddedHunks(diffA, "/repo")
	if err != nil {
		t.Fatalf("hashAddedHunks A: %v", err)
	}
	hb, err := hashAddedHunks(diffB, "/repo")
	if err != nil {
		t.Fatalf("hashAddedHunks B: %v", err)
	}

	for h := range ha {
		if !hb[h] {
			t.Errorf("hash %s from diffA not found in diffB despite identical file+text", h)
		}
	}
}

func TestHashAddedHunksSameTextDifferentFileDoesNotMatch(t *testing.T) {
	// Two different files independently gaining the same small fragment (e.g.
	// a common one-line import) must NOT hash equal — that would be a false
	// intersected match. This is the tightening this test guards.
	diffX := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1,1 @@\n+import foo\n"
	diffY := "diff --git a/y b/y\n--- a/y\n+++ b/y\n@@ -0,0 +1,1 @@\n+import foo\n"

	hx, err := hashAddedHunks(diffX, "/repo")
	if err != nil {
		t.Fatalf("hashAddedHunks X: %v", err)
	}
	hy, err := hashAddedHunks(diffY, "/repo")
	if err != nil {
		t.Fatalf("hashAddedHunks Y: %v", err)
	}

	for h := range hx {
		if hy[h] {
			t.Errorf("hash %s matched across different files for identical text — false intersected match", h)
		}
	}
}

func TestHashAddedHunksEmptyDiff(t *testing.T) {
	hashes, err := hashAddedHunks("", "/repo")
	if err != nil {
		t.Fatalf("hashAddedHunks: %v", err)
	}
	if len(hashes) != 0 {
		t.Errorf("want 0 hashes for empty diff, got %d", len(hashes))
	}
}
