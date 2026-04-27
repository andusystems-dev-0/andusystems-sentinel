# Architecture

This document describes Sentinel's internal architecture, component interactions, data flows, and key design decisions.

## System Overview

Sentinel is a single Go binary that orchestrates the software development lifecycle across Forgejo (source of truth), GitHub (sanitized public mirror), Ollama (local LLM), and Discord (operator interface). It also serves an embedded web dashboard and REST API for real-time monitoring.

```
                          +-------------------------+
                          |       Discord Bot        |
                          |  (embeds, reactions,     |
                          |   threads, /commands)    |
                          +--------+----------------+
                                   |
                                   | operator reactions
                                   | notifications
                                   v
+-----------+  webhook   +----------------------------+   push     +-----------+
|           | ---------> |         Sentinel            | --------> |           |
|  Forgejo  |            |                            |           |  GitHub   |
|  (git)    | <--------- |  +-------+ +------------+ |           | (mirror)  |
|           |   PRs      |  |SQLite | | Worktree   | |           |           |
+-----------+            |  | (DB)  | | Manager    | |           +-----------+
                         |  +-------+ +------------+ |
                         |                            |
                         |  +-------+ +------------+ |    +----------+
                         |  |Ollama | |Claude Code | |--->| Web      |
                         |  |(LLM)  | |  (CLI)     | |   | Dashboard|
                         |  +-------+ +------------+ |    +----------+
                         +----------------------------+
                                   |
                                   | REST API + SSE
                                   v
                         +----------------------------+
                         |   Browser / API Clients     |
                         +----------------------------+
```

## Component Map

### Entry Point

`cmd/sentinel/main.go` — loads config, initialises all subsystems, wires dependencies, dispatches by mode.

### Core Packages

| Package | Path | Responsibility |
|---------|------|----------------|
| **config** | `internal/config/` | YAML loading, env var injection, startup validation, defaults |
| **types** | `internal/types/` | All data models and interface contracts |
| **store** | `internal/store/` | SQLite layer; one file per table group; idempotent DDL migrations |
| **webhook** | `internal/webhook/` | HTTP server, HMAC validation, buffered queue, async worker pool |
| **forge** | `internal/forge/` | Forgejo (Gitea SDK) and GitHub (go-github) API clients; webhook auto-registration |
| **llm** | `internal/llm/` | Ollama client, multi-call batcher, prompt loading, semaphore |
| **sanitize** | `internal/sanitize/` | Three-layer sanitization pipeline with skip zones and scrub patterns |
| **executor** | `internal/executor/` | Claude Code CLI invocation via `os/exec`; prompt templating |
| **pipeline** | `internal/pipeline/` | Mode 1 nightly orchestration (preflight, routing, dependency resolution) |
| **sync** | `internal/sync/` | Mode 3 incremental sync (Forgejo → sanitize → GitHub) |
| **migration** | `internal/migration/` | Mode 4 full-repo migration with Discord confirmation and auto-bootstrap |
| **reconcile** | `internal/reconcile/` | Drift detection between Forgejo HEAD and last sync SHA; auto-remediation |
| **discord** | `internal/discord/` | Bot lifecycle, embeds, reactions, threads, digest, slash commands |
| **prnotify** | `internal/prnotify/` | PR notifications, reaction handlers, Forgejo→Discord sync, mention tracking |
| **worktree** | `internal/worktree/` | Git worktree lifecycle, per-repo locking, token_index resolution, GitHub push |
| **claude** | `internal/claude/` | Claude Code CLI wrapper for sanitization Layer 2 fallback and Layer 3 |
| **docs** | `internal/docs/` | Documentation generation, changelog management, Obsidian vault integration |
| **api** | `internal/api/` | REST API server, SSE event bus, embedded SPA handler |

### Supporting Files

| Path | Purpose |
|------|---------|
| `prompts/` | LLM role prompts (Roles A through G), loaded at startup |
| `fixtures/` | Test data: webhook payloads, diffs, synthetic secret files |
| `tools/` | CLI test harnesses referenced by Makefile targets |
| `web/` | SvelteKit dashboard source (compiled and embedded into Go binary) |
| `charts/sentinel/` | Helm chart for Kubernetes deployment |
| `argocd/sentinel-app.yaml` | ArgoCD Application manifest (manual sync) |

## Data Flow: Mode 1 — Nightly Pipeline

```
Cron trigger (or --mode nightly)
    |
    v
Pre-flight checks per repo:
  - Excluded?
  - Active dev within skip window?
  - PR flood threshold exceeded?
  - Pending migration?
    | (pass)
    v
Diff Forgejo HEAD vs last recorded SHA
  (--force: full scan of all files, ignoring skip window)
    |
    v
Partition diffs into LLM-sized batches
    |
    v
Ollama Role A (Analyst): identify tasks
    |
    v
Router: task type + complexity -> executor
  - Claude Code CLI: fix, feat, vulnerability, refactor
  - LLM (Ollama): docs, dependency-update
    |
    v
Executor creates branch, commits changes
    |
    v
Open PR on Forgejo
    |
    v
Post Discord notification with reaction controls
    |
    v
Update stale documentation targets (if doc-gen enabled)
    |
    v
Post nightly digest summary
```

## Data Flow: Mode 2 — PR Review (Webhook)

```
Forgejo pull_request webhook event
    |
    v
HTTP handler: HMAC validate -> parse -> enqueue -> ACK (200)
    |
    v
Worker pool picks up event
    |
    v
SyncHandler: update Discord embed for PR open/merge/close
    |
    v
Ollama Role B (Reviewer): analyse PR diff
    |
    v
ReviewResult: verdict + per-file notes + security assessment
    |
    v
Post review comments on Forgejo PR
    |
    v
Discord notification (high priority PRs get @here mention, subject to cooldown)
    |
    v
Optional: open housekeeping companion PR if files need cleanup
```

## Data Flow: Mode 3 — Incremental Sync

```
Push webhook on main/master (or manual trigger, or drift reconciler)
    |
    v
Diff changed files since last sync SHA
    |
    v
For each file:
  Load skip zones (previously approved values)
    |
    v
  Layer 1: gitleaks + regex patterns
    >= 0.9 confidence -> auto-redact (tag inserted)
    < 0.9             -> pass to Layer 2
    |
    v
  Layer 2: Ollama Role D (semantic analysis)
    Any finding -> pending operator review
    On timeout/error -> fall back to Claude Code CLI
    |
    v
  Layer 3: Claude Code CLI (optional, configurable via layer3_enabled)
    Additional semantic safety-net pass
    |
    v
  Scrub patterns: regex substitutions applied to final content
    |
    v
  staging.go: assign TOKEN_N indices, build tagged content
    |
    v
Push sanitized content to GitHub mirror (MixedReset + per-file Add)
    |
    v
Post findings to Discord logs channel
```

## Data Flow: Mode 4 — Full Migration

```
--mode migrate --repo <name> [--force]
    |
    v
If --force and target exists: post Discord confirmation (TTL-based)
    |
    v
Scan all files in Forgejo repo
    |
    v
Run full sanitization pipeline (same 3 layers as Mode 3)
    |
    v
Push all sanitized files to GitHub mirror
    |
    v
Post summary + pending findings to Discord
```

### Auto-Bootstrap

On daemon startup, sentinel checks every sync-enabled repo. If a GitHub mirror does not exist or is empty, Mode 4 migration runs automatically. This eliminates manual setup for new repositories.

## Data Flow: Doc-Gen

```
--mode doc-gen --repo <name> (or nightly UpdateStale)
    |
    v
Gather source context (file list, up to max_context_files)
    |
    v
Read Obsidian vault context for domain knowledge
    |
    v
Invoke Claude Code CLI with documentation prompt
    |
    v
Claude Code writes doc files to a timestamped feature branch
    |
    v
Open PR on Forgejo
    |
    v
Post Discord notification with merge/close reactions
    |
    v
Write doc snapshots to Obsidian vault
    |
    v
If sentinel.local_checkout_base is set: fast-forward operator's
local clone from Forgejo remote after merge
```

## Webhook Processing Architecture

```
POST /webhooks/forgejo
    |
    v
Handler (synchronous):
  1. Read body (max 10 MB)
  2. HMAC-SHA256 validation (constant-time)
  3. Parse event type + repo name
  4. Enqueue to buffered channel
  5. Return HTTP 200 immediately
    |
    v
Queue (buffered channel):
  - Configurable size (default: 100)
  - Returns HTTP 429 when full (back-pressure signal)
    |
    v
Worker Pool (configurable, default: 4 workers):
  - pull_request         -> SyncHandler (Discord embed) + Mode 2 PR review
  - push (main/master)   -> Mode 3 sync trigger
  - push (sentinel/* branch) -> look up task, open PR on Forgejo
```

## REST API Architecture

The API server (`internal/api/`) exposes read-only JSON endpoints mounted on the same HTTP mux as the webhook listener:

```
GET /api/v1/sessions             -- list recent nightly sessions
GET /api/v1/sessions/active      -- get currently running session
GET /api/v1/sessions/{id}        -- get session by ID
GET /api/v1/sessions/{id}/tasks  -- list tasks for a session
GET /api/v1/tasks                -- list recent tasks
GET /api/v1/tasks/{id}           -- get task by ID
GET /api/v1/prs                  -- list open PRs
GET /api/v1/actions              -- list recent audit actions
GET /api/v1/repos                -- list configured repos with metadata
GET /api/v1/events               -- SSE stream (real-time updates)
```

All list endpoints support `?limit=N` (max 500).

### Server-Sent Events

The `/api/v1/events` endpoint streams real-time events to connected clients. The `EventBus` fans out to all subscribers via non-blocking sends with a 16-event per-client buffer. Events include `session:update`, `task:update`, and `progress` types. A 30-second keepalive heartbeat prevents connection timeouts. Slow clients that fall behind have events dropped rather than blocking the pipeline.

### Web Dashboard

The embedded SvelteKit SPA is served from `GET /` via `SPAHandler`. Static assets under `_app/` (content-hashed filenames) receive long-lived cache headers. All other paths serve `index.html` for client-side routing.

## Drift Reconciliation

Webhooks can be missed during daemon downtime, network issues, or delivery failures. The reconciler closes this gap:

1. **On startup** (if `reconcile.on_startup` is true): compare every sync-enabled repo's Forgejo HEAD SHA against the last recorded `sync_runs.last_sha`.
2. **On interval** (if `reconcile.interval_minutes` > 0): repeat the same check periodically.

All drift-triggered syncs run through the standard Mode 3 sanitization pipeline. A global mutex serialises reconciler passes to prevent overlapping drift checks.

## Sanitization Tag Format

Each finding is replaced with a tag in staged content:

```
<REMOVED BY SENTINEL BOT: TOKEN_0 CATEGORY — reason>
```

When an operator approves or rejects via Discord reaction, `worktree/token_index.go` locates the tag by scanning staged file bytes, replaces it with the final value, and adjusts byte offsets for all subsequent tags in the same file.

**Constraint:** No `>` character is allowed in `category_reasons` values. This is validated at startup by `config/validate.go`.

### Scrub Patterns

In addition to tag-based sanitization, configurable regex scrub patterns (`sanitize.scrub_patterns`) are applied to all file content before it reaches the GitHub mirror. These provide a deterministic final safety net for known patterns (e.g., internal hostnames, address ranges) that should always be removed regardless of confidence scoring.

## Concurrency Model

| Resource | Lock Type | Scope | Location |
|----------|-----------|-------|----------|
| Forgejo worktree | `sync.RWMutex` | Per repo | `worktree/lock.go` |
| Staged file | `sync.Mutex` | Per (repo, filename) | `worktree/filemutex.go` |
| Ollama | Semaphore (size 1) | Global | `llm/semaphore.go` |
| Claude Code CLI | Semaphore (size 1) | Global | `executor/semaphore.go` |
| SQLite writes | `SetMaxOpenConns(1)` | Process | `store/db.go` |
| Drift reconciler | `sync.Mutex` | Global | `reconcile/reconcile.go` |
| SSE event bus | `sync.RWMutex` | Global | `api/eventbus.go` |

### Lock Level Requirements

Functions that call into `worktree/manager.go` must acquire the appropriate lock level:

- **Write lock:** git pull, branch create, Claude Code invocation, staging push, doc generation
- **Read lock:** diff reads for LLM analysis, PR diff fetch

### Why Ollama and Claude Code Are Serialized

- **Ollama** with `qwen2.5-coder:14b` produces non-deterministic results under concurrent requests. The semaphore of size 1 ensures serial execution.
- **Claude Code CLI** shares process-level state and cannot run concurrently. This is a tool limitation, not a design choice.

## Database Design

SQLite with WAL mode and a single-writer constraint (`SetMaxOpenConns(1)`).

### Tables

| Table | Purpose |
|-------|---------|
| `sentinel_prs` | All sentinel-opened PRs; links Forgejo PR number to Discord message |
| `sanitization_findings` | Per-layer findings from sanitization pipeline |
| `pending_resolutions` | Operator decisions on findings (pending/approved/rejected/custom/reanalyzing/superseded) |
| `approved_values` | Allowlist of values that should never be re-flagged (per repo) |
| `sync_runs` | Mode 3/4 execution records with baseline SHA tracking |
| `tasks` | Task records for audit trail (links to sessions and branches) |
| `sessions` | Nightly session records (start/end time, repo, status) |
| `sentinel_actions` | Append-only audit log (action type, repo, entity, detail JSON) |
| `confirmations` | TTL-based confirmation state for `--force` migrations |
| `pr_reviews` | PR review dedup records and migration status per repo |
| `webhook_events` | Raw webhook event log |
| `doc_state` | Documentation generation state per (repo, doc file) |

All DDL is in `store/migrations.go` and is idempotent (`CREATE TABLE IF NOT EXISTS`).

## Key Design Decisions

### Single-Writer SQLite

SQLite WAL mode allows concurrent reads but has a single write lock. `SetMaxOpenConns(1)` is intentional. The database path must never be on a network filesystem — Kubernetes uses `ReadWriteOnce` PVCs.

### Async Webhook ACK

The webhook handler returns HTTP 200 before any processing begins. This prevents Forgejo from timing out and retrying, which would cause duplicate events. The buffered queue provides back-pressure via HTTP 429 when full.

### Operator Token Isolation

`FORGEJO_OPERATOR_TOKEN` is used in exactly one code path: `forge/forgejo.go:MergePR`. It is never stored in the database. This limits the blast radius of a token compromise.

### Manual ArgoCD Sync

Sentinel has side effects on Forgejo, GitHub, and Discord. Auto-sync on a pod restart can replay nightly runs or re-trigger migrations. The ArgoCD Application manifest intentionally omits `automated: {}`.

### Layer 2 Fallback to Claude Code CLI

When Ollama times out or returns an error during Layer 2 sanitization, sentinel falls back to the Claude Code CLI via subscription (if configured). This ensures sanitization coverage even when the local LLM is temporarily unavailable, with a configurable timeout (`sanitize.layer2_timeout_seconds`).

### Sync Push Strategy

The GitHub push step uses a MixedReset followed by per-file `git add` rather than a blanket `git add -A`. This prevents workflow files and other files that were not changed in the sync diff from being accidentally dropped during the push.

### SSE Event Bus Fan-Out

Each SSE client receives a 16-event buffered channel. Events are published non-blocking: slow clients drop events rather than blocking the pipeline. A 30-second keepalive heartbeat lets the server detect disconnected clients promptly.

### Local Checkout Fast-Forward

When `sentinel.local_checkout_base` is set, sentinel fast-forwards the operator's local working clone after merging a doc-gen PR. This prevents the operator's next push from overwriting sentinel-generated documentation.

## Graceful Shutdown

On SIGTERM or SIGINT:

1. Stop the drift reconciler ticker
2. Stop cron scheduler; wait for in-flight jobs (5-minute timeout)
3. Stop accepting new webhook connections (30-second drain)
4. Close the webhook queue (workers drain remaining events)
5. Wait briefly for in-flight event processing
6. Stop the Discord bot session
7. Close the database (WAL checkpoint flushed)
