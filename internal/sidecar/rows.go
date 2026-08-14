package sidecar

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/report"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// RepoRow is one row of whodunit_repos.
type RepoRow struct {
	RepoID      string
	Contributor string
	SpecVersion string
	SyncedAt    time.Time
}

// CommitRow is one row of whodunit_commits — the dashboard grain.
type CommitRow struct {
	CommitSHA     string
	RepoID        string
	CommittedAt   time.Time
	Status        string
	Method        string
	Agent         string
	AgentVersion  string
	Purpose       string
	Ratio         *float64 // nil when no line-level evidence existed
	LinesAdded    int
	LinesRemoved  int
	FilesChanged  int
	SpecVersion   string
	SchemaVersion int
	SyncedAt      time.Time
}

// EventRow is one row of whodunit_events — the journal grain.
type EventRow struct {
	// EventID identifies the observation, derived from the fields that
	// make it unique rather than assigned. Re-syncing the same event
	// therefore collides on the primary key instead of duplicating, which
	// is what lets a sync be re-run safely.
	EventID string

	RepoID       string
	ObservedAt   time.Time
	Agent        string
	AgentVersion string
	Session      string
	Event        string
	Tool         string
	File         string
	LinesAdded   int
	LinesRemoved int
	HunkHash     string
	SpecVersion  string
	Outcome      string
	SyncedAt     time.Time
}

// SessionRow is one row of whodunit_sessions (NAV-55).
type SessionRow struct {
	RepoID        string
	Session       string
	Agent         string
	AgentVersion  string
	FirstSeen     time.Time
	LastSeen      time.Time
	UserMessages  int
	AgentMessages int
	ToolCalls     int
	DistinctTools int
	MCPCalls      int
	SyncedAt      time.Time

	// Measured cost, timing and autonomy. Pointers so a nil survives all
	// the way to the database as NULL — an agent that does not report a
	// figure must not be recorded as having measured zero (NAV-21).
	InputTokens        *int64
	OutputTokens       *int64
	CacheReadTokens    *int64
	CacheWriteTokens   *int64
	ReasoningTokens    *int64
	DurationMS         *int64
	TimeToFirstTokenMS *int64

	// Empty means not reported. Converted to NULL at the bind, so an
	// empty string never lands in the column — COALESCE would then treat
	// it as a real value and refuse to let a later sync fill it in.
	Effort         string
	PermissionMode string
	Model          string
}

// LineRow is one row of whodunit_event_lines.
type LineRow struct {
	RepoID   string
	Hash     uint64
	FirstAt  time.Time
	SyncedAt time.Time
}

// Payload is everything one repository contributes to a sync.
type Payload struct {
	Repo     RepoRow
	Commits  []CommitRow
	Events   []EventRow
	Lines    []LineRow
	Sessions []SessionRow
}

// CommitRowsFrom maps analysed commits onto the dashboard grain.
//
// A commit with no valid trailer still produces a row, carrying
// status=undetermined. Dropping it would make coverage uncomputable —
// the denominator is every commit, not every commit we happened to
// understand (NAV-21).
func CommitRowsFrom(commits []report.Commit, repoID string, syncedAt time.Time) []CommitRow {
	rows := make([]CommitRow, 0, len(commits))
	for _, c := range commits {
		row := CommitRow{
			CommitSHA:     c.SHA,
			RepoID:        repoID,
			CommittedAt:   c.Timestamp,
			Status:        string(spec.StatusUndetermined),
			Method:        string(spec.MethodUndetermined),
			Purpose:       string(c.Purpose),
			LinesAdded:    c.LinesAdded,
			LinesRemoved:  c.LinesRemoved,
			FilesChanged:  len(c.Files),
			SchemaVersion: SchemaVersion,
			SyncedAt:      syncedAt,
		}
		if t := c.Trailer; t != nil {
			row.Status = string(t.Status)
			row.Method = string(t.Method)
			row.Agent = t.Agent
			row.AgentVersion = t.Version
			row.Ratio = t.Ratio
		}
		rows = append(rows, row)
	}
	return rows
}

// EventRowsFrom maps journal entries onto the event grain.
func EventRowsFrom(entries []journal.Entry, repoID string, syncedAt time.Time) []EventRow {
	rows := make([]EventRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, EventRow{
			EventID:      eventID(repoID, e),
			RepoID:       repoID,
			ObservedAt:   e.Timestamp,
			Agent:        e.Agent,
			AgentVersion: e.AgentVersion,
			Session:      e.Session,
			Event:        e.Event,
			Tool:         e.Tool,
			File:         e.File,
			LinesAdded:   e.LinesAdded,
			LinesRemoved: e.LinesRemoved,
			HunkHash:     e.HunkHash,
			SpecVersion:  e.SpecVersion,
			Outcome:      e.Outcome,
			SyncedAt:     syncedAt,
		})
	}
	return rows
}

// eventID derives an observation's identity from the fields that make it
// unique: the same tuple the table would otherwise have used as a
// composite primary key, hashed so it fits within MySQL's key length limit
// and does not put a file path in an index.
//
// Deriving rather than assigning is what makes sync idempotent — the same
// observation always produces the same id, so re-syncing collides instead
// of duplicating.
func eventID(repoID string, e journal.Entry) string {
	h := sha256.New()
	for _, part := range []string{
		repoID,
		e.Session,
		strconv.FormatInt(e.Timestamp.UnixNano(), 10),
		e.Tool,
		e.File,
		e.HunkHash,
	} {
		h.Write([]byte(part))
		// A separator, so ("ab", "c") and ("a", "bc") cannot collide.
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SessionRowsFrom maps session activity onto its row type.
func SessionRowsFrom(sessions []journal.Session, repoID string, syncedAt time.Time) []SessionRow {
	rows := make([]SessionRow, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, SessionRow{
			RepoID: repoID, Session: s.Session, Agent: s.Agent,
			AgentVersion: s.AgentVersion, FirstSeen: s.FirstSeen, LastSeen: s.LastSeen,
			UserMessages: s.UserMessages, AgentMessages: s.AgentMessages,
			ToolCalls: s.ToolCalls, DistinctTools: s.DistinctTools,
			MCPCalls: s.MCPCalls, SyncedAt: syncedAt,

			InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
			CacheReadTokens: s.CacheReadTokens, CacheWriteTokens: s.CacheWriteTokens,
			ReasoningTokens: s.ReasoningTokens, DurationMS: s.DurationMS,
			TimeToFirstTokenMS: s.TimeToFirstTokenMS,
			Effort:             s.Effort, PermissionMode: s.PermissionMode, Model: s.Model,
		})
	}
	return rows
}

// LineRowsFrom maps line hashes onto their row type.
//
// firstAt is the sync time rather than the original observation: the local
// agent_lines table keeps a first-seen timestamp, but a set read back for
// sync has lost which hash arrived when. Approximating it would invent
// precision, so the honest value is when it was synced.
func LineRowsFrom(hashes map[uint64]struct{}, repoID string, syncedAt time.Time) []LineRow {
	rows := make([]LineRow, 0, len(hashes))
	for h := range hashes {
		rows = append(rows, LineRow{
			RepoID:   repoID,
			Hash:     h,
			FirstAt:  syncedAt,
			SyncedAt: syncedAt,
		})
	}
	return rows
}
