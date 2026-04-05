package store

// schema is the full SQLite schema for sentinel.
// Applied incrementally via runMigrations in db.go.
// NEVER drop or rename columns in existing migrations — add new ones.
const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;

-- Sentinel-authored PRs on Forgejo
CREATE TABLE IF NOT EXISTS sentinel_prs (
    id                    TEXT PRIMARY KEY,
    repo                  TEXT NOT NULL,
    pr_number             INTEGER NOT NULL,
    pr_url                TEXT NOT NULL,
    branch                TEXT NOT NULL,
    base_branch           TEXT NOT NULL,
    title                 TEXT NOT NULL,
    pr_type               TEXT NOT NULL,
    priority_tier         TEXT NOT NULL DEFAULT 'low',
    related_pr_number     INTEGER,
    discord_message_id    TEXT NOT NULL,
    discord_channel_id    TEXT NOT NULL,
    discord_thread_id     TEXT,
    status                TEXT NOT NULL DEFAULT 'open',
    opened_at             DATETIME NOT NULL,
    resolved_at           DATETIME,
    resolved_by           TEXT,
    task_id               TEXT,
    UNIQUE(repo, pr_number)
);
CREATE INDEX IF NOT EXISTS idx_sentinel_prs_status ON sentinel_prs(status);
CREATE INDEX IF NOT EXISTS idx_sentinel_prs_discord_msg ON sentinel_prs(discord_message_id);

-- Per-repo sync state (last synced Forgejo HEAD SHA)
CREATE TABLE IF NOT EXISTS repo_sync_state (
    repo          TEXT PRIMARY KEY,
    forgejo_sha   TEXT NOT NULL,
    synced_at     DATETIME NOT NULL
);

-- Sync run records (Modes 3 and 4)
CREATE TABLE IF NOT EXISTS sync_runs (
    id                   TEXT PRIMARY KEY,
    repo                 TEXT NOT NULL,
    mode                 INTEGER NOT NULL,
    status               TEXT NOT NULL,
    started_at           DATETIME NOT NULL,
    completed_at         DATETIME,
    files_synced         INTEGER DEFAULT 0,
    files_with_pending   INTEGER DEFAULT 0,
    findings_high        INTEGER DEFAULT 0,
    findings_medium      INTEGER DEFAULT 0,
    findings_low         INTEGER DEFAULT 0
);

-- Individual sanitization findings per file per sync run
CREATE TABLE IF NOT EXISTS sanitization_findings (
    id                    TEXT PRIMARY KEY,
    sync_run_id           TEXT NOT NULL REFERENCES sync_runs(id),
    layer                 INTEGER NOT NULL,
    repo                  TEXT NOT NULL,
    filename              TEXT NOT NULL,
    line_number           INTEGER NOT NULL,
    byte_offset_start     INTEGER NOT NULL,
    byte_offset_end       INTEGER NOT NULL,
    original_value        TEXT NOT NULL,
    suggested_replacement TEXT NOT NULL,
    category              TEXT NOT NULL,
    confidence            TEXT NOT NULL,
    auto_redacted         BOOLEAN NOT NULL DEFAULT FALSE,
    token_index           INTEGER,
    pending_resolution_id TEXT REFERENCES pending_resolutions(id)
);
CREATE INDEX IF NOT EXISTS idx_findings_repo_file ON sanitization_findings(repo, filename);
CREATE INDEX IF NOT EXISTS idx_findings_sync_run ON sanitization_findings(sync_run_id);

-- Pending operator decisions on medium/low confidence findings
CREATE TABLE IF NOT EXISTS pending_resolutions (
    id                    TEXT PRIMARY KEY,
    repo                  TEXT NOT NULL,
    filename              TEXT NOT NULL,
    finding_id            TEXT NOT NULL REFERENCES sanitization_findings(id),
    sync_run_id           TEXT NOT NULL REFERENCES sync_runs(id),
    forgejo_issue_number  INTEGER,
    discord_message_id    TEXT NOT NULL,
    discord_channel_id    TEXT NOT NULL,
    discord_thread_id     TEXT,
    has_thread            BOOLEAN NOT NULL DEFAULT FALSE,
    suggested_replacement TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending',
    superseded_by         TEXT REFERENCES pending_resolutions(id),
    resolved_at           DATETIME,
    resolved_by           TEXT,
    final_value           TEXT
);
CREATE INDEX IF NOT EXISTS idx_resolutions_discord_msg ON pending_resolutions(discord_message_id);
CREATE INDEX IF NOT EXISTS idx_resolutions_repo_file ON pending_resolutions(repo, filename, status);

-- Per-repo approved values allowlist
CREATE TABLE IF NOT EXISTS approved_values (
    id          TEXT PRIMARY KEY,
    repo        TEXT NOT NULL,
    value       TEXT NOT NULL,
    category    TEXT NOT NULL,
    approved_by TEXT NOT NULL,
    approved_at DATETIME NOT NULL,
    UNIQUE(repo, value)
);

-- Pending operator confirmations (allowlist, --force migration)
CREATE TABLE IF NOT EXISTS pending_confirmations (
    id                 TEXT PRIMARY KEY,
    kind               TEXT NOT NULL,
    repo               TEXT NOT NULL,
    value              TEXT,
    discord_message_id TEXT NOT NULL,
    discord_channel_id TEXT NOT NULL,
    requested_by       TEXT NOT NULL,
    created_at         DATETIME NOT NULL,
    expires_at         DATETIME NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending'
);

-- Analysis task records
CREATE TABLE IF NOT EXISTS tasks (
    id              TEXT PRIMARY KEY,
    repo            TEXT NOT NULL,
    pipeline_run_id TEXT,
    task_type       TEXT NOT NULL,
    complexity      TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL,
    affected_files  TEXT NOT NULL,
    acceptance      TEXT NOT NULL,
    branch          TEXT,
    executor        TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      DATETIME NOT NULL,
    started_at      DATETIME,
    completed_at    DATETIME,
    pr_number       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_tasks_repo_status ON tasks(repo, status);
CREATE INDEX IF NOT EXISTS idx_tasks_branch ON tasks(branch);

-- Dedup records for PR reviews
CREATE TABLE IF NOT EXISTS pr_dedup (
    id            TEXT PRIMARY KEY,
    repo          TEXT NOT NULL,
    pr_number     INTEGER NOT NULL,
    reviewed_at   DATETIME NOT NULL,
    UNIQUE(repo, pr_number)
);

-- PR review records (Mode 2 results)
CREATE TABLE IF NOT EXISTS reviews (
    id              TEXT PRIMARY KEY,
    repo            TEXT NOT NULL,
    pr_number       INTEGER NOT NULL,
    verdict         TEXT NOT NULL,
    comment_posted  BOOLEAN NOT NULL DEFAULT FALSE,
    housekeeping_pr INTEGER,
    fix_pr          INTEGER,
    reviewed_at     DATETIME NOT NULL
);

-- Per-file sync records (last synced SHA per file per repo)
CREATE TABLE IF NOT EXISTS sync_records (
    id           TEXT PRIMARY KEY,
    repo         TEXT NOT NULL,
    filename     TEXT NOT NULL,
    last_sha     TEXT NOT NULL,
    synced_at    DATETIME NOT NULL,
    UNIQUE(repo, filename)
);

-- All incoming webhook events (audit log)
CREATE TABLE IF NOT EXISTS webhook_events (
    id            TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    repo          TEXT NOT NULL,
    payload       BLOB NOT NULL,
    hmac_valid    BOOLEAN NOT NULL,
    received_at   DATETIME NOT NULL,
    processed_at  DATETIME,
    error         TEXT
);

-- All sentinel-originated actions (audit trail)
CREATE TABLE IF NOT EXISTS sentinel_actions (
    id            TEXT PRIMARY KEY,
    action_type   TEXT NOT NULL,
    repo          TEXT NOT NULL,
    entity_id     TEXT,
    detail        TEXT,
    actor         TEXT NOT NULL DEFAULT 'sentinel',
    created_at    DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_actions_type_repo_created ON sentinel_actions(action_type, repo, created_at);

-- Migration state per repo
CREATE TABLE IF NOT EXISTS migration_status (
    repo            TEXT PRIMARY KEY,
    status          TEXT NOT NULL DEFAULT 'pending',
    forgejo_sha     TEXT,
    started_at      DATETIME,
    completed_at    DATETIME,
    error           TEXT
);

-- Documentation generation state per (repo, doc_file)
CREATE TABLE IF NOT EXISTS doc_state (
    repo                TEXT NOT NULL,
    doc_file            TEXT NOT NULL,
    generated_from_sha  TEXT NOT NULL,
    generated_at        DATETIME NOT NULL,
    pr_branch           TEXT,
    status              TEXT NOT NULL DEFAULT 'current',
    PRIMARY KEY (repo, doc_file)
);
CREATE INDEX IF NOT EXISTS idx_doc_state_repo ON doc_state(repo);

-- Changelog entries per repo (append-only audit; CHANGELOG.md is the rendered form)
CREATE TABLE IF NOT EXISTS changelog_entries (
    id          TEXT PRIMARY KEY,
    repo        TEXT NOT NULL,
    sha         TEXT NOT NULL,
    entry       TEXT NOT NULL,
    pr_title    TEXT,
    created_at  DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_changelog_entries_repo ON changelog_entries(repo, created_at);
`
