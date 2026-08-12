package sidecar

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// Store is a connection to an aggregation database, carrying which engine
// it is.
//
// The dialect is recorded at Open rather than probed later: we chose the
// driver, so asking the server what it is afterwards is indirection that
// can only go wrong. It did — MySQL 8 returns a bare "8.4.11" from
// VERSION(), with no product name to match on.
type Store struct {
	*sql.DB
	mysql bool
}

// Open connects to an aggregation store from a URL.
//
// Supported:
//
//	mysql://user:pass@host:port/database    a DevLake database
//	sqlite:///absolute/path.db              a local file, for trying it out
//
// The database is shared with DevLake, so nothing here creates or drops a
// database — only whodunit's own tables within one that already exists.
func Open(dsn string) (*Store, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	switch u.Scheme {
	case "mysql":
		// The Go MySQL driver wants its own DSN shape rather than a URL.
		// parseTime is required: without it, timestamps come back as
		// []byte and every date comparison silently misbehaves.
		pass, _ := u.User.Password()
		driverDSN := fmt.Sprintf("%s:%s@tcp(%s)%s?parseTime=true&loc=UTC",
			u.User.Username(), pass, u.Host, u.Path)
		db, err := sql.Open("mysql", driverDSN)
		if err != nil {
			return nil, err
		}
		return &Store{DB: db, mysql: true}, nil

	case "sqlite":
		db, err := sql.Open("sqlite", strings.TrimPrefix(u.Path, "/"))
		if err != nil {
			return nil, err
		}
		return &Store{DB: db}, nil

	default:
		return nil, fmt.Errorf("unsupported database scheme %q: expected mysql or sqlite", u.Scheme)
	}
}

// EnsureSchema creates whodunit's tables if they are not already present.
//
// Index creation failures are reported but not fatal: the realistic cause
// is that the index already exists (MySQL has no CREATE INDEX IF NOT
// EXISTS), and a missing index costs query speed rather than correctness.
func EnsureSchema(db *Store) error {
	for _, stmt := range splitStatements(Schema) {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema: %w\nstatement: %s", err, stmt)
		}
	}
	for _, stmt := range Migrations {
		_, _ = db.Exec(stmt)
	}
	for _, stmt := range Indexes {
		_, _ = db.Exec(stmt)
	}
	return nil
}

// splitStatements breaks the DDL into individual statements, because the
// MySQL driver refuses multiple statements in one Exec by default.
func splitStatements(ddl string) []string {
	var out []string
	for _, raw := range strings.Split(ddl, ";") {
		var lines []string
		for _, line := range strings.Split(raw, "\n") {
			trimmed := strings.TrimSpace(line)
			// SQL comments are fine inside a statement but a comment-only
			// fragment is not a statement.
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			continue
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return out
}

// Write upserts a payload.
//
// Every table is keyed so that re-syncing the same data collides rather
// than duplicating, which is what makes this safe to run repeatedly — the
// local journal is the source of truth and a sync is a projection of it,
// not a handoff.
func Write(db *Store, p Payload) (Counts, error) {
	var counts Counts
	mysql := db.mysql

	tx, err := db.Begin()
	if err != nil {
		return counts, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(upsertRepo(mysql),
		p.Repo.RepoID, p.Repo.Contributor, p.Repo.SpecVersion, p.Repo.SyncedAt.UnixNano()); err != nil {
		return counts, fmt.Errorf("write repo: %w", err)
	}
	counts.Repos = 1

	for _, c := range p.Commits {
		if _, err := tx.Exec(upsertCommit(mysql),
			c.CommitSHA, c.RepoID, c.CommittedAt.UnixNano(), c.Status, c.Method,
			c.Agent, c.AgentVersion, c.Purpose, c.Ratio, c.LinesAdded, c.LinesRemoved,
			c.FilesChanged, c.SpecVersion, c.SchemaVersion, c.SyncedAt.UnixNano()); err != nil {
			return counts, fmt.Errorf("write commit %s: %w", c.CommitSHA, err)
		}
		counts.Commits++
	}

	for _, e := range p.Events {
		if _, err := tx.Exec(upsertEvent(mysql),
			e.EventID, e.RepoID, e.ObservedAt.UnixNano(), e.Agent, e.AgentVersion,
			e.Session, e.Event, e.Tool, e.File, e.LinesAdded, e.LinesRemoved,
			e.HunkHash, e.SpecVersion, e.Outcome, e.SyncedAt.UnixNano()); err != nil {
			return counts, fmt.Errorf("write event: %w", err)
		}
		counts.Events++
	}

	for _, s := range p.Sessions {
		if _, err := tx.Exec(upsertSession(mysql),
			s.RepoID, s.Session, s.Agent, s.AgentVersion,
			s.FirstSeen.UnixNano(), s.LastSeen.UnixNano(),
			s.UserMessages, s.AgentMessages, s.ToolCalls, s.DistinctTools,
			s.MCPCalls, s.SyncedAt.UnixNano()); err != nil {
			return counts, fmt.Errorf("write session: %w", err)
		}
		counts.Sessions++
	}

	for _, l := range p.Lines {
		if _, err := tx.Exec(upsertLine(mysql),
			l.RepoID, int64(l.Hash), l.FirstAt.UnixNano(), l.SyncedAt.UnixNano()); err != nil {
			return counts, fmt.Errorf("write line hash: %w", err)
		}
		counts.Lines++
	}

	if err := tx.Commit(); err != nil {
		return counts, fmt.Errorf("commit: %w", err)
	}
	return counts, nil
}

// Counts is what a sync moved.
type Counts struct {
	Repos    int
	Commits  int
	Events   int
	Lines    int
	Sessions int
}

// The two engines spell "insert or replace" differently and neither
// accepts the other's syntax, so each statement exists in two forms
// differing only in that clause.

func upsertRepo(mysql bool) string {
	cols := `INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at) VALUES (?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE contributor=VALUES(contributor), spec_version=VALUES(spec_version), synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(repo_id) DO UPDATE SET contributor=excluded.contributor, spec_version=excluded.spec_version, synced_at=excluded.synced_at`
}

func upsertCommit(mysql bool) string {
	cols := `INSERT INTO whodunit_commits
		(commit_sha, repo_id, committed_at, status, method, agent, agent_version,
		 purpose, ratio, lines_added, lines_removed, files_changed, spec_version,
		 schema_version, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE
			status=VALUES(status), method=VALUES(method), agent=VALUES(agent),
			agent_version=VALUES(agent_version), purpose=VALUES(purpose),
			ratio=VALUES(ratio), lines_added=VALUES(lines_added),
			lines_removed=VALUES(lines_removed), files_changed=VALUES(files_changed),
			spec_version=VALUES(spec_version), schema_version=VALUES(schema_version),
			synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(commit_sha, repo_id) DO UPDATE SET
		status=excluded.status, method=excluded.method, agent=excluded.agent,
		agent_version=excluded.agent_version, purpose=excluded.purpose,
		ratio=excluded.ratio, lines_added=excluded.lines_added,
		lines_removed=excluded.lines_removed, files_changed=excluded.files_changed,
		spec_version=excluded.spec_version, schema_version=excluded.schema_version,
		synced_at=excluded.synced_at`
}

func upsertEvent(mysql bool) string {
	cols := `INSERT INTO whodunit_events
		(event_id, repo_id, observed_at, agent, agent_version, session, event,
		 tool, file, lines_added, lines_removed, hunk_hash, spec_version, outcome, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	// outcome is refreshed on conflict, unlike the rest of the row: an
	// event's identity is fixed but its outcome can be backfilled by a
	// later ingest that finally saw the tool result.
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE outcome=VALUES(outcome),
			lines_added=VALUES(lines_added), lines_removed=VALUES(lines_removed),
			synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(event_id) DO UPDATE SET outcome=excluded.outcome,
		lines_added=excluded.lines_added, lines_removed=excluded.lines_removed,
		synced_at=excluded.synced_at`
}

func upsertSession(mysql bool) string {
	cols := `INSERT INTO whodunit_sessions
		(repo_id, session, agent, agent_version, first_seen, last_seen,
		 user_messages, agent_messages, tool_calls, distinct_tools, mcp_calls, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	// A session grows while it is open, so every counter is refreshed.
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE
			last_seen=VALUES(last_seen), user_messages=VALUES(user_messages),
			agent_messages=VALUES(agent_messages), tool_calls=VALUES(tool_calls),
			distinct_tools=VALUES(distinct_tools), mcp_calls=VALUES(mcp_calls),
			synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(repo_id, session) DO UPDATE SET
		last_seen=excluded.last_seen, user_messages=excluded.user_messages,
		agent_messages=excluded.agent_messages, tool_calls=excluded.tool_calls,
		distinct_tools=excluded.distinct_tools, mcp_calls=excluded.mcp_calls,
		synced_at=excluded.synced_at`
}

func upsertLine(mysql bool) string {
	cols := `INSERT INTO whodunit_event_lines (repo_id, line_hash, first_at, synced_at) VALUES (?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(repo_id, line_hash) DO UPDATE SET synced_at=excluded.synced_at`
}
