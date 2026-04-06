# Development Guide

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| **Go** | 1.24+ | No CGo required; SQLite driver (`modernc.org/sqlite`) is pure Go |
| **git** | Any recent version | Required for worktree operations |
| **golangci-lint** | Latest | For `make lint` |
| **Helm** | 3.x | For `make helm-lint` and Kubernetes deployment |
| **Docker** | Any recent version | For container builds |

### Runtime Dependencies (for full functionality)

| Component | Purpose |
|-----------|---------|
| **Forgejo** | Source git forge; two tokens required (sentinel + operator) |
| **GitHub** | Mirror target; PAT with `repo` scope |
| **Discord** | Operator interface; bot with message content + reaction intents |
| **Ollama** | Local LLM; requires `qwen2.5-coder:14b` model pulled |
| **[AI_ASSISTANT] Code CLI** | Optional; enables execution of fix/feat/vulnerability/refactor tasks |

## Project Structure

```
cmd/sentinel/main.go        — Entry point: config, DB init, mode dispatch, cron, HTTP server
internal/
  config/                   — Config struct, YAML unmarshaling, env var injection, validation
  types/                    — All data models and interface contracts
  store/                    — SQLite layer; one file per table group; migrations
  webhook/                  — HTTP server, HMAC validation, buffered queue, worker pool
  forge/                    — Forgejo (Gitea SDK) + GitHub (go-github) API clients
  llm/                      — Ollama client, batcher, prompt loading, semaphore
  sanitize/                 — Three-layer sanitization pipeline
  executor/                 — [AI_ASSISTANT] Code CLI invocation via os/exec
  pipeline/                 — Mode 1 nightly orchestration
  sync/                     — Mode 3 incremental sync
  migration/                — Mode 4 full-repo migration
  reconcile/                — Drift detection and auto-bootstrap
  discord/                  — Discord bot: embeds, reactions, threads, digest, commands
  prnotify/                 — PR notifications, reaction handlers, Forgejo→Discord sync
  worktree/                 — Git worktree lifecycle, per-repo locking, token_index
  [AI_ASSISTANT]/                   — [AI_ASSISTANT] Code CLI wrapper
  docs/                     — Documentation generation and changelog
prompts/                    — LLM role prompts (Roles A through G)
fixtures/                   — Test data: webhook payloads, diffs, synthetic secrets
tools/                      — CLI test harnesses referenced by Makefile
charts/sentinel/            — Helm chart (Kubernetes deployment)
argocd/                     — ArgoCD Application manifest
```

## Local Development Setup

### 1. Clone and Build

```bash
git clone <repo-url>
cd sentinel
make build
```

### 2. Environment Variables

Create a `.env` file in the project root (never commit this):

```bash
FORGEJO_SENTINEL_TOKEN=<sentinel-account-token>
FORGEJO_OPERATOR_TOKEN=<operator-token-with-merge-perms>
DISCORD_BOT_TOKEN=Bot <your-bot-token>
GITHUB_TOKEN=<github-pat>
FORGEJO_WEBHOOK_SECRET=<shared-secret>
# Optional
ANTHROPIC_API_KEY=
SENTINEL_DB_PATH=./sentinel.db
```

Sentinel calls `godotenv.Load(".env")` at startup.

### 3. Configuration

```bash
cp config.yaml.example config.yaml
```

Edit `config.yaml` with your instance URLs, Discord channel IDs, and repo definitions. See `config.yaml.example` for the complete annotated reference.

### 4. Run

```bash
# Full daemon
make run

# Or directly
./bin/sentinel --config config.yaml
```

## Build Commands

| Command | Description |
|---------|-------------|
| `make build` | Compile `./bin/sentinel` |
| `make run` | Run daemon with `config.yaml` |
| `make dry-run REPO=<name>` | Analysis only; no PRs or GitHub pushes |
| `make docker-build` | Build Docker image tagged with git SHA |
| `make docker-push` | Push image to container registry |

## Test Commands

| Command | Description |
|---------|-------------|
| `make test` | All Go tests with `-race`, 5-minute timeout |
| `make lint` | Run `golangci-lint ./...` |
| `make helm-lint` | Validate Helm chart |

### Targeted Test Harnesses

These use CLI tools in `tools/` to exercise specific subsystems:

| Command | Description |
|---------|-------------|
| `make token-index-test` | Unit tests for token_index algorithm (fast) |
| `make sanitize-test` | Run all three sanitization layers on `fixtures/secret_file.go` |
| `make webhook-test` | Send fixture webhook payload to local server |
| `make llm-test` | Send fixture diff to Ollama, print TaskSpec JSON |
| `make discord-test` | Connect bot, post synthetic notification, verify reactions |
| `make reaction-test` | Simulate finding reactions (✅/❌/🔍/✏️) |
| `make pr-reaction-test` | Simulate PR reactions (✅/❌/💬) + Forgejo webhook sync |
| `make forgejo-sync-test` | Simulate Forgejo merge/close events, verify Discord embed |
| `make [AI_ASSISTANT]-api-test` | Send fixture to [AI_ASSISTANT] API sanitization layer, print findings |

### Mode-Specific Test Invocations

| Command | Description |
|---------|-------------|
| `make sync-dry-run REPO=<name>` | Mode 3 sync, no GitHub push |
| `make migrate REPO=<name>` | Mode 4 migration (requires Discord confirmation if repo exists) |
| `make migrate-dry-run REPO=<name>` | Scan + print findings, no GitHub push |

## Environment Variables Reference

### Required

| Variable | Description |
|----------|-------------|
| `FORGEJO_SENTINEL_TOKEN` | Read-only Forgejo service account token; no merge permission |
| `FORGEJO_OPERATOR_TOKEN` | Merge-only token; used in exactly one code path (`forge/forgejo.go:MergePR`) |
| `DISCORD_BOT_TOKEN` | Full bot token including `Bot ` prefix |
| `GITHUB_TOKEN` | GitHub PAT with `repo` scope |
| `FORGEJO_WEBHOOK_SECRET` | HMAC-SHA256 shared secret for webhook validation |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | (empty) | Enables sanitization Layer 3 ([AI_ASSISTANT] API pass) |
| `SENTINEL_DB_PATH` | `/data/db/sentinel.db` | SQLite database file path |
| `SENTINEL_INGRESS_HOST` | (empty) | If set, auto-registers Forgejo webhooks on all repos at startup |

Config resolution order: YAML file → environment variable override. See `internal/config/env.go` for the field-by-field mapping.

## Discord Bot Setup

1. Create a new application at [discord.com/developers/applications](https://discord.com/developers/applications)
2. Under **Bot**, enable privileged intents: `Message Content Intent`, `Server Members Intent`
3. Under **OAuth2 → URL Generator**, select scopes `bot` + `applications.commands` with permissions:
   - Send Messages
   - Add Reactions
   - Create Public Threads
   - Read Message History
4. Invite the bot to your server using the generated URL
5. Create three channels (e.g. `#sentinel-prs`, `#sentinel-findings`, `#sentinel-commands`)
6. Copy channel IDs and your Discord user ID into `config.yaml`

## Forgejo Account Setup

The `sentinel` service account needs per-repo permissions:
- **Read:** code, issues, pull requests
- **Write:** issues, pull requests (to open PRs and post comments)
- **No merge permission** — enforced at the Forgejo role level

The operator token (for merging) should belong to a separate account or be a personal token with merge permission.

## Kubernetes Deployment

### Build and Push

```bash
make docker-build docker-push
```

### Helm Install

```bash
helm install sentinel charts/sentinel \
  -n sentinel --create-namespace \
  -f charts/sentinel/values.yaml \
  -f values-prod.yaml
```

### Storage

Three RWO PVCs are created by the Helm chart:

| PVC | Mount | Size | Contents |
|-----|-------|------|----------|
| `sentinel-db` | `/data/db` | 5 Gi | SQLite database |
| `sentinel-workspace-forgejo` | `/data/workspace/forgejo` | 20 Gi | Forgejo git worktrees |
| `sentinel-workspace-github` | `/data/workspace/github` | 50 Gi | GitHub staging worktrees |

**Replicas must be 1.** SQLite requires exclusive file access. Never change `accessModes` to `ReadWriteMany`.

### ArgoCD

```bash
kubectl apply -f argocd/sentinel-app.yaml
```

Sync is **manual only**. Sentinel has side effects on Forgejo, GitHub, and Discord. Never enable auto-sync without an idempotency audit.

## Token Rotation

1. Generate the new token
2. Update the environment variable (or Kubernetes Secret)
3. Restart sentinel — tokens are read at startup only
4. Revoke the old token

For `FORGEJO_WEBHOOK_SECRET`: update both the Kubernetes Secret and the webhook settings in each Forgejo repo.

## Adding a New Store Table

1. Write `CREATE TABLE IF NOT EXISTS` DDL in `internal/store/migrations.go`
2. Create `internal/store/<tablename>.go` with CRUD methods
3. Declare the store interface in `internal/types/interfaces.go`
4. Wire into `store/db.go` (return from `Open()`)
5. Inject into components in `cmd/sentinel/main.go`
6. Run `make test`

## Adding a New Task Type (Mode 1)

1. Add the type string constant to `internal/types/types.go`
2. Add routing logic in `internal/pipeline/router.go`
3. If needed, implement the `TaskExecutor` interface from `internal/types/interfaces.go`
4. Add test fixtures in `fixtures/`
5. Update `config.yaml.example` `high_priority_types` comment if relevant

## Known Stubs

These exist in the codebase but are not fully wired:

- **`sanitize/layer3_claude.go`** — [AI_ASSISTANT] API client is stubbed. Layer 3 is a no-op even when `ANTHROPIC_API_KEY` is set.
- **Push event dispatch** — push events are logged but sentinel-branch-push detection path is incomplete.
- **LLM Roles E, F, G** — Thread Q&A and housekeeping PR generation are partially stubbed. Basic posting works; full back-and-forth Q&A may not.
