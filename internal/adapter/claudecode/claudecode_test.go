package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

const fixture = `{"type":"user","timestamp":"2026-08-11T22:49:00.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"text","text":"do the thing"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:01.653Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"text","text":"working on it"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:09.451Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"c1","name":"Write","input":{"file_path":"/repo/main.go","content":"package main\nfunc main() {}\n"}}]}}
{"type":"user","timestamp":"2026-08-11T22:50:09.500Z","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:20.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"c2","name":"Edit","input":{"file_path":"/repo/main.go","old_string":"func main() {}","new_string":"func main() {\n\tprintln(\"hi\")\n}"}}]}}
{"type":"user","timestamp":"2026-08-11T22:50:20.500Z","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"c2","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:30.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]}}
`

// edits keeps only file-edit entries. ParseSince also emits a bare
// tool_call entry per non-editing call, which these tests are not about.
func edits(entries []journal.Entry) []journal.Entry {
	var out []journal.Entry
	for _, e := range entries {
		if e.Event == "tool_use" {
			out = append(out, e)
		}
	}
	return out
}

// parseEdits is ParseSince narrowed to file edits.
func parseEdits(path string, since time.Time) ([]journal.Entry, error) {
	all, err := ParseSince(path, since)
	if err != nil {
		return nil, err
	}
	return edits(all), nil
}

func TestParseSince(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	entries, err := parseEdits(path, time.Time{})
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 tool_use entries (Bash and text excluded), got %d: %+v", len(entries), entries)
	}

	write := entries[0]
	if write.Tool != "Write" || write.File != "/repo/main.go" || write.LinesAdded != 3 {
		t.Errorf("Write entry wrong: %+v", write)
	}
	if write.Agent != AgentName || write.AgentVersion != "2.1.227" || write.Session != "sess-1" {
		t.Errorf("Write entry metadata wrong: %+v", write)
	}
	if write.HunkHash == "" {
		t.Error("Write entry missing hunk hash")
	}

	edit := entries[1]
	if edit.Tool != "Edit" || edit.LinesRemoved != 1 || edit.LinesAdded != 3 {
		t.Errorf("Edit entry wrong: %+v", edit)
	}
	if edit.HunkHash == write.HunkHash {
		t.Error("Edit and Write hunk hashes must differ")
	}
}

func TestParseSinceFiltersByTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cutoff := time.Date(2026, 8, 11, 22, 50, 15, 0, time.UTC)
	entries, err := parseEdits(path, cutoff)
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if len(entries) != 1 || entries[0].Tool != "Edit" {
		t.Fatalf("want only the post-cutoff Edit entry, got %+v", entries)
	}
}

func TestParseSinceSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	content := "not json at all\n" + fixture
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	entries, err := parseEdits(path, time.Time{})
	if err != nil {
		t.Fatalf("ParseSince should not fail on malformed line: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries despite leading garbage line, got %d", len(entries))
	}
}

func TestSlugForCwd(t *testing.T) {
	got := SlugForCwd("/Users/nav/repo")
	want := "-Users-nav-repo"
	if got != want {
		t.Errorf("SlugForCwd = %q, want %q", got, want)
	}
}

func TestSlugForCwdIsALegalDirectoryName(t *testing.T) {
	// A slug is used as a directory name, so it has to be creatable. The
	// Windows path here produced "C:-Users-runneradmin-..." when only the
	// separator was replaced, and every mkdir failed with "The directory
	// name is invalid" — which surfaced as no transcripts found and every
	// commit undetermined, rather than as an error anyone could see.
	//
	// Asserted on every platform, not just Windows: the encoding has to be
	// the same everywhere, because a path with forward slashes reaches this
	// function on Windows too.
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{"unix", "/Users/nav/repo", "-Users-nav-repo"},
		{"windows backslash", `C:\Users\nav\repo`, "C--Users-nav-repo"},
		{"windows forward slash", "C:/Users/nav/repo", "C--Users-nav-repo"},
		{"unc", `\\server\share\repo`, "--server-share-repo"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SlugForCwd(c.cwd)
			if got != c.want {
				t.Errorf("SlugForCwd(%q) = %q, want %q", c.cwd, got, c.want)
			}
			if strings.ContainsAny(got, `:\/`) {
				t.Errorf("SlugForCwd(%q) = %q, which cannot be a directory "+
					"name on Windows", c.cwd, got)
			}
			// The real proof: the operating system accepts it.
			if err := os.MkdirAll(filepath.Join(t.TempDir(), got), 0o700); err != nil {
				t.Errorf("cannot create a directory named %q: %v", got, err)
			}
		})
	}
}

func TestSlugForCwdReplacesEveryNonAlphanumeric(t *testing.T) {
	// The contract is Claude Code's own rule — /[^a-zA-Z0-9]/g -> '-' — not a
	// list of separators we happened to think of. A narrower rule is a silent
	// failure: the adapter looks in a directory that does not exist, finds no
	// transcript, and attribution reports that no agent touched the repository.
	//
	// The dotted case is not hypothetical. Every agent sandbox under ~/.orion
	// hit it, and on one machine 77 of 91 git repositories were invisible to
	// the adapter because of it.
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{"dotted parent", "/Users/nav/.orion/projects/x/repo", "-Users-nav--orion-projects-x-repo"},
		{"underscore", "/Users/nav/my_repo", "-Users-nav-my-repo"},
		{"dot in name", "/Users/nav/repo.git", "-Users-nav-repo-git"},
		{"at sign", "/Users/nav/repo@2", "-Users-nav-repo-2"},
		{"space", "/Users/nav/my repo", "-Users-nav-my-repo"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SlugForCwd(c.cwd); got != c.want {
				t.Errorf("SlugForCwd(%q) = %q, want %q", c.cwd, got, c.want)
			}
		})
	}
}

func TestSlugForCwdTruncatesLongPathsLikeTheClient(t *testing.T) {
	// A slug over 200 characters is truncated and given a base36 hash of the
	// FULL path — hashing the slug would collide precisely where the
	// truncation needs to disambiguate, because the encoding is lossy.
	//
	// These two cases are not constructed from this implementation. Each is
	// a directory name Claude Code itself wrote: a deep path was created,
	// the client was run in it, and the name it produced was recorded here.
	// Asserting our own hash against our own hash would prove nothing, which
	// is why WHO-237 stayed open until the client could be observed.
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{
			// 241-character slug, plain segments.
			name: "plain segments",
			cwd: "/private/tmp/whotrunc/segment00/segment01/segment02/segment03/" +
				"segment04/segment05/segment06/segment07/segment08/segment09/" +
				"segment10/segment11/segment12/segment13/segment14/segment15/" +
				"segment16/segment17/segment18/segment19/segment20/segment21",
			want: "-private-tmp-whotrunc-segment00-segment01-segment02-segment03-" +
				"segment04-segment05-segment06-segment07-segment08-segment09-" +
				"segment10-segment11-segment12-segment13-segment14-segment15-" +
				"segment16-segment1-3wu2bq",
		},
		{
			// 202-character slug, segments carrying underscores and dots —
			// so the hash is over characters the slug no longer contains.
			name: "punctuated segments",
			cwd: "/private/tmp/whotrunc2/dir_00.x/dir_01.x/dir_02.x/dir_03.x/" +
				"dir_04.x/dir_05.x/dir_06.x/dir_07.x/dir_08.x/dir_09.x/" +
				"dir_10.x/dir_11.x/dir_12.x/dir_13.x/dir_14.x/dir_15.x/" +
				"dir_16.x/dir_17.x/dir_18.x/dir_19.x",
			want: "-private-tmp-whotrunc2-dir-00-x-dir-01-x-dir-02-x-dir-03-x-" +
				"dir-04-x-dir-05-x-dir-06-x-dir-07-x-dir-08-x-dir-09-x-" +
				"dir-10-x-dir-11-x-dir-12-x-dir-13-x-dir-14-x-dir-15-x-" +
				"dir-16-x-dir-17-x-dir-18-x-dir-19-9c8b8b",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SlugForCwd(c.cwd)
			if got != c.want {
				t.Errorf("SlugForCwd(%q)\n = %q\nwant %q", c.cwd, got, c.want)
			}
			if len(got) != maxSlugLen+1+len(got[maxSlugLen+1:]) {
				t.Errorf("truncated slug is malformed: %q", got)
			}
		})
	}
}

func TestSlugForCwdLeavesShortPathsAlone(t *testing.T) {
	// The truncation must not touch the ordinary case, which is every real
	// repository path anyone has. A regression here would break attribution
	// everywhere rather than only on deep paths.
	cwd := "/Users/nav/repo"
	if got := SlugForCwd(cwd); got != "-Users-nav-repo" {
		t.Errorf("SlugForCwd(%q) = %q, want it unchanged", cwd, got)
	}
	if got := SlugForCwd(cwd); len(got) > maxSlugLen {
		t.Errorf("a short path produced a %d-character slug", len(got))
	}
}

func TestSlugForCwdIsLossyAndThatIsUpstreamsChoice(t *testing.T) {
	// Distinct paths CAN collide, and this test exists so that is a recorded
	// property rather than a surprise. An earlier version dropped ':' instead
	// of mapping it, specifically to keep C:\repo apart from C-\repo.
	//
	// That was reverted: the goal is to find the directory Claude Code
	// actually wrote, and it maps ':' like everything else. A slug that is
	// distinct but wrong finds nothing at all, which is worse than one that
	// is right for every real path and theoretically ambiguous. Upstream has
	// the same collision (anthropics/claude-code#35162).
	if SlugForCwd(`C:\repo`) != SlugForCwd(`C-\repo`) {
		t.Error("expected these to collide, matching Claude Code's own encoding")
	}
}
