# Architecture

This document describes Sentinel's internal architecture, component interactions, data flows, and key design decisions.

## System Overview

Sentinel is a single Go binary that orchestrates the software development lifecycle across Forgejo (source of truth), GitHub (sanitized public mirror), Ollama (local LLM), and Discord (operator interface).

```
                          ┌─────────────────────────┐
                          │       Discord Bot        │
                          │  (embeds, reactions,     │
                          │   threads, /commands)    │
                          └────────┬────────────────┘
                                   │
                                   │ operator reactions
                                   │ notifications
                                   ▼
┌──────────┐  webhook   ┌──────────────────────────┐   push     ┌──────────┐
│          │ ─────────► │        Sentinel           │ ────────► │          │
│  Forgejo │            │                           │           │  GitHub  │
│  (git)   │ ◄───────── │  ┌───────┐ ┌───────────┐ │           │ (mirror) │
│          │   PRs      │  │ SQLite │ │ Worktree  │ │           │          │
└──────────┘            │  │  (DB)  │ │  Manager  │ │           └──────────┘
                        │  └───────┘ └───────────┘ │
                        │                           │
                        │  ┌───────┐ ┌───────────┐ │
                        │  │Ollama │ │[AI_ASSISTANT] Code│ │
                        │  │(LLM)  │ │  (CLI)    │ │
                        │  └───────┘ └───────────┘ │
                        └──────────────────────────┘
```

## Component Map

### Entry Point

`cmd/sentinel/main.go` — loads config, initialises all subsystems, wires dependencies, dispatches by mode.

### Core Packages

| Package | Path | Responsibility |
|---------|------|----------------|
| **config** | `internal/config/` | YAML loading, env var injection, startup validation |
| **types** | `internal/types/` | All data models and interface contracts |
| **store** | `internal/store/` | SQLite layer; one file per table group; idempotent DDL migrations |
| **webhook** | `internal/webhook/` | HTTP server, HMAC validation, buffered queue, async worker pool |
| **forge** | `internal/forge/` | Forgejo (Gitea SDK) and GitHub (go-github) API clients |
| **llm** | `internal/llm/` | Ollama client, multi-call batcher, prompt loading, semaphore |
| **sanitize** | `internal/sanitize/` | Three-layer sanitization pipeline |
| **executor** | `internal/executor/` | [AI_ASSISTANT] Code CLI invocation via `os/exec` |
| **pipeline** | `internal/pipeline/` | Mode 1 nightly orchestration (preflight, routing, dependency resolution) |
| **sync** | `internal/sync/` | Mode 3 incremental sync (Forgejo → sanitize → GitHub) |
| **migration** | `internal/migration/` | Mode 4 full-repo migration with Discord confirmation |
| **reconcile** | `internal/reconcile/` | Drift detection and auto-bootstrap for GitHub mirrors |
| **discord** | `internal/discord/` | Bot lifecycle, embeds, reactions, threads, digest, slash commands |
| **prnotify** | `internal/prnotify/` | PR notifications, reaction handlers, Forgejo→Discord sync |
| **worktree** | `internal/worktree/` | Git worktree lifecycle, per-repo locking, token_index resolution, GitHub push |
| **[AI_ASSISTANT]** | `internal/[AI_ASSISTANT]/` | [AI_ASSISTANT] Code CLI wrapper (used for sanitization Layer 3) |
| **docs** | `internal/docs/` | Documentation generation and changelog management |

### Supporting Files

| Path | Purpose |
|------|---------|
| `prompts/` | LLM role prompts (Roles A through G), loaded at startup |
| `fixtures/` | Test data: webhook payloads, diffs, synthetic secret files |
| `tools/` | CLI test harnesses referenced by Makefile targets |
| `charts/sentinel/` | Helm chart for Kubernetes deployment |
| `argocd/sentinel-app.yaml` | ArgoCD Application manifest (manual sync) |

## Data Flow: Mode 1 — Nightly Pipeline

```
Cron trigger (or --mode nightly)
    │
    ▼
Pre-flight checks per repo:
  - Excluded?
  - Active dev within skip window?
  - PR flood threshold exceeded?
  - Pending migration?
    │ (pass)
    ▼
Diff Forgejo HEAD vs last recorded SHA
    │
    ▼
Partition diffs into LLM-sized batches
    │
    ▼
Ollama Role A (Analyst): identify tasks
    │
    ▼
Router: task type + complexity → executor
  - [AI_ASSISTANT] Code CLI: fix, feat, vulnerability, refactor
  - LLM (Ollama): docs, dependency-update
    │
    ▼
Executor creates branch, commits changes
    │
    ▼
Open PR on Forgejo
    │
    ▼
Post Discord notification with reaction controls
    │
    ▼
Post nightly digest summary
```

## Data Flow: Mode 2 — PR Review (Webhook)

```
Forgejo push webhook (pull_request event)
    │
    ▼
HTTP handler: HMAC validate → parse → enqueue → ACK (200)
    │
    ▼
Worker pool picks up event
    │
    ▼
Ollama Role B (Reviewer): analyse PR diff
    │
    ▼
ReviewResult: verdict + per-file notes + security assessment
    │
    ▼
Post review comments on Forgejo PR
    │
    ▼
Discord notification (high priority PRs get @here mention)
    │
    ▼
Optional: open housekeeping companion PR if files need cleanup
```

## Data Flow: Mode 3 — Incremental Sync

```
Push webhook on main/master (or manual trigger, or drift reconciler)
    │
    ▼
Diff changed files since last sync SHA
    │
    ▼
For each file:
  Load skip zones (approved values)
    │
    ▼
  Layer 1: gitleaks + regex patterns
    ≥ 0.9 confidence → auto-redact (tag inserted)
    < 0.9 → pass to Layer 2
    │
    ▼
  Layer 2: Ollama Role D (semantic analysis)
    Any finding → pending operator review
    │
    ▼
  Layer 3: [AI_ASSISTANT] API (optional)
    Additional semantic safety-net
    │
    ▼
  staging.go: assign TOKEN_N indices, build tagged content
    │
    ▼
Push sanitized content to GitHub mirror
    │
    ▼
Post findings to Discord findings channel
```

## Data Flow: Mode 4 — Full Migration

```
--mode migrate --repo <name> [--force]
    │
    ▼
If --force and target exists: Discord confirmation (TTL-based)
    │
    ▼
Scan all files in Forgejo repo
    │
    ▼
Run full sanitization pipeline (same 3 layers)
    │
    ▼
Push all sanitized files to GitHub mirror
    │
    ▼
Post summary + pending findings to Discord
```

## Webhook Processing Architecture

```
POST /webhooks/forgejo
    │
    ▼
Handler (synchronous):
  1. Read body (max 10 MB)
  2. HMAC-SHA256 validation (constant-time)
  3. Parse event type + repo name
  4. Enqueue to buffered channel
  5. Return HTTP 200 immediately
    │
    ▼
Queue (buffered channel):
  - Configurable size (default: 100)
  - Returns HTTP 429 when full (back-pressure)
    │
    ▼
Worker Pool (configurable, default: 4 workers):
  - pull_request → PR review (Mode 2) + Forgejo→Discord sync
  - push → sentinel branch detection → PR creation
         → main/master push → Mode 3 sync trigger
```

## Sanitization Tag Format

Each finding is replaced with a tag in the staged content:

```
<REMOVED BY SENTINEL BOT: TOKEN_0 CATEGORY — reason>
```

When an operator approves/rejects via Discord reaction, `worktree/token_index.go` locates the tag, replaces it with the final value, and adjusts byte offsets for all subsequent tags in the same file.

**Constraint:** No `>` character is allowed in `category_reasons` values. This is validated at startup by `config/validate.go`.

## Concurrency Model

| Resource | Lock Type | Scope | Location |
|----------|-----------|-------|----------|
| Forgejo worktree | `sync.RWMutex` | Per repo | `worktree/lock.go` |
| Staged file | `sync.Mutex` | Per (repo, filename) | `worktree/filemutex.go` |
| Ollama | Semaphore (size 1) | Global | `llm/semaphore.go` |
| [AI_ASSISTANT] Code CLI | Semaphore (size 1) | Global | `executor/semaphore.go` |
| SQLite writes | `SetMaxOpenConns(1)` | Process | `store/db.go` |
| Drift reconciler | `sync.Mutex` | Global | `reconcile/reconcile.go` |

### Lock Level Requirements

Functions that call into `worktree/manager.go` must acquire the appropriate lock:

- **Write lock:** git pull, branch create, [AI_ASSISTANT] Code invocation, staging push
- **Read lock:** diff reads for LLM analysis, PR diff fetch

### Why Ollama and [AI_ASSISTANT] Code Are Serialized

- **Ollama** with `qwen2.5-coder:14b` produces non-deterministic results under concurrent requests. The semaphore of size 1 ensures serial execution.
- **[AI_ASSISTANT] Code CLI** shares process-level state and cannot run concurrently. This is a tool limitation.

## Database Design

SQLite with WAL mode and single-writer constraint (`SetMaxOpenConns(1)`).

### Tables

| Table | Purpose |
|-------|---------|
| `sentinel_prs` | All sentinel-opened PRs; links Forgejo PR number to Discord message |
| `sanitization_findings` | Per-layer findings from sanitization pipeline |
| `pending_resolutions` | Operator decisions on findings (pending/approved/rejected/custom/reanalyzing/superseded) |
| `approved_values` | Allowlist of values that should never be re-flagged (per repo) |
| `sync_runs` | Mode 3/4 execution records with baseline SHA tracking |
| `tasks` | Task records for audit trail |
| `sentinel_actions` | Append-only audit log (action type, repo, entity, detail JSON) |
| `confirmations` | TTL-based confirmation state for `--force` migrations |
| `pr_reviews` | PR review dedup records + migration status per repo |
| `webhook_events` | Raw webhook event log |

All DDL is in `store/migrations.go` and is idempotent (`CREATE TABLE IF NOT EXISTS`).

## Key Design Decisions

### Single-Writer SQLite

SQLite's WAL mode allows concurrent reads but has a single write lock. `SetMaxOpenConns(1)` is intentional. The database path must never be on a network filesystem — Kubernetes uses `ReadWriteOnce` PVCs.

### Async Webhook ACK

The webhook handler returns HTTP 200 before any processing begins. This prevents Forgejo from timing out and retrying, which would cause duplicate events. The buffered queue provides back-pressure via HTTP 429 when full.

### Operator Token Isolation

`FORGEJO_OPERATOR_TOKEN` is used in exactly one code path: `forge/forgejo.go:MergePR`. It is never stored in the database. This limits the blast radius of a token compromise.

### Manual ArgoCD Sync

Sentinel has side effects on Forgejo, GitHub, and Discord. Auto-sync on pod restart can replay nightly runs or re-trigger migrations. The ArgoCD Application manifest intentionally omits `automated: {}`.

### Drift Reconciliation

While Mode 3 sync is triggered by Forgejo push webhooks, webhooks can be missed (daemon downtime, network issues, delivery failures). The reconciler catches drift by comparing Forgejo HEAD SHA against the last recorded sync SHA, both at startup and on a configurable periodic interval.

### Auto-Bootstrap

On daemon startup, sentinel checks every sync-enabled repo. If a GitHub mirror doesn't exist or is empty, Mode 4 migration runs automatically. This eliminates manual setup for new repositories.

## Graceful Shutdown

On SIGTERM/SIGINT:

1. Stop the drift reconciler ticker
2. Stop cron scheduler; wait for in-flight jobs (5-minute timeout)
3. Stop accepting new webhook connections (30-second drain)
4. Close the webhook queue (workers drain remaining events)
5. Stop the Discord bot session
6. Close the database (WAL checkpoint flushed)
