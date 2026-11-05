// Author: Navjyot Nishant
// Created: 2026-11-05
// Last updated: 2026-11-05
// Description: Adapts Claude Desktop's local agent mode (Cowork) sessions.

// Package claudedesktop reads the sessions Claude Desktop's agent mode,
// Cowork, runs on this machine.
//
// A local Cowork session runs Claude Code inside a VM, so its transcripts
// are Claude Code transcripts and are parsed by the claudecode package.
// Three things differ, and they are all this package handles:
//
//   - Where they live: under Claude Desktop's own data directory, one
//     directory per session, not under ~/.claude/projects.
//   - How paths are written: as the VM sees them, /sessions/<name>/mnt/
//     <mount>/…, where <mount> stands for a folder the user connected. The
//     session's metadata file maps each connected host folder to its mount.
//   - Which repository a session belongs to: the transcript's directory is
//     keyed by the session's own scratch folder, so the repository is found
//     through the connected folders instead.
//
// Cowork sessions that run in Anthropic's cloud leave no transcript on this
// machine and are out of reach here. See docs/adapters/agent-support.md.
package claudedesktop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// AgentName keeps Cowork's work apart from Claude Code's. The same model can
// sit behind both, but they are different tools used in different ways, and
// counting one as the other would misstate how each is used.
const AgentName = "claude-desktop"

// bashTool is Cowork's shell. It plays the part Bash plays in Claude Code,
// so its write targets are attributed the same way (WHO-238).
const bashTool = "mcp__workspace__bash"

// SessionsDir is the root holding every local agent-mode session:
// <root>/<account>/<org>/local_<id>.json beside local_<id>/.
//
// os.UserConfigDir is ~/Library/Application Support on macOS, where the
// layout was surveyed. Windows (%AppData%) is unverified; Linux has no
// Claude Desktop, so the directory is simply absent there.
func SessionsDir() string {
	path, _ := adapter.ResolveRoot(AgentName, builtinSessionsDir())
	return path
}

func builtinSessionsDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Claude", "local-agent-mode-sessions")
}

// SessionFiles returns the transcripts of every local session that had a
// folder connected which is, contains, or sits inside the repository at cwd.
//
// A connected folder that contains the repository counts: connecting a
// parent directory is how Cowork is pointed at several repositories at once.
func SessionFiles(cwd string) ([]string, error) {
	root := SessionsDir()
	if root == "" {
		return nil, nil
	}
	metas, err := filepath.Glob(filepath.Join(root, "*", "*", "local_*.json"))
	if err != nil {
		return nil, err
	}
	repo := linehash.Canonical(cwd)
	var out []string
	for _, meta := range metas {
		folders, err := selectedFolders(meta)
		if err != nil {
			// One unreadable session must not hide the others.
			continue
		}
		if !overlaps(folders, repo) {
			continue
		}
		dir := strings.TrimSuffix(meta, ".json")
		files, err := filepath.Glob(filepath.Join(dir, ".claude", "projects", "*", "*.jsonl"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			scopeTo(f, repo)
		}
		out = append(out, files...)
	}
	return out, nil
}

// scopes remembers which repository each transcript was listed for.
//
// One Cowork session can connect several folders, so one transcript can
// hold edits to several repositories. Claude Code never needs this, because
// its transcripts are already filed per repository. Here the scope is
// recorded when SessionFiles lists a transcript and applied when it is
// parsed, so an edit to another connected folder is not journaled against
// this repository. Every caller lists before it parses.
var (
	scopes   = map[string]string{}
	scopesMu sync.Mutex
)

func scopeTo(transcript, repo string) {
	scopesMu.Lock()
	scopes[transcript] = repo
	scopesMu.Unlock()
}

func scopeOf(transcript string) string {
	scopesMu.Lock()
	defer scopesMu.Unlock()
	return scopes[transcript]
}

// ParseSince reads one Cowork transcript, with every path rewritten from the
// VM's spelling to this machine's. Edits outside the connected folders, to
// the session's own scratch and upload folders, are dropped: they cannot be
// in any repository.
func ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	opts, ok := optionsFor(path)
	if !ok {
		return nil, nil
	}
	return claudecode.ParseWith(path, since, opts)
}

// ParseSessionActivity summarises the Cowork sessions in one transcript that
// edited a file in this repository, and no others.
//
// A Cowork session is often not coding at all: the same app drafts email,
// works a CRM, builds a deck. Connecting a folder that holds a repository
// does not make the rest of that session about the repository, so its
// message counts and token use are reported only when it wrote code here.
// Counts only, never content, as for Claude Code (NAV-55).
func ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	opts, ok := optionsFor(path)
	if !ok {
		return nil, nil
	}
	entries, err := claudecode.ParseWith(path, since, opts)
	if err != nil {
		return nil, err
	}
	coding := map[string]bool{}
	for _, e := range entries {
		if e.File != "" {
			coding[e.Session] = true
		}
	}
	if len(coding) == 0 {
		return nil, nil
	}
	all, err := claudecode.ParseSessionActivityWith(path, since, opts)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, s := range all {
		if coding[s.Session] {
			out = append(out, s)
		}
	}
	return out, nil
}

// optionsFor builds the parser options for the session a transcript belongs
// to. False when the transcript is not inside a session directory with
// metadata beside it: without the mount map, no path can be trusted.
func optionsFor(transcript string) (claudecode.Options, bool) {
	meta := metaFor(transcript)
	if meta == "" {
		return claudecode.Options{}, false
	}
	m, err := loadMounts(meta)
	if err != nil {
		return claudecode.Options{}, false
	}
	repo := scopeOf(transcript)
	return claudecode.Options{
		Agent:     AgentName,
		BashTool:  bashTool,
		EditsOnly: true,
		MapPath: func(p string) (string, bool) {
			host, ok := m.hostPath(p)
			if !ok {
				return "", false
			}
			// A transcript parsed without being listed first has no scope,
			// and every connected folder is kept.
			if repo != "" && !within(host, repo) {
				return "", false
			}
			return host, true
		},
	}, true
}

// metaFor walks up from a transcript to its session directory, local_<id>,
// and returns the metadata file beside it.
func metaFor(transcript string) string {
	dir := filepath.Dir(transcript)
	for i := 0; i < 6; i++ {
		if strings.HasPrefix(filepath.Base(dir), "local_") {
			meta := dir + ".json"
			if _, err := os.Stat(meta); err == nil {
				return meta
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// vmPath is how a connected folder appears inside the VM. The session name
// varies and carries nothing; the mount name is what identifies the folder.
var vmPath = regexp.MustCompile(`^/sessions/[^/]+/mnt/([^/]+)(?:/(.*))?$`)

// mounts maps mount names to host folders for one session.
type mounts struct {
	byMount map[string]string
	folders []string
}

// hostPath rewrites a transcript path to this machine's. A host path inside
// a connected folder is kept as written. Anything else, including the
// session's outputs and uploads folders and any path in the VM's own
// filesystem, is not on this machine and is dropped.
func (m mounts) hostPath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if sm := vmPath.FindStringSubmatch(filepath.ToSlash(p)); sm != nil {
		host, ok := m.byMount[sm[1]]
		if !ok {
			return "", false
		}
		if sm[2] == "" {
			return host, true
		}
		return filepath.Join(host, filepath.FromSlash(sm[2])), true
	}
	if filepath.IsAbs(p) {
		clean := linehash.Canonical(p)
		for _, f := range m.folders {
			if within(clean, f) {
				return clean, true
			}
		}
	}
	return "", false
}

// sessionMeta is the subset of the metadata file this package reads. The
// file also lists MCP servers, plugins and approved file paths; none of that
// is declared here, so none of it is decoded.
type sessionMeta struct {
	UserSelectedFolders []string          `json:"userSelectedFolders"`
	FolderMountNames    map[string]string `json:"folderMountNames"`
}

var (
	mountCache   = map[string]cachedMounts{}
	mountCacheMu sync.Mutex
)

type cachedMounts struct {
	modTime time.Time
	size    int64
	m       mounts
}

// loadMounts reads a session's mount map, cached by modification time so the
// daemon does not re-read a half-megabyte file on every pass.
//
// Older sessions carry no folderMountNames; their folders were mounted under
// their own base name, which is used as the fallback. Two folders sharing a
// base name are ambiguous under that rule, so neither is mapped: a path that
// might be in either repository must not be credited to one of them.
func loadMounts(meta string) (mounts, error) {
	info, err := os.Stat(meta)
	if err != nil {
		return mounts{}, err
	}
	mountCacheMu.Lock()
	c, ok := mountCache[meta]
	mountCacheMu.Unlock()
	if ok && c.modTime.Equal(info.ModTime()) && c.size == info.Size() {
		return c.m, nil
	}

	raw, err := os.ReadFile(meta)
	if err != nil {
		return mounts{}, err
	}
	var sm sessionMeta
	if err := json.Unmarshal(raw, &sm); err != nil {
		return mounts{}, err
	}
	// Folders are stored canonical, resolved once here while they exist,
	// so a mapped path agrees with the repository path the hook is given
	// even when the file itself does not exist yet: /var against
	// /private/var on macOS, where resolving the file would fail and leave
	// the two spellings apart.
	m := mounts{byMount: map[string]string{}}
	seen := map[string]int{}
	for _, f := range sm.UserSelectedFolders {
		if f == "" {
			continue
		}
		c := linehash.Canonical(f)
		m.folders = append(m.folders, c)
		if name, ok := sm.FolderMountNames[f]; ok && name != "" {
			m.byMount[name] = c
			continue
		}
		base := filepath.Base(f)
		seen[base]++
		m.byMount[base] = c
	}
	for base, n := range seen {
		if n > 1 {
			delete(m.byMount, base)
		}
	}
	for host, name := range sm.FolderMountNames {
		if _, ok := m.byMount[name]; !ok && name != "" && host != "" {
			m.byMount[name] = linehash.Canonical(host)
			m.folders = append(m.folders, linehash.Canonical(host))
		}
	}

	mountCacheMu.Lock()
	mountCache[meta] = cachedMounts{modTime: info.ModTime(), size: info.Size(), m: m}
	mountCacheMu.Unlock()
	return m, nil
}

// headBytes bounds how much of a metadata file SessionFiles reads.
// userSelectedFolders starts within the first 400 bytes of all 242 files
// surveyed, while the files run to 1.8 MB; reading them whole on every
// commit would cost 140 MB on the survey machine, and this runs in the
// commit hook.
const headBytes = 8 * 1024

// selectedFolders reads only the connected folders from the head of a
// metadata file. A file whose list does not start in the head is read whole
// rather than skipped, so an unusual layout costs time, never a session.
func selectedFolders(meta string) ([]string, error) {
	f, err := os.Open(meta)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, headBytes)
	n, err := io.ReadFull(bufio.NewReader(f), head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	head = head[:n]
	if folders, ok := decodeFolders(head); ok {
		return folders, nil
	}
	raw, err := os.ReadFile(meta)
	if err != nil {
		return nil, err
	}
	var sm sessionMeta
	if err := json.Unmarshal(raw, &sm); err != nil {
		return nil, err
	}
	folders := append([]string(nil), sm.UserSelectedFolders...)
	for host := range sm.FolderMountNames {
		folders = append(folders, host)
	}
	return folders, nil
}

var foldersKey = []byte(`"userSelectedFolders"`)

// decodeFolders finds the folder list in a prefix of the file and decodes
// just that value.
func decodeFolders(head []byte) ([]string, bool) {
	i := bytes.Index(head, foldersKey)
	if i < 0 {
		return nil, false
	}
	rest := head[i+len(foldersKey):]
	j := bytes.IndexByte(rest, ':')
	if j < 0 {
		return nil, false
	}
	var folders []string
	if err := json.NewDecoder(bytes.NewReader(rest[j+1:])).Decode(&folders); err != nil {
		return nil, false
	}
	return folders, true
}

// overlaps reports whether any connected folder is the repository, contains
// it, or lies inside it.
func overlaps(folders []string, repo string) bool {
	for _, f := range folders {
		if f == "" {
			continue
		}
		c := linehash.Canonical(f)
		if within(repo, c) || within(c, repo) {
			return true
		}
	}
	return false
}

// within reports whether path is dir or lies under it. Compared on whole
// path elements, so /repo-old is not inside /repo.
func within(path, dir string) bool {
	path, dir = filepath.ToSlash(filepath.Clean(path)), filepath.ToSlash(filepath.Clean(dir))
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}
