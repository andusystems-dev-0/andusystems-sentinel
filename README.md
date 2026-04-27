# sentinel

> Autonomous SDLC orchestration engine that monitors Forgejo repositories, sanitizes secrets, and surfaces operator decisions through Discord reactions.

## Purpose

Sentinel acts as the autonomous engineering layer between private Forgejo source repositories and their sanitized public GitHub mirrors. It runs nightly code analysis via a local LLM, detects and redacts secrets through a three-layer pipeline before any code reaches GitHub, and routes all operator decisions (merge, close, approve, reject) through Discord emoji reactions. Webhooks from Forgejo trigger real-time PR review and incremental sync; drift reconciliation closes any gaps caused by missed deliveries.

## At a glance

| Field | Value |
|---|---|
| Type | application |
| Role | service |
| Primary stack | Go + SvelteKit + SQLite + Ollama |
| Deployed by | Helm + ArgoCD (manual sync) |
| Status | production |

## Components

| Component | Purpose | Location |
|---|---|---|
| Entry point | Config load, mode dispatch, daemon wiring | `cmd/sentinel/` |
| Config | YAML loading, env var injection, startup validation | `internal/config/` |
| Store | SQLite layer with idempotent DDL migrations | `internal/store/` |
| Webhook server | HMAC-validated inbound events, buffered queue, worker pool | `internal/webhook/` |
| Forge clients | Forgejo (Gitea SDK) and GitHub (go-github) API clients | `internal/forge/` |
| LLM client | Ollama batching, prompt loading, serialized semaphore | `internal/llm/` |
| Sanitization pipeline | Three-layer secret detection and redaction | `internal/sanitize/` |
| Claude Code executor | CLI invocation for task execution and doc generation | `internal/executor/`, `internal/claude/` |
| Nightly pipeline | Mode 1: diff, analyse, route, create PRs | `internal/pipeline/` |
| Incremental sync | Mode 3: sanitize changed files, push to GitHub mirror | `internal/sync/` |
| Migration | Mode 4: full-repo scan with Discord confirmation flow | `internal/migration/` |
| Drift reconciler | Periodic Forgejo↔GitHub drift detection and auto-remediation | `internal/reconcile/` |
| Discord bot | Embeds, reactions, threads, digest, slash commands | `internal/discord/` |
| PR notifications | Forgejo→Discord sync, reaction handlers, mention tracking | `internal/prnotify/` |
| Worktree manager | Git worktree lifecycle, per-repo locking, token index resolution | `internal/worktree/` |
| Doc generator | Documentation generation, changelog management, Obsidian vault integration | `internal/docs/` |
| REST API | Read-only JSON endpoints and SSE event bus for web dashboard | `internal/api/` |

## Architecture

```
Forgejo ──webhook──► Sentinel ─────────────────────────► GitHub mirror
   ▲                    │
   │    PRs             ├─ SQLite (state + audit log)
   └────────────────────┤
                        ├─ Worktree Manager (per-repo RW locks)
 Discord ◄──────────────┤
(embeds, reactions,     ├─ Ollama (LLM, serialized)
 commands, threads)     ├─ Claude Code CLI (serialized)
                        │
                        └──► REST API + SSE ──► Web dashboard
```

A single Go binary with no external runtime dependencies beyond the services above. SQLite stores all state; two git worktree trees (one Forgejo clone, one GitHub staging area) hold file content. All operator decisions flow through Discord; Forgejo UI events are reflected back to Discord automatically. See [docs/architecture.md](docs/architecture.md) for the full component map, data flows, and concurrency model.

## Quick start

### Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.24+ | Build the binary (no CGo) |
| git | Any recent | Worktree operations |
| Forgejo | Running instance | Source forge; two service tokens required |
| GitHub PAT | `repo` scope | Mirror target authentication |
| Discord bot | Message content + reaction intents | Operator interface |
| Ollama | Running, `qwen2.5-coder:14b` pulled | Nightly analysis and Layer 2 sanitization |
| Claude Code CLI | Optional | Task execution, Layer 3 sanitization, doc generation |
| Node.js / npm | Latest LTS | Optional; only needed to rebuild the web dashboard |

### Deploy / run

```bash
# Build
make build

# Copy and edit with Forgejo URL, GitHub org, Discord channel IDs, and repo list
cp config.yaml.example config.yaml

# Set secrets in environment — never in the config file
# For local dev, put these in .env (sentinel loads it automatically)
export FORGEJO_SENTINEL_TOKEN=<sentinel-account-token>
export FORGEJO_OPERATOR_TOKEN=<operator-token-with-merge-perms>
export DISCORD_BOT_TOKEN=<raw-bot-token>
export GITHUB_TOKEN=<github-pat>
export FORGEJO_WEBHOOK_SECRET=<shared-secret>

# One-time full migration per repo (run before starting the daemon)
./bin/sentinel --config config.yaml --mode migrate --repo <repo-name>

# Start the full daemon (webhook server + Discord bot + REST API + cron)
./bin/sentinel --config config.yaml
```

See [docs/development.md](docs/development.md) for Kubernetes deployment, web dashboard builds, and targeted test harnesses.

## Configuration

| Key | Required | Description |
|---|---|---|
| `FORGEJO_SENTINEL_TOKEN` | Yes | Forgejo service account token (read/write PRs, no merge permission) |
| `FORGEJO_OPERATOR_TOKEN` | Yes | Merge-only Forgejo token; used in exactly one code path |
| `DISCORD_BOT_TOKEN` | Yes | Raw Discord bot token (code prepends `Bot ` prefix automatically) |
| `GITHUB_TOKEN` | Yes | GitHub PAT with `repo` scope |
| `FORGEJO_WEBHOOK_SECRET` | Yes | HMAC-SHA256 shared secret for webhook validation |
| `ANTHROPIC_API_KEY` | No | Enables sanitization Layer 3 Claude pass |
| `SENTINEL_DB_PATH` | No | SQLite database path (default: `/data/db/sentinel.db`) |
| `SENTINEL_INGRESS_HOST` | No | Auto-registers Forgejo webhooks on all repos at startup if set |
| `sentinel.git_name` / `sentinel.git_email` | Yes | Git identity for sentinel-authored commits |
| `sentinel.local_checkout_base` | No | Operator working clone directory; enables post-merge fast-forward |
| `forgejo.base_url` | Yes | Internal Forgejo instance base URL |
| `github.org` | Yes | GitHub organisation for mirror repositories |
| `discord.guild_id` | Yes | Discord server ID |
| `discord.actions_channel_id` | Yes | Channel for operator commands and confirmations |
| `discord.logs_channel_id` | Yes | Channel for findings, sync status, and errors |
| `discord.operator_user_ids` | Yes | Discord user IDs permitted to trigger Forgejo actions |
| `ollama.host` | Yes | Ollama API base URL |
| `worktree.base_path` | Yes | Filesystem path for git worktrees |
| `repos` | Yes | Per-repo configuration (name, paths, languages, sync settings) |

Full annotated reference: `config.yaml.example`.

## Repository layout

```
.
├── cmd/sentinel/main.go        # Entry point
├── internal/
│   ├── api/                    # REST API, SSE event bus, embedded SPA handler
│   ├── claude/                 # Claude Code CLI wrapper for sanitization
│   ├── config/                 # YAML config loading, env var injection, validation
│   ├── discord/                # Discord bot: embeds, reactions, threads, commands
│   ├── docs/                   # Doc generation, changelog management, Obsidian vault
│   ├── executor/               # Claude Code CLI invocation via os/exec
│   ├── forge/                  # Forgejo and GitHub API clients
│   ├── llm/                    # Ollama client, batcher, semaphore
│   ├── migration/              # Mode 4 full-repo migration with auto-bootstrap
│   ├── pipeline/               # Mode 1 nightly pipeline orchestration
│   ├── prnotify/               # PR notifications and Forgejo→Discord sync
│   ├── reconcile/              # Drift detection and periodic sync
│   ├── sanitize/               # Three-layer sanitization pipeline
│   ├── store/                  # SQLite layer with idempotent DDL migrations
│   ├── sync/                   # Mode 3 incremental sync
│   ├── types/                  # Data models and interface contracts
│   └── worktree/               # Git worktree lifecycle, per-repo locking, token index
├── prompts/                    # LLM role prompts (Roles A–G)
├── web/                        # SvelteKit dashboard source (embedded into Go binary)
├── charts/sentinel/            # Helm chart for Kubernetes deployment
├── argocd/sentinel-app.yaml    # ArgoCD Application manifest (manual sync only)
├── fixtures/                   # Test data: payloads, diffs, synthetic secrets
├── tools/                      # CLI test harnesses (referenced by Makefile)
└── config.yaml.example         # Annotated configuration reference
```

## Further documentation

- [Architecture](docs/architecture.md) — component diagram, data flows, concurrency model, design decisions
- [Development](docs/development.md) — local setup, build commands, test harnesses, Kubernetes deployment
- [Changelog](CHANGELOG.md) — release history
