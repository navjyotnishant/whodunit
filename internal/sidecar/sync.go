package sidecar

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

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
	return WriteProgress(db, p, nil)
}

// WriteProgress is Write with a callback after each row.
//
// The callback exists so a caller can show progress on a payload of tens of
// thousands of rows, where the alternative is a silent pause long enough to
// read as a hang. It is called inside the transaction and must not block:
// anything slow there stalls the write it is reporting on.
//
// A nil callback makes this exactly Write.
func WriteProgress(db *Store, p Payload, onRow func(done, total int)) (Counts, error) {
	var counts Counts
	total := len(p.Commits) + len(p.Events) + len(p.Sessions) + len(p.Lines)
	done := 0
	tick := func() {
		done++
		if onRow != nil {
			onRow(done, total)
		}
	}
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
			c.FilesChanged, c.SpecVersion, c.SchemaVersion, c.SyncedAt.UnixNano(),
			nullString(c.ChangedBy)); err != nil {
			return counts, fmt.Errorf("write commit %s: %w", c.CommitSHA, err)
		}
		counts.Commits++
		tick()
	}

	for _, e := range p.Events {
		if _, err := tx.Exec(upsertEvent(mysql),
			e.EventID, e.RepoID, e.ObservedAt.UnixNano(), e.Agent, e.AgentVersion,
			e.Session, e.Event, e.Tool, e.File, e.LinesAdded, e.LinesRemoved,
			e.HunkHash, e.SpecVersion, e.Outcome, e.SyncedAt.UnixNano(),
			nullString(e.Model), nullString(e.Branch), nullString(e.MCPServer),
			e.UserModified); err != nil {
			return counts, fmt.Errorf("write event: %w", err)
		}
		counts.Events++
		tick()
	}

	for _, s := range p.Sessions {
		if _, err := tx.Exec(upsertSession(mysql),
			s.RepoID, s.Session, s.Agent, s.AgentVersion,
			s.FirstSeen.UnixNano(), s.LastSeen.UnixNano(),
			s.UserMessages, s.AgentMessages, s.ToolCalls, s.DistinctTools,
			s.MCPCalls, s.SyncedAt.UnixNano(),
			s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens,
			s.ReasoningTokens, s.DurationMS, s.TimeToFirstTokenMS,
			nullString(s.Effort), nullString(s.PermissionMode), nullString(s.Model),
			s.Compactions); err != nil {
			return counts, fmt.Errorf("write session: %w", err)
		}
		counts.Sessions++
		tick()
	}

	if b := p.Baseline; b != nil {
		if _, err := tx.Exec(upsertBaseline(mysql),
			b.RepoID, b.CapturedAt.UnixNano(), b.WindowDays, b.HeadSHA,
			b.SchemaVersion, nanosOrNil(b.WindowSince), nanosOrNil(b.WindowUntil),
			b.Commits, b.CommitsPerWeek, b.MedianDiffLines,
			b.MeanHoursBetween, b.Reverts, b.RevertRate,
			b.SyncedAt.UnixNano()); err != nil {
			return counts, fmt.Errorf("write baseline: %w", err)
		}
		counts.Baselines = 1
	}

	for _, l := range p.Lines {
		if _, err := tx.Exec(upsertLine(mysql),
			l.RepoID, int64(l.Hash), l.FirstAt.UnixNano(), l.SyncedAt.UnixNano()); err != nil {
			return counts, fmt.Errorf("write line hash: %w", err)
		}
		counts.Lines++
		tick()
	}

	if err := tx.Commit(); err != nil {
		return counts, fmt.Errorf("commit: %w", err)
	}
	return counts, nil
}

// Counts is what a sync moved.
type Counts struct {
	Repos     int
	Commits   int
	Events    int
	Lines     int
	Sessions  int
	Baselines int
}

// The two engines spell "insert or replace" differently and neither
// accepts the other's syntax, so each statement exists in two forms
// differing only in that clause.

func upsertRepo(mysql bool) string {
	cols := `INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at) VALUES (?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE spec_version=VALUES(spec_version), synced_at=VALUES(synced_at)`
	}
	// The conflict target is the full key. Narrowing it to repo_id would
	// restore the overwrite the key change exists to prevent, and it would
	// do so silently.
	return cols + ` ON CONFLICT(repo_id, contributor) DO UPDATE SET spec_version=excluded.spec_version, synced_at=excluded.synced_at`
}

func upsertCommit(mysql bool) string {
	cols := `INSERT INTO whodunit_commits
		(commit_sha, repo_id, committed_at, status, method, agent, agent_version,
		 purpose, ratio, lines_added, lines_removed, files_changed, spec_version,
		 schema_version, synced_at, changed_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE
			status=VALUES(status), method=VALUES(method), agent=VALUES(agent),
			agent_version=VALUES(agent_version), purpose=VALUES(purpose),
			ratio=VALUES(ratio), lines_added=VALUES(lines_added),
			lines_removed=VALUES(lines_removed), files_changed=VALUES(files_changed),
			spec_version=VALUES(spec_version), schema_version=VALUES(schema_version),
			synced_at=VALUES(synced_at), changed_by=VALUES(changed_by)`
	}
	return cols + ` ON CONFLICT(commit_sha, repo_id) DO UPDATE SET
		status=excluded.status, method=excluded.method, agent=excluded.agent,
		agent_version=excluded.agent_version, purpose=excluded.purpose,
		ratio=excluded.ratio, lines_added=excluded.lines_added,
		lines_removed=excluded.lines_removed, files_changed=excluded.files_changed,
		spec_version=excluded.spec_version, schema_version=excluded.schema_version,
		synced_at=excluded.synced_at, changed_by=excluded.changed_by`
}

func upsertEvent(mysql bool) string {
	cols := `INSERT INTO whodunit_events
		(event_id, repo_id, observed_at, agent, agent_version, session, event,
		 tool, file, lines_added, lines_removed, hunk_hash, spec_version, outcome, synced_at,
		 model, branch, mcp_server, user_modified)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	// outcome is refreshed on conflict, unlike the rest of the row: an
	// event's identity is fixed but its outcome can be backfilled by a
	// later ingest that finally saw the tool result.
	//
	// The observation columns are COALESCEd rather than replaced, for the
	// reason the session ones are: a re-sync over a window that cannot see
	// them must not erase what an earlier pass established, and centrally
	// there is no transcript left to re-read.
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE outcome=VALUES(outcome),
			lines_added=VALUES(lines_added), lines_removed=VALUES(lines_removed),
			synced_at=VALUES(synced_at),
			model=COALESCE(VALUES(model), model),
			branch=COALESCE(VALUES(branch), branch),
			mcp_server=COALESCE(VALUES(mcp_server), mcp_server),
			user_modified=COALESCE(VALUES(user_modified), user_modified)`
	}
	return cols + ` ON CONFLICT(event_id) DO UPDATE SET outcome=excluded.outcome,
		lines_added=excluded.lines_added, lines_removed=excluded.lines_removed,
		synced_at=excluded.synced_at,
		model=COALESCE(excluded.model, whodunit_events.model),
		branch=COALESCE(excluded.branch, whodunit_events.branch),
		mcp_server=COALESCE(excluded.mcp_server, whodunit_events.mcp_server),
		user_modified=COALESCE(excluded.user_modified, whodunit_events.user_modified)`
}

// upsertSession refreshes a session's counters and fills in its
// measurements without ever erasing one.
//
// The counters are replaced outright: a session grows while it is open, so
// the newest read is the right one.
//
// The measured columns are COALESCEd instead. A developer machine that
// re-syncs after an ingest over a narrow window sends nil for tokens that
// were measured on an earlier pass, and a plain overwrite would replace a
// real figure with NULL — silently, permanently, and centrally, where the
// original transcript is not available to re-read. COALESCE takes a new
// value when there is one and keeps the old when there is not.
//
// Each engine spells the incoming row differently: MySQL as VALUES(col),
// SQLite as excluded.col. Same shape, two dialects, which is why this
// function exists rather than one shared string.
// nullString writes an empty string as SQL NULL, so COALESCE does not
// mistake "not reported" for a measured empty value (NAV-21).
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func upsertSession(mysql bool) string {
	cols := `INSERT INTO whodunit_sessions
		(repo_id, session, agent, agent_version, first_seen, last_seen,
		 user_messages, agent_messages, tool_calls, distinct_tools, mcp_calls, synced_at,
		 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		 reasoning_tokens, duration_ms, time_to_first_token_ms,
		 effort, permission_mode, model, compactions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE
			last_seen=VALUES(last_seen), user_messages=VALUES(user_messages),
			agent_messages=VALUES(agent_messages), tool_calls=VALUES(tool_calls),
			distinct_tools=VALUES(distinct_tools), mcp_calls=VALUES(mcp_calls),
			synced_at=VALUES(synced_at),
			input_tokens=COALESCE(VALUES(input_tokens), input_tokens),
			output_tokens=COALESCE(VALUES(output_tokens), output_tokens),
			cache_read_tokens=COALESCE(VALUES(cache_read_tokens), cache_read_tokens),
			cache_write_tokens=COALESCE(VALUES(cache_write_tokens), cache_write_tokens),
			reasoning_tokens=COALESCE(VALUES(reasoning_tokens), reasoning_tokens),
			duration_ms=COALESCE(VALUES(duration_ms), duration_ms),
			time_to_first_token_ms=COALESCE(VALUES(time_to_first_token_ms), time_to_first_token_ms),
			effort=COALESCE(VALUES(effort), effort),
			permission_mode=COALESCE(VALUES(permission_mode), permission_mode),
			model=COALESCE(VALUES(model), model),
			compactions=COALESCE(VALUES(compactions), compactions)`
	}
	return cols + ` ON CONFLICT(repo_id, session) DO UPDATE SET
		last_seen=excluded.last_seen, user_messages=excluded.user_messages,
		agent_messages=excluded.agent_messages, tool_calls=excluded.tool_calls,
		distinct_tools=excluded.distinct_tools, mcp_calls=excluded.mcp_calls,
		synced_at=excluded.synced_at,
		input_tokens=COALESCE(excluded.input_tokens, whodunit_sessions.input_tokens),
		output_tokens=COALESCE(excluded.output_tokens, whodunit_sessions.output_tokens),
		cache_read_tokens=COALESCE(excluded.cache_read_tokens, whodunit_sessions.cache_read_tokens),
		cache_write_tokens=COALESCE(excluded.cache_write_tokens, whodunit_sessions.cache_write_tokens),
		reasoning_tokens=COALESCE(excluded.reasoning_tokens, whodunit_sessions.reasoning_tokens),
		duration_ms=COALESCE(excluded.duration_ms, whodunit_sessions.duration_ms),
		time_to_first_token_ms=COALESCE(excluded.time_to_first_token_ms, whodunit_sessions.time_to_first_token_ms),
		effort=COALESCE(excluded.effort, whodunit_sessions.effort),
		permission_mode=COALESCE(excluded.permission_mode, whodunit_sessions.permission_mode),
		model=COALESCE(excluded.model, whodunit_sessions.model),
		compactions=COALESCE(excluded.compactions, whodunit_sessions.compactions)`
}

// nanosOrNil keeps an absent timestamp NULL rather than storing 0, which
// would render as 1970 on a panel naming the window it compared (NAV-21).
func nanosOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

// upsertBaseline records a captured snapshot.
//
// Only synced_at is refreshed on conflict — every measured column is left
// alone. A baseline is immutable by design: `baseline.Write` refuses to
// overwrite one locally, and the whole reason the window is fixed before
// any comparison exists is that a window chosen afterwards can manufacture
// almost any figure.
//
// So re-syncing the same capture is a no-op on the numbers, and a capture
// with a different timestamp inserts a new row rather than replacing the
// old one — the key includes captured_at precisely so that both are kept.
func upsertBaseline(mysql bool) string {
	cols := `INSERT INTO whodunit_baselines
		(repo_id, captured_at, window_days, head_sha, schema_version,
		 window_since, window_until,
		 commits, commits_per_week, median_diff_lines, mean_hours_between,
		 reverts, revert_rate, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(repo_id, captured_at) DO UPDATE SET synced_at=excluded.synced_at`
}

func upsertLine(mysql bool) string {
	cols := `INSERT INTO whodunit_event_lines (repo_id, line_hash, first_at, synced_at) VALUES (?, ?, ?, ?)`
	if mysql {
		return cols + ` ON DUPLICATE KEY UPDATE synced_at=VALUES(synced_at)`
	}
	return cols + ` ON CONFLICT(repo_id, line_hash) DO UPDATE SET synced_at=excluded.synced_at`
}

// LastSync reports when a repository last published, from the store's own
// synced_at column.
//
// Read from the target rather than tracked locally on purpose. The remote is
// where the answer actually lives: a local timestamp would say a sync
// happened, not that the rows arrived, and the two diverge exactly when it
// matters — a write that failed halfway, or a database restored from an
// older backup.
//
// Returns the zero time when the repository has never synced. That is a
// state worth reporting, not an error.
// LastSyncAll returns when each repository last published, for the whole set
// at once.
//
// One query and one connection for N repositories, rather than N of each. The
// cross-repo view calls this: asking per repository meant a connection and a
// handshake each, so a machine with ten repositories paid ten round trips —
// and ten separate two-second timeouts if the target was slow, which turns a
// status command into a twenty-second stall.
//
// Repositories that have never published are simply absent from the result;
// the caller distinguishes that from a failed lookup by the error.
func LastSyncAll(db *Store) (map[string]time.Time, error) {
	rows, err := db.Query(`SELECT repo_id, synced_at FROM whodunit_repos`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var ns sql.NullInt64
		if err := rows.Scan(&id, &ns); err != nil {
			return nil, err
		}
		if ns.Valid && ns.Int64 != 0 {
			out[id] = time.Unix(0, ns.Int64)
		}
	}
	return out, rows.Err()
}

func LastSync(db *Store, repoID string) (time.Time, error) {
	var ns sql.NullInt64
	// From whodunit_repos, which holds one row per repository keyed by its
	// primary key — a single-row lookup whatever the store's size.
	//
	// Not MAX(synced_at) over whodunit_commits, which is the same answer at
	// O(commits-for-this-repo): synced_at is in no index, so that aggregate
	// walks every one of the repository's rows. Invisible against a laptop
	// database and expensive against a team's, and `dun status` runs it on
	// every invocation — once per repository in the cross-repo view.
	err := db.QueryRow(
		`SELECT synced_at FROM whodunit_repos WHERE repo_id = ?`, repoID).Scan(&ns)
	if err == sql.ErrNoRows {
		// Never published. Not an error — it is the answer for a repository
		// that has only ever recorded locally.
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if !ns.Valid || ns.Int64 == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, ns.Int64), nil
}

// Unsynced reports how far behind the target is, from one indexed lookup.
//
// Deliberately not a COUNT. Counting rows for a repository walks every index
// entry it has, which is fine on a laptop database and is not fine on a
// shared one holding millions of events across a team — and `dun status`
// runs constantly. MAX(observed_at) over the (repo_id, observed_at) index is
// answered from the index alone: MySQL reports "Select tables optimized
// away", meaning zero rows read, whatever the table's size.
//
// The trade is that this answers "how far behind in time" rather than "how
// many rows behind". That is the more useful answer anyway — "nothing since
// Tuesday" tells someone what to do; "412 events" does not — and it costs
// one round trip instead of three.
//
// A zero newest means the repository has never published.
func Unsynced(db *Store, repoID string) (remoteNewest time.Time, err error) {
	var ns sql.NullInt64
	if err := db.QueryRow(
		`SELECT MAX(observed_at) FROM whodunit_events WHERE repo_id = ?`,
		repoID).Scan(&ns); err != nil {
		return time.Time{}, err
	}
	if !ns.Valid || ns.Int64 == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, ns.Int64), nil
}
