# sentinel

Autonomous SDLC orchestration engine. Sentinel monitors Forgejo repositories, runs nightly code analysis via a local LLM, sanitizes secrets before mirroring to GitHub, and surfaces everything through Discord with operator-gated reactions.

---

## What it does

| Mode | Trigger | What happens |
|---|---|---|
| **Mode 1 — Nightly** | Cron (default 23:00) | Diffs Forgejo HEAD vs last run → LLM analysis → opens fix/feat/docs PRs on Forgejo → Discord notification |
| **Mode 2 — PR Review** | Forgejo webhook (`pull_request`) | Reviews developer PRs via LLM → posts verdict + per-file notes → optionally opens housekeeping companion PR |
| **Mode 3 — Sync** | Nightly or manual | Sanitizes changed files → pushes clean content to GitHub mirror → Discord alert on findings |
| **Mode 4 — Migration** | Manual (`--mode migrate`) | One-time full-repo scan → sanitizes all files → pushes to GitHub mirror → Discord confirmation flow |

Operator decisions (merge, close, approve finding, reject finding) happen entirely through Discord emoji reactions. Forgejo actions taken in the UI are reflected back to Discord automatically via webhooks.

---

## Architecture overview

```
Forgejo ──webhook──► sentinel ──► Discord
   ▲                    │
   │              ┌─────┼──────┐
   │           Ollama  SQLite  GitHub
   │           (LLM)   (DB)   (mirror)
   └── PRs ◄───────────┘
```

- **One SQLite database** — all state, audit log, pending findings, PR records
- **Two git worktrees** — `/data/workspace/forgejo/<repo>` (source of truth, cloned from Forgejo) and `/data/workspace/github/<repo>` (sanitized staging, pushed to GitHub)
- **Three-layer sanitization** — gitleaks (L1, auto-redact) → Ollama Role D (L2, operator review) → [AI_ASSISTANT] API (L3, optional final pass)
- **Per-repo RW locking** — `sync.RWMutex` per repo prevents concurrent worktree corruption
- **Webhook async ACK** — HTTP 200 returned immediately; processing happens in a worker pool
- **Operator-gated reactions** — only Discord user IDs in `operator_user_ids` can trigger Forgejo actions

---

## Prerequisites

### Infrastructure

| Component | Minimum requirement |
|---|---|
| **Forgejo** | A running instance. Two tokens: `sentinel` service account (read/write PRs, no merge permission) and an operator token (merge only). |
| **GitHub** | Organisation or account for mirror repos. PAT with `repo` scope. |
| **Discord** | A server you control. Bot application with message content and reaction intents. Three channels: PRs, findings, commands. |
| **Ollama** | Running locally or in-cluster with `qwen2.5-coder:14b` pulled. Required for nightly analysis and sanitization Layer 2. |
| **Storage** | Local directory (dev) or Longhorn RWO PVC (Kubernetes). SQLite requires exclusive file access — never use a network filesystem or RWX volume. |

### Optional

| Component | Used for |
|---|---|
| **[AI_PROVIDER] API key** | Sanitization Layer 3 (additional safety-net semantic pass via [AI_ASSISTANT] API). Sentinel works without it. |
| **[AI_ASSISTANT] Code CLI** | Mode 1 task execution for `fix`, `feat`, `vulnerability`, `refactor` task types. Without it, only `docs` and `dependency-update` tasks execute. |

### To build

- Go 1.24+
- `git`

No CGo required. The SQLite driver (`modernc.org/sqlite`) is pure Go.

For Kubernetes deployment: `helm`, `kubectl`, ArgoCD (optional).

---

## How sanitization works

Every file that flows from Forgejo to the GitHub mirror passes through a three-layer pipeline:

```
Input file
    │
    ▼
[Skip zone pre-scan] — byte ranges of previously approved values are excluded from all layers
    │
    ▼
Layer 1: gitleaks library + regex patterns
  Confidence ≥ 0.9  →  auto-redact: tag inserted immediately, no operator needed
  Confidence < 0.9  →  pass to Layer 2
    │
    ▼
Layer 2: Ollama (Role D) — semantic analysis
  Any finding  →  pending operator review via Discord
    │
    ▼
Layer 3: [AI_ASSISTANT] API — safety-net pass (only if ANTHROPIC_API_KEY is set)
  Any new finding  →  pending operator review
    │
    ▼
staging.go: single left-to-right pass
  Assign TOKEN_N index to each finding
  Build staged content:  <REMOVED BY SENTINEL BOT: TOKEN_0 CATEGORY — reason>
  Return staged content + findings + token index map
```

**Operator reactions on pending findings (Discord findings channel):**
- ✅ — Approve the suggested replacement; tag is resolved; file pushed to GitHub
- ❌ — Reject; original value stays in Forgejo only; nothing pushed to GitHub for that value
- ✏️ — Provide a custom replacement in the Discord thread
- 🔍 — Ask Ollama to re-analyse the finding

**Approved values** are stored in the database allowlist. Future pipeline runs skip byte ranges matching previously approved values, so legitimate config values are not re-flagged.

---

## Configuration

### 1. Copy the example config

```bash
cp config.yaml.example config.yaml
```

### 2. Fill in the required fields

```yaml
forgejo:
  base_url: "https://git.yourdomain.com"

github:
  org: "your-github-org"

discord:
  guild_id: "123456789012345678"
  pr_channel_id: "..."
  findings_channel_id: "..."
  command_channel_id: "..."
  operator_user_ids:
    - "your-discord-user-id"    # right-click yourself → Copy User ID

ollama:
  host: "http://localhost:11434"
  model: "qwen2.5-coder:14b"

repos:
  - name: "myrepo"
    forgejo_path: "myorg/myrepo"
    github_path: "myorg/myrepo"
    languages: ["go"]
    focus_areas: ["security", "error-handling"]
    max_tasks_per_run: 10
    sync_enabled: true
```

### 3. Set environment variables

All secrets come from environment variables, never from the config file.

```bash
export FORGEJO_SENTINEL_TOKEN="<sentinel-account-token>"
export FORGEJO_OPERATOR_TOKEN="<operator-token-with-merge-perms>"
export DISCORD_BOT_TOKEN="Bot <your-bot-token>"
export GITHUB_TOKEN="<github-pat>"
export FORGEJO_WEBHOOK_SECRET="$(openssl rand -hex 32)"
export ANTHROPIC_API_KEY=""          # optional; enables Layer 3 sanitization
```

For local development, put these in a `.env` file — sentinel loads it automatically.

```bash
# .env (never commit this)
FORGEJO_SENTINEL_TOKEN=abc123
FORGEJO_OPERATOR_TOKEN=def456
DISCORD_BOT_TOKEN=Bot xyz...
GITHUB_TOKEN=ghp_...
FORGEJO_WEBHOOK_SECRET=...
```

**Additional optional env vars:**
- `SENTINEL_DB_PATH` — SQLite database location (default: `/data/db/sentinel.db`)
- `SENTINEL_INGRESS_HOST` — if set, sentinel auto-registers Forgejo webhooks on all watched repos at startup

---

## Full configuration reference

```yaml
sentinel:
  git_name: "Sentinel"               # Commit author name for GitHub mirror commits
  git_email: "sentinel@example.com"  # Commit author email
  forgejo_username: "sentinel"        # Forgejo service account username
  github_username: "sentinel-bot"     # GitHub bot username for mirror repos

forgejo:
  base_url: "https://git.yourdomain.com"
  # FORGEJO_SENTINEL_TOKEN — read-only; no merge permission
  # FORGEJO_OPERATOR_TOKEN — merge only; used in exactly one code path

github:
  base_url: "https://api.github.com"  # Change for GitHub Enterprise
  org: "your-github-org"
  # GITHUB_TOKEN — PAT with repo scope

discord:
  # DISCORD_BOT_TOKEN — include the "Bot " prefix
  guild_id: "..."
  pr_channel_id: "..."          # PR notifications and merge/close reactions
  findings_channel_id: "..."    # Sanitization finding alerts and approval reactions
  command_channel_id: "..."     # /sentinel commands and --force confirmation messages
  operator_user_ids:
    - "..."                     # Discord user IDs that can approve/merge/reject

pr:
  merge_strategy: "squash"      # squash | merge | rebase
  high_priority_types:          # PR types that get pinned notifications
    - "fix"
    - "feat"
    - "vulnerability"
  mention_on_security: true     # Post @here on vulnerability PRs
  mention_cooldown_minutes: 60  # Minimum minutes between @here mentions per repo
  housekeeping:
    enabled: true
    open_only_if_content: true  # Only open housekeeping PR if files actually changed

nightly:
  cron: "0 23 * * *"
  skip_if_active_dev_within_hours: 2  # Skip if non-sentinel commits in last N hours
  flood_threshold: 5                  # Max open sentinel PRs per repo before skipping

digest:
  enabled: true
  low_priority_collapse_threshold: 5  # If >N low-priority PRs open, show count not list

webhook:
  port: 8080
  event_queue_size: 100          # Buffered channel size; 429 when full
  processing_workers: 4          # Async worker pool size
  review_cooldown_minutes: 5     # Dedup window for PR review triggers

ollama:
  host: "http://localhost:11434"
  model: "qwen2.5-coder:14b"
  temperature: 0.1               # Low randomness for deterministic analysis
  context_window: 16384          # Tokens; used to partition large diffs
  response_buffer_tokens: 2048   # Reserved for LLM response; subtracted from context budget

claude_api:
  # ANTHROPIC_API_KEY
  model: "[AI_ASSISTANT]-sonnet-4-6"
  max_tokens: 8192
  rpm_limit: 50
  rate_limit_buffer_ms: 200

claude_code:
  binary_path: "/usr/local/bin/[AI_ASSISTANT]"
  flags:
    - "--output-format=json"
    - "--no-interactive"
  task_timeout_minutes: 30

worktree:
  base_path: "/data/workspace"   # Root for both Forgejo and GitHub worktrees

sanitize:
  high_confidence_threshold: 0.9   # ≥ this → auto-redact (Layer 1)
  medium_confidence_threshold: 0.6 # ≥ this → pending review (Layer 2/3)
  skip_patterns:                   # Files excluded from all sanitization layers
    - "*.test"
    - "testdata/**"
    - "fixtures/**"
    - "*.example"
  category_reasons:                # Tags used in SENTINEL BOT replacement strings
    SECRET: "secret or credential detected"
    API_KEY: "API key or token detected"
    PASSWORD: "password or passphrase detected"
    PRIVATE_KEY: "private key material detected"
    CONNECTION_STRING: "database or service connection string detected"
    INTERNAL_URL: "internal hostname or IP address detected"
  # Note: no '>' character is allowed in any reason string (validated at startup)

allowlist:
  confirmation_ttl_minutes: 10   # TTL for --force migration confirmation messages

repos:
  - name: "myrepo"               # Unique identifier used in CLI flags
    forgejo_path: "org/repo"     # org/repo on your Forgejo instance
    github_path: "org/repo"      # org/repo on GitHub (mirror destination)
    languages: ["go"]            # Passed to LLM role A as context
    focus_areas: ["security"]    # Passed to LLM role A (template variable)
    max_tasks_per_run: 10        # Max Mode 1 tasks opened per nightly run
    merge_strategy: "squash"     # Per-repo override of global merge_strategy
    sync_enabled: true           # Whether Mode 3 runs for this repo
    excluded: false              # Set true to skip this repo entirely

excluded_repos: []               # Repo names to skip globally
```

---

## Discord bot setup

1. Go to [discord.com/developers/applications](https://discord.com/developers/applications) and create a new application
2. Under **Bot**, enable these **Privileged Gateway Intents**:
   - `Message Content Intent`
   - `Server Members Intent`
3. Under **OAuth2 → URL Generator**, select scopes `bot` and `applications.commands`; select permissions:
   - `Send Messages`
   - `Add Reactions`
   - `Create Public Threads`
   - `Read Message History`
4. Use the generated URL to invite the bot to your server
5. Create three text channels in your server (e.g. `#sentinel-prs`, `#sentinel-findings`, `#sentinel-commands`)
6. Get each channel's ID (right-click channel → Copy Channel ID) and put them in `config.yaml`
7. Get your own Discord user ID (right-click yourself → Copy User ID) and add it to `operator_user_ids`

---

## Running locally

### Build

```bash
make build
# binary at ./bin/sentinel
```

### First run for a new repo (Mode 4 migration)

Run this once per repo before starting the daemon. It scans all files, sanitizes secrets, and pushes to the GitHub mirror.

```bash
./bin/sentinel --config config.yaml --mode migrate --repo myrepo
```

If the GitHub mirror repo already has content, add `--force`. Sentinel will post a confirmation message in your Discord command channel — react ✅ to proceed or ❌ to cancel. The confirmation expires after `confirmation_ttl_minutes`.

After migration, review pending findings in the Discord findings channel:
- ✅ — approve the suggested replacement
- ❌ — reject (keep original in Forgejo only; nothing pushed to GitHub)
- ✏️ — provide a custom replacement value in the thread
- 🔍 — ask Ollama to re-analyse the finding

### Start the daemon

```bash
./bin/sentinel --config config.yaml
```

This starts:
- The webhook HTTP server (port 8080)
- The Discord bot
- The nightly cron scheduler (default 23:00 daily)

### Manual mode triggers

```bash
# Run Mode 3 incremental sync for one repo (dry-run: no GitHub push)
./bin/sentinel --config config.yaml --mode sync --repo myrepo --dry-run

# Run Mode 1 nightly pipeline for one repo immediately
./bin/sentinel --config config.yaml --mode nightly --repo myrepo

# Run nightly for all repos
./bin/sentinel --config config.yaml --mode nightly
```

### Webhook registration

When `SENTINEL_INGRESS_HOST` is set, sentinel auto-registers webhooks on all configured repos at startup:

```bash
export SENTINEL_INGRESS_HOST="sentinel.yourdomain.com"
./bin/sentinel --config config.yaml
```

Or register manually in Forgejo: **Repo → Settings → Webhooks → Add Webhook**
- URL: `https://sentinel.yourdomain.com/webhooks/forgejo`
- Content type: `application/json`
- Secret: value of `FORGEJO_WEBHOOK_SECRET`
- Events: Pull requests, Pushes, Issue comments

---

## Deploying to Kubernetes

### 1. Build and push the image

```bash
make docker-build docker-push
# pushes to registry.andusystems.com/sentinel:<git-sha>
```

### 2. Create a values override file

```yaml
# values-prod.yaml — do not commit real secrets; use external-secrets or Vault
image:
  tag: "<git-sha>"

config:
  forgejo:
    baseUrl: "https://git.yourdomain.com"
  ingressHost: "sentinel.yourdomain.com"

secrets:
  forgejoSentinelToken: "..."
  forgejoOperatorToken: "..."
  discordBotToken: "..."
  githubToken: "..."
  forgejoWebhookSecret: "..."
```

Update `charts/sentinel/templates/configmap.yaml` with your Discord channel IDs and repos list, or template them through additional values.

### 3. Install with Helm

```bash
helm install sentinel charts/sentinel \
  -n sentinel --create-namespace \
  -f charts/sentinel/values.yaml \
  -f values-prod.yaml
```

### 4. ArgoCD (optional)

Update `argocd/sentinel-app.yaml` with your Forgejo repo URL, then apply:

```bash
kubectl apply -f argocd/sentinel-app.yaml
```

Sync is **manual only** — sentinel has side effects on Forgejo, GitHub, and Discord. Always review changes in the ArgoCD UI before syncing. Never enable auto-sync.

### Storage notes

The Helm chart creates three Longhorn RWO PVCs:

| PVC | Mount | Size | Contents |
|---|---|---|---|
| `sentinel-db` | `/data/db` | 5 Gi | SQLite database |
| `sentinel-workspace-forgejo` | `/data/workspace/forgejo` | 20 Gi | Forgejo git worktrees (source) |
| `sentinel-workspace-github` | `/data/workspace/github` | 50 Gi | GitHub staging worktrees (mirror) |

**Do not change `accessModes` to `ReadWriteMany`.** SQLite requires exclusive file access. Multiple pods or nodes mounting the same volume simultaneously will corrupt the database and worktree state. The chart enforces `replicas: 1`.

---

## Makefile targets

```
make build             # Compile ./bin/sentinel
make run               # Run daemon with config.yaml
make dry-run           # Analysis only; no PRs or GitHub pushes (set REPO=name)
make test              # Run all Go tests with -race
make lint              # Run golangci-lint
make helm-lint         # Lint Helm chart
make docker-build      # Build Docker image tagged with git SHA
make docker-push       # Push image to registry

# Targeted test harnesses
make token-index-test  # Unit tests for token_index algorithm only (fast)
make sanitize-test     # Run all three sanitization layers on fixtures/secret_file.go
make webhook-test      # Send fixture webhook payload to local server
make llm-test          # Send fixture diff to Ollama, print TaskSpec JSON
make discord-test      # Connect bot, post synthetic notification, verify reactions
make reaction-test     # Simulate finding reactions (✅/❌/🔍/✏️)
make pr-reaction-test  # Simulate PR reactions (✅/❌/💬) + Forgejo webhook sync
make forgejo-sync-test # Simulate Forgejo merge/close events, verify Discord embed

# Mode-specific invocations
make migrate           # Run Mode 4 for REPO (requires Discord confirmation if repo exists)
make migrate-dry-run   # Scan all files, print findings, no GitHub push
make sync-dry-run      # Mode 3 sync, no GitHub push
make [AI_ASSISTANT]-api-test   # Send fixture to [AI_ASSISTANT] API sanitization layer, print findings

# Install (local or Kubernetes)
make install           # Copy binary to /usr/local/bin + helm install in sentinel namespace
```

---

## Forgejo account setup

The `sentinel` service account needs these permissions on each watched repo:
- Read: code, issues, pull requests
- Write: issues, pull requests (to open PRs and post comments)
- **No merge permission** — enforced at the Forgejo role level

The operator token (used only for merging) should belong to a separate account or be a personal token with merge permission. It is used in exactly one code path (`forge/forgejo.go:MergePR`) and never stored in the database.

---

## Token rotation

When rotating any token:

1. Generate the new token
2. Update the environment variable (or Kubernetes Secret)
3. Restart sentinel — tokens are read at startup only
4. Revoke the old token

For `FORGEJO_WEBHOOK_SECRET`: update both the Kubernetes Secret and the webhook settings in each Forgejo repo (Repo → Settings → Webhooks → Edit).

---

## LLM roles

Sentinel uses seven distinct LLM roles, each with its own system prompt in `prompts/`:

| Role | Prompt file | Purpose | Called by |
|---|---|---|---|
| A — Analyst | `role_a_analyst.md` | Nightly code analysis; identifies tasks | Mode 1 nightly pipeline |
| B — Reviewer | `role_b_reviewer.md` | Reviews developer PRs; produces verdict + per-file notes | Mode 2 webhook handler |
| C — Prose | `role_c_prose.md` | General prose generation (PR bodies, summaries) | Various |
| D — Sanitize | `role_d_sanitize.md` | Semantic detection of secrets in file content | Layer 2 of sanitization pipeline |
| E — Finding thread | `role_e_finding_thread.md` | Answers questions about a specific finding in Discord | Discord finding thread |
| F — PR thread | `role_f_pr_thread.md` | Answers questions about a PR in Discord | Discord PR thread |
| G — Housekeeping | `role_g_housekeeping.md` | Generates PR body for housekeeping companion PRs | Mode 1 housekeeping path |

Large diffs are partitioned by `llm/batcher.go` to fit within `context_window - response_buffer_tokens`. Each partition is a separate Ollama call; results are merged before returning.

---

## Mode 1 nightly pipeline — pre-flight checks

Before running analysis on any repo, the nightly pipeline checks:

1. **Excluded** — `repos[].excluded: true` → skip
2. **Active development** — non-sentinel commits within `skip_if_active_dev_within_hours` → skip (sentinel does not compete with active development)
3. **PR flood** — open sentinel PRs ≥ `flood_threshold` → skip (avoids overwhelming the review queue)
4. **Pending migration** — Mode 4 migration never completed → skip (repo not safe to analyse yet)

If all checks pass, it proceeds to diff analysis and task generation.

---

## Security model

- **Token isolation**: All secrets are environment variables. The sentinel token has no merge permission. The operator token (`FORGEJO_OPERATOR_TOKEN`) is used in exactly one code path.
- **Webhook validation**: HMAC-SHA256 with constant-time compare (`hmac.Equal`). Invalid signatures return HTTP 400.
- **Operator gating**: All significant actions (merge, close, approve finding, force migration) require a Discord emoji reaction from a user ID in `operator_user_ids`. Non-operator reactions are silently ignored.
- **No secrets in database**: Tokens are read at startup and never persisted.
- **Sanitization skip zones**: Previously approved values are stored as byte ranges; all pipeline layers skip these ranges so legitimate config values are not re-flagged indefinitely.
- **Non-root container**: Docker image runs as user `sentinel:sentinel`.

---

## Observability

Sentinel logs to stdout in structured `slog` text format. In Kubernetes, collect with your existing log aggregator.

Key log events to watch:

| Event | What it means |
|---|---|
| `mode1 nightly pipeline start/complete` | Mode 1 ran for a repo |
| `mode3 sync start/complete` | Incremental sync ran |
| `mode4 migration start/complete` | Initial migration ran |
| `token_index resolution error` | A sanitization tag could not be located in the staged file — Discord alert also sent |
| `ollama chat error` | LLM unavailable — nightly analysis skipped for that batch |
| `webhook server error` | HTTP server failed — pod likely needs restart |
| `cron did not stop within 5 minutes` | Nightly job stalled during graceful shutdown |

Health endpoints (on webhook port):
- `GET /health` — liveness
- `GET /ready` — readiness

All significant actions are also written to the `sentinel_actions` table in SQLite for audit purposes.

---

## Graceful shutdown

On SIGTERM/SIGINT, sentinel:

1. Stops accepting new webhook connections (30-second drain)
2. Waits for in-flight cron jobs to finish (5-minute timeout)
3. Closes the webhook queue (workers drain remaining events)
4. Stops the Discord bot session
5. Closes the database (WAL checkpoint is flushed)
