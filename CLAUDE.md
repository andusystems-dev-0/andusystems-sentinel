# CLAUDE.md — Sentinel Developer Reference

This file is for Claude Code. It describes build commands, architecture invariants, critical design patterns, and where to find things in the codebase.

---

## Build & Test Commands

```bash
make build             # Compile ./bin/sentinel (no CGo required)
make run               # Run daemon: webhook server + Discord bot + nightly cron
make test              # All Go tests with -race, 5 minute timeout
make lint              # golangci-lint ./...
make helm-lint         # Helm chart validation

# Targeted tests (fast feedback loops)
make token-index-test  # Unit tests for token_index algorithm only
make sanitize-test     # Run all three sanitization layers on fixtures/secret_file.go
make webhook-test      # Send fixture webhook payload to local server (port 8080)
make llm-test          # Send fixtures/diff_small.patch to Ollama, print TaskSpec JSON
make discord-test      # Connect bot, post synthetic notification, verify reactions
make reaction-test     # Simulate finding reactions (✅/❌/🔍/✏️)
make pr-reaction-test  # Simulate PR reactions (✅/❌/💬) + Forgejo webhook sync
make forgejo-sync-test # Simulate Forgejo merge/close events, verify Discord embed

# Mode-specific invocations
make dry-run REPO=myrepo          # Analysis only, no PRs or GitHub pushes
make sync-dry-run REPO=myrepo     # Mode 3 sync, no GitHub push
make migrate REPO=myrepo          # Mode 4 migration (requires Discord confirmation if --force)
make migrate-dry-run REPO=myrepo  # Scan + print findings, no GitHub push

# Docker
make docker-build      # Builds registry.andusystems.com/sentinel:<git-sha>
make docker-push       # Pushes to registry
```

**Run modes:**

```bash
./bin/sentinel --config config.yaml                        # full daemon
./bin/sentinel --config config.yaml --mode nightly --repo myrepo
./bin/sentinel --config config.yaml --mode sync   --repo myrepo --dry-run
./bin/sentinel --config config.yaml --mode migrate --repo myrepo --force
```

---

## Module & Go Version

```
module github.com/andusystems/sentinel
go 1.24.0
```

No CGo. SQLite driver is `modernc.org/sqlite` (pure Go). Docker multi-stage build works from a bare Go alpine image.

---

## Directory Map

```
cmd/sentinel/main.go        — entry point: config load, DB init, mode dispatch, cron, HTTP server
internal/
  config/                   — Config struct, YAML unmarshaling, env var injection, validation
  types/                    — All data models (SentinelPR, SanitizationFinding, TaskSpec, …) + all interface contracts
  store/                    — SQLite layer; one file per table group; migrations.go holds DDL
  webhook/                  — HTTP server, HMAC validation, buffered queue, async worker pool
  forge/                    — Forgejo (Gitea SDK) + GitHub (go-github) API clients
  llm/                      — Ollama client, multi-call batcher, prompt loading, semaphore
  sanitize/                 — 3-layer sanitization pipeline
  executor/                 — Claude Code CLI invocation via os/exec
  pipeline/                 — Mode 1 nightly orchestration
  sync/                     — Mode 3 incremental sync
  migration/                — Mode 4 full-repo migration
  discord/                  — Discord bot: embeds, reactions, threads, digest, /sentinel commands
  prnotify/                 — PR notifications, reaction handlers, Forgejo→Discord sync
  worktree/                 — Git worktree lifecycle, per-repo locking, token_index resolution, GitHub push
prompts/                    — LLM role prompts (role_a_analyst.md through role_g_housekeeping.md)
fixtures/                   — Test data: webhook payload, small/large diffs, synthetic secret file
charts/sentinel/            — Helm chart (Kubernetes deployment)
argocd/sentinel-app.yaml    — ArgoCD Application (manual sync only)
tools/                      — CLI test harnesses referenced by Makefile targets
```

---

## Critical Architecture Invariants

These must not be broken without understanding the downstream effects.

### 1. Single-writer SQLite

`db.SetMaxOpenConns(1)` is intentional. WAL mode allows concurrent reads but SQLite has one write lock. Never change this. The DB path is never on a network filesystem — `deployment.yaml` uses `ReadWriteOnce` PVCs for this reason.

### 2. Webhook ACK must be synchronous and immediate

`webhook/handler.go` returns HTTP 200 before any processing. Processing happens in the worker pool (`webhook/processor.go`). If handler.go blocks, Forgejo may time out and retry, causing duplicate events. The queue backs up with a 429 when full — this is the only legitimate back-pressure signal.

### 3. Operator token is used in exactly one code path

`FORGEJO_OPERATOR_TOKEN` flows through `config.ForgejoOperatorToken` → `forge/forgejo.go:MergePR`. It is never stored in the database. Do not use it anywhere else. The sentinel token has no merge permission.

### 4. No `>` characters in `category_reasons` values

The sanitization tag format is `<REMOVED BY SENTINEL BOT: CATEGORY — reason>`. If a reason string contains `>`, the token_index scanner breaks. `config/validate.go` enforces this at startup. Do not remove this validation.

### 5. Concurrent Ollama calls are serialized

`llm/semaphore.go` holds a semaphore of size 1. Ollama with `qwen2.5-coder:14b` cannot safely handle concurrent requests — responses become deterministic only in serial. Do not increase this limit without load-testing Ollama separately.

### 6. Claude Code CLI calls are serialized

`executor/semaphore.go` also has size 1. Claude Code CLI shares process-level state and cannot run concurrently. This is a tool limitation, not a design choice.

### 7. Per-repo worktree locking must be respected

`worktree/lock.go` provides `sync.RWMutex` per repo. Any function that calls into `worktree/manager.go` must acquire the appropriate lock level:
- **Write lock**: git pull, branch create, Claude Code invocation, staging push
- **Read lock**: diff reads for LLM analysis, PR diff fetch

Failing to acquire a write lock before git operations corrupts the worktree.

### 8. token_index algorithm is offset-sensitive

`worktree/token_index.go` scans staged content for `<REMOVED BY SENTINEL BOT: TOKEN_{N} ...>` tags. When a finding is approved/rejected, the tag is replaced with the final value. Because replacement changes byte offsets, the algorithm adjusts all subsequent tag positions. If you modify `sanitize/staging.go` (which assigns token indices) or `worktree/token_index.go` (which resolves them), run `make token-index-test` and verify offset correctness for multi-finding files.

### 9. ArgoCD sync must remain MANUAL

`argocd/sentinel-app.yaml` has `automated: {}` intentionally absent. Sentinel has side effects on Forgejo, GitHub, and Discord. Auto-sync on a sentinel pod restart can replay nightly runs or re-trigger migrations. Never add auto-sync without an idempotency audit.

---

## Configuration & Environment

All secrets are environment variables. The config file has no secrets.

**Required env vars:**
- `FORGEJO_SENTINEL_TOKEN` — read-only Forgejo service account
- `FORGEJO_OPERATOR_TOKEN` — merge-only; used in exactly one code path
- `DISCORD_BOT_TOKEN` — raw bot token (no `Bot ` prefix; `discord/bot.go` prepends it)
- `GITHUB_TOKEN` — PAT with `repo` scope
- `FORGEJO_WEBHOOK_SECRET` — HMAC-SHA256 shared secret

**Optional env vars:**
- `ANTHROPIC_API_KEY` — enables sanitization Layer 3 (Claude API)
- `SENTINEL_DB_PATH` — SQLite path (default: `/data/db/sentinel.db`)
- `SENTINEL_INGRESS_HOST` — if set, auto-registers Forgejo webhooks at startup

Local dev: put secrets in `.env` (never commit). `main.go` calls `godotenv.Load()` at startup.

Config resolution order: YAML file → env var override. See `internal/config/env.go` for the field-by-field mapping.

---

## Data Models (internal/types/types.go)

Key types:

| Type | Description |
|------|-------------|
| `SentinelPR` | A sentinel-created PR on Forgejo. Links to Discord message/thread IDs. |
| `SanitizationFinding` | One sensitive-value match from any layer. Has `TokenIndex` for tag-based resolution. |
| `PendingResolution` | Operator decision state for a finding: pending/approved/rejected/custom_replaced/reanalyzing/superseded. |
| `SkipZone` | Byte range within a file that has an approved value (all layers skip it). |
| `TaskSpec` | LLM-identified code improvement task. Type + Priority (1–5) + Complexity + AffectedFiles. |
| `ReviewResult` | LLM PR review verdict. Verdict is `APPROVE | REQUEST_CHANGES | COMMENT`. |
| `NightlyDigest` | Summary struct posted to Discord after Mode 1 run. |
| `ForgejoEvent` | Incoming webhook event (type, repo, raw payload). |

All interfaces are in `internal/types/interfaces.go`. When adding a new subsystem, define its interface there first.

---

## Database Schema

Tables (all created via `store/migrations.go`, idempotent DDL):

| Table | Purpose |
|-------|---------|
| `sentinel_prs` | All sentinel-opened PRs; links Forgejo PR number ↔ Discord message |
| `sanitization_findings` | Per-layer findings from sanitization pipeline |
| `pending_resolutions` | Operator decisions on findings |
| `approved_values` | Allowlist of values that should never be flagged again (per repo) |
| `sync_runs` | Mode 3/4 execution records with baseline SHA tracking |
| `tasks` | Task records for audit trail |
| `sentinel_actions` | Append-only audit log (action type, repo, entity, detail JSON) |
| `confirmations` | TTL-based confirmation state for `--force` migrations |
| `pr_reviews` | PR review dedup records + migration status per repo |
| `webhook_events` | Raw webhook event log |

**WAL mode + single writer** is set in `store/db.go`. Never change `SetMaxOpenConns`.

---

## Sanitization Pipeline (internal/sanitize/)

Three sequential layers; each layer's findings are de-duped against skip zones from previous approvals.

```
Input file content
      │
      ▼
[SkipZone scan] — load approved values byte ranges
      │
      ▼
Layer 1: gitleaks library + regex patterns
  → High confidence (≥0.9): auto-redact → TAG inserted
  → Lower confidence: pass to Layer 2
      │
      ▼
Layer 2: Ollama Role D (semantic analysis)
  → Medium confidence (≥0.6): pending operator review
  → Low confidence: pending operator review
      │
      ▼
Layer 3: Claude API (optional, if ANTHROPIC_API_KEY set)
  → Additional semantic safety-net pass
      │
      ▼
staging.go: Single L→R pass
  → Assign TOKEN_N index to each finding
  → Build staged content with SENTINEL BOT tags
  → Return: staged content + findings + token_index map
```

Tag format: `<REMOVED BY SENTINEL BOT: TOKEN_0 CATEGORY — reason>`

When an operator approves/rejects a finding via Discord reaction, `worktree/token_index.go` scans the staged file for the tag, replaces it, adjusts offsets for subsequent tags.

---

## LLM Roles & Prompts

Prompt files live in `prompts/`. Loaded at startup by `llm/prompts.go`. Template variables are resolved per-call.

| Role | File | Called by | Input | Output |
|------|------|-----------|-------|--------|
| A — Analyst | `role_a_analyst.md` | `pipeline/nightly.go` | File diffs, focus areas | `[]TaskSpec` (JSON) |
| B — Reviewer | `role_b_reviewer.md` | `webhook/processor.go` (Mode 2) | PR diff + title | `ReviewResult` (JSON) |
| C — Prose | `role_c_prose.md` | Various | Context string | Markdown text |
| D — Sanitize | `role_d_sanitize.md` | `sanitize/layer2_llm.go` | File content chunk | `[]SanitizationFinding` (JSON) |
| E — Finding thread | `role_e_finding_thread.md` | `discord/threads.go` | Finding detail + question | Markdown answer |
| F — PR thread | `role_f_pr_thread.md` | `discord/threads.go` | PR diff + question | Markdown answer |
| G — Housekeeping | `role_g_housekeeping.md` | `pipeline/nightly.go` | Housekeeping file list | PR body Markdown |

**Batching**: Large diffs are partitioned by `llm/batcher.go` to fit within `context_window - response_buffer_tokens`. Each partition is a separate Ollama call. Results are merged.

---

## Webhook Event Processing

```
POST /webhooks/forgejo
  → hmac.Validate (constant-time compare, 400 if invalid)
  → webhook/handler.go parse JSON → ForgejoEvent
  → queue.Enqueue (buffered chan, 429 if full)
  → HTTP 200 returned immediately

webhook/processor.go worker pool (N workers, default 4):
  → pull_request event:
      → Mode 2: PR review via Ollama Role B
      → sync: Forgejo→Discord PR open/merge/close notification
  → push event:
      → detect sentinel branch push (sentinel/ prefix) → skip
      → detect developer push → Mode 3 sync trigger
  → issue_comment event: (future)
```

---

## Concurrency Model

| Resource | Lock type | Scope | Location |
|----------|-----------|-------|----------|
| Forgejo worktree | `sync.RWMutex` | Per repo | `worktree/lock.go` |
| Staged file | `sync.Mutex` | Per (repo, filename) | `worktree/filemutex.go` |
| Ollama | semaphore(1) | Global | `llm/semaphore.go` |
| Claude Code CLI | semaphore(1) | Global | `executor/semaphore.go` |
| SQLite writes | `SetMaxOpenConns(1)` | Process | `store/db.go` |

---

## Discord Channel Layout

Two channels:

| Channel | Config field | Purpose |
|---------|-------------|---------|
| **sentinel-actions** | `actions_channel_id` | Interactive: PR embeds (✅/❌/💬), migration confirmations (✅/❌), `/sentinel` commands |
| **sentinel-logs** | `logs_channel_id` | Informational: finding embeds (✅/❌/🔍/✏️), sync/migration status, errors |

## Discord Reaction Handlers

Finding reactions (logs channel):
- ✅ — Approve suggested replacement; call `token_index.Resolve`, push staged file, mark `approved`
- ❌ — Reject; do not push; mark `rejected`; original value remains in Forgejo only
- 🔍 — Re-analyze via Ollama Role D; mark `reanalyzing`, post result in thread
- ✏️ — Prompt for custom value in thread; on reply, resolve with custom string

PR reactions (actions channel):
- ✅ — Merge PR via Forgejo API using operator token (FORGEJO_OPERATOR_TOKEN)
- ❌ — Close PR without merging
- 💬 — Open/focus discussion thread; route follow-up messages to LLM Role F

Migration confirmation reactions (actions channel):
- ✅ — Confirm migration
- ❌ — Reject migration

All reaction handlers validate that the reactor's Discord user ID is in `config.discord.operator_user_ids`. Non-operator reactions are silently ignored.

---

## Adding a New Task Type (Mode 1)

1. Add the type string constant to `internal/types/types.go`
2. Add routing logic in `internal/pipeline/router.go` (type + complexity → executor)
3. If it needs a new executor (not claude_code, llm, or go), implement the `TaskExecutor` interface from `internal/types/interfaces.go`
4. Add test fixtures in `fixtures/` if the task requires file-based input
5. Update `config.yaml.example` `high_priority_types` comment if relevant

---

## Adding a New Store Table

1. Write the `CREATE TABLE IF NOT EXISTS` DDL in `internal/store/migrations.go`
2. Create a new file `internal/store/<tablename>.go` with CRUD methods
3. Declare the store interface in `internal/types/interfaces.go`
4. Wire into `store/db.go` (return from `Open()`)
5. Inject into components in `cmd/sentinel/main.go`
6. Run `make test` — SQLite migrations are idempotent; existing DBs survive

---

## Known Stubs / Incomplete Implementations

These are in the codebase but not fully wired:

- **`sanitize/layer3_claude.go`** — The Claude API client is stubbed (`claudeAPIStub`). The full Anthropic SDK integration is pending. Layer 3 is a no-op even when `ANTHROPIC_API_KEY` is set.
- **Push event dispatch** — `webhook/processor.go` logs push events but the full sentinel-branch-push detection path is not complete.
- **LLM Roles E, F, G** — Thread Q&A (E, F) and housekeeping PR generation (G) are partially stubbed in `discord/threads.go`. Basic posting works; full back-and-forth Q&A loop may not.

---

## Security Notes for Code Changes

- Never log tokens or secrets, even in debug mode. `slog` with structured fields risks accidental token inclusion in key names.
- All operator-gated actions check `config.discord.operator_user_ids` in the Discord reaction handler before taking any Forgejo action.
- HMAC validation in `webhook/hmac.go` uses `hmac.Equal` (constant-time). Do not replace with `==`.
- The `FORGEJO_OPERATOR_TOKEN` must never touch a code path other than `forge/forgejo.go:MergePR`.
- Sanitization skip patterns (`sanitize.skip_patterns` in config) are glob-matched before any layer runs. Ensure test/fixture paths are excluded to avoid false findings in CI.

---

## Health & Observability

```
GET /health   — liveness probe
GET /ready    — readiness probe
```

Logs to stdout via `slog` text format. Key events:

| Log message | Meaning |
|-------------|---------|
| `mode1 nightly pipeline start/complete` | Mode 1 ran |
| `mode3 sync start/complete` | Mode 3 ran |
| `mode4 migration start/complete` | Mode 4 ran |
| `token_index resolution error` | Finding tag not found in staged file — Discord alert also fires |
| `ollama chat error` | Ollama unreachable — batch skipped, pipeline continues |
| `webhook server error` | HTTP listener failed — pod restart likely needed |
| `cron did not stop within 5 minutes` | Nightly job stalled during graceful shutdown |

---

## Kubernetes Deployment Notes

- **Replicas: 1** always. SQLite requires exclusive PVC access. `deployment.yaml` sets this; don't change it.
- Three RWO PVCs: `sentinel-db` (5 Gi), `sentinel-workspace-forgejo` (20 Gi), `sentinel-workspace-github` (50 Gi)
- ArgoCD sync: **manual only** (`argocd/sentinel-app.yaml`)
- Image: `registry.andusystems.com/sentinel:<git-sha>` (built via `make docker-build`)
- Non-root user `sentinel:sentinel` in container
- Webhook endpoint exposed via Traefik IngressRoute with TLS (`charts/sentinel/templates/ingressroute.yaml`)
