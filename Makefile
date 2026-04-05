.PHONY: build run dry-run webhook-test sync-dry-run llm-test migrate \
        migrate-dry-run sanitize-test [AI_ASSISTANT]-api-test discord-test \
        reaction-test pr-reaction-test token-index-test forgejo-sync-test \
        install test lint helm-lint docker-build docker-push

# Compile sentinel binary to ./bin/sentinel
build:
	go build -o bin/sentinel ./cmd/sentinel

# Run sentinel with full schedule (nightly cron + webhook server + Discord bot)
run:
	./bin/sentinel --config config.yaml

# Run analysis only: no PRs, no GitHub pushes; output findings to Discord
dry-run:
	./bin/sentinel --config config.yaml --dry-run --repo $(REPO)

# Send synthetic Forgejo pull_request and push webhook payloads to local server
webhook-test:
	go run ./tools/webhook-test --url http://localhost:8080/webhooks/forgejo \
		--secret $(FORGEJO_WEBHOOK_SECRET) \
		--fixture fixtures/webhook_pr_open.json

# Run Mode 3 sanitization and log findings; do not push to GitHub
sync-dry-run:
	./bin/sentinel --config config.yaml --mode sync --repo $(REPO) --dry-run

# Send fixture diff to Ollama and print returned TaskSpec JSON
llm-test:
	go run ./tools/llm-test --fixture fixtures/diff_small.patch --config config.yaml

# Trigger Mode 4 migration for a repo
migrate:
	./bin/sentinel --config config.yaml --mode migrate --repo $(REPO)

# Run full sanitization pass on all repo files; print findings report; no GitHub push
migrate-dry-run:
	./bin/sentinel --config config.yaml --mode migrate --repo $(REPO) --dry-run

# Run all three sanitization layers on fixture files; print findings
sanitize-test:
	go run ./tools/sanitize-test --fixture fixtures/secret_file.go --config config.yaml

# Send fixture file chunk to [AI_ASSISTANT] API sanitization endpoint; print findings
[AI_ASSISTANT]-api-test:
	go run ./tools/[AI_ASSISTANT]-api-test --fixture fixtures/secret_file.go --config config.yaml

# Connect Discord bot; post synthetic finding + PR notification; verify reactions fire
discord-test:
	go run ./tools/discord-test --config config.yaml

# Simulate all four finding reactions (✅/❌/🔍/✏️) against a test finding in DB
reaction-test:
	go run ./tools/reaction-test --config config.yaml --type finding

# Simulate all PR reactions (✅/❌/💬) and Forgejo webhook merge/close; verify sync
pr-reaction-test:
	go run ./tools/reaction-test --config config.yaml --type pr

# Run unit tests for token_index algorithm only (fastest correctness check)
token-index-test:
	go test ./internal/worktree/ -run TestTokenIndex -v

# Simulate Forgejo webhook merge/close events; verify Discord embed + channel message
forgejo-sync-test:
	go run ./tools/forgejo-sync-test --config config.yaml

# Copy binary to /usr/local/bin and run Helm install in sentinel namespace
install:
	cp bin/sentinel /usr/local/bin/sentinel
	helm install sentinel charts/sentinel -n sentinel --create-namespace -f values-local.yaml

# Run all Go tests
test:
	go test ./... -race -timeout 5m

# Run golangci-lint
lint:
	golangci-lint run ./...

# Lint Helm charts
helm-lint:
	helm lint charts/sentinel

# Build Docker image
docker-build:
	docker build -t registry.andusystems.com/sentinel:$(shell git rev-parse --short HEAD) .

# Push Docker image to registry
docker-push:
	docker push registry.andusystems.com/sentinel:$(shell git rev-parse --short HEAD)
