package executor

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// taskSpecTemplate is the task specification fed to [AI_ASSISTANT] Code via stdin.
// [AI_ASSISTANT] Code reads from stdin when --no-interactive is set.
const taskSpecTemplate = `## Task: {{.ID}}

You are working in repo: {{.Repo}}
Branch to work on: {{.BranchName}}
Base branch: {{.BaseBranch}}

## Description
{{.Description}}

## Affected Files
{{range .AffectedFiles}}- {{.}}
{{end}}

## Acceptance Criteria
{{range .AcceptanceCriteria}}- {{.}}
{{end}}

## Scope Boundary
- Only change files listed in "Affected Files" above.
- Do NOT modify {{.BaseBranch}} directly.
- Do NOT commit to any existing branch other than {{.BranchName}}.
- Do NOT open a pull request — sentinel will open it after your push.
- Do NOT merge any branch.
- Do NOT call external APIs or services.
- Do NOT read files outside this repository's worktree.

## PR Instructions
- Title (for reference only — sentinel will set this): {{.PRTitle}}
- Commit your changes to branch {{.BranchName}}
- Push the branch to origin
- Sentinel detects your push via webhook and opens the PR

## Git Author Identity
Configure git to use:
  Name:  {{.GitName}}
  Email: {{.GitEmail}}
`

type taskTemplateData struct {
	ID                 string
	Repo               string
	BranchName         string
	BaseBranch         string
	Description        string
	AffectedFiles      []string
	AcceptanceCriteria []string
	PRTitle            string
	GitName            string
	GitEmail           string
}

// RenderTaskSpec renders the task specification string for [AI_ASSISTANT] Code stdin.
func RenderTaskSpec(spec types.TaskSpec, branch, baseBranch, prTitle string, cfg *config.Config) (string, error) {
	tmpl, err := template.New("task").Parse(taskSpecTemplate)
	if err != nil {
		return "", fmt.Errorf("parse task template: %w", err)
	}

	data := taskTemplateData{
		ID:                 spec.ID,
		Repo:               spec.AffectedFiles[0], // repo is implicit from worktree path
		BranchName:         branch,
		BaseBranch:         baseBranch,
		Description:        spec.Description,
		AffectedFiles:      spec.AffectedFiles,
		AcceptanceCriteria: spec.AcceptanceCriteria,
		PRTitle:            prTitle,
		GitName:            cfg.Sentinel.GitName,
		GitEmail:           cfg.Sentinel.GitEmail,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render task template: %w", err)
	}
	return buf.String(), nil
}

// docGenTemplate is fed to [AI_ASSISTANT] Code for documentation generation tasks.
// [AI_ASSISTANT] may READ any source file but must only CREATE or MODIFY the listed doc targets.
const docGenTemplate = `## Documentation Task: {{.ID}}

Repository: {{.Repo}}
Branch: {{.BranchName}}
Base branch: {{.BaseBranch}}

## Documentation Targets
Generate or update the following files. Create parent directories as needed.
{{range .DocTargets}}- {{.}}
{{end}}
## Source Context
{{.SourceContext}}
{{- if .ObsidianContext}}

## Domain Knowledge (from Obsidian vault)
The following notes provide additional context about the system. Use this to inform
terminology, architecture descriptions, and operational details.

{{.ObsidianContext}}
{{- end}}

## Requirements per target
- **README.md** — Project overview, purpose, quick-start, configuration reference, architecture summary, links to further docs.
- **docs/architecture.md** — Component diagram (ASCII), data flows, key design decisions, invariants, concurrency model.
- **docs/development.md** — Prerequisites, build commands, test commands, local dev setup, environment variables.
- **docs/api.md** — All HTTP endpoints or exported interfaces; request/response shapes; auth model. Omit if no public API.
- **CHANGELOG.md** — Auto-generated from git history. Use Keep a Changelog format (## [Unreleased], ## [version]).
- Any other target: write the most appropriate documentation for its name/path.

Write in Markdown. Be thorough but concise. Prefer tables for config/env references.

## Security Constraints — MUST follow exactly
The generated documentation will be published publicly. You must not expose internal
infrastructure details. Apply the following rules to every file you write:

- **IP addresses**: Never include real IP addresses. Replace with a representative placeholder
  that signals the address class, e.g. <management-cluster-ip> or <storage-vip>.
  If an example IP is needed, use an RFC 5737 documentation range (192.0.2.x, 198.51.100.x).
  CIDR notation is fine when describing address space conceptually ("a /24 management subnet")
  but must never match real production ranges.
- **Internal hostnames / FQDNs**: Never include real internal hostnames, service FQDNs, or
  Kubernetes cluster-internal DNS names. Use descriptive placeholders such as
  <internal-git-host>, <registry-host>, or <monitoring-ingress>.
- **Internal URLs**: Never include full URLs pointing to private infrastructure. Describe
  endpoints functionally ("the internal Forgejo instance", "the ArgoCD dashboard") without
  a real URL. If a URL example is required, use https://example.internal/...
- **Port numbers**: Omit specific port numbers for internal services unless the port is a
  well-known public standard (443, 80, 22). Use descriptive language instead
  ("the Prometheus scrape port", "the webhook listener port").
- **Credentials / tokens / secrets**: Never include API keys, tokens, passwords, kubeconfig
  credentials, or any secret material, even if encountered in source or config files.
- **Cluster topology details**: Avoid exact node counts, specific node names, or VLAN IDs that
  would map the internal network. Describe topology generically ("a multi-cluster environment",
  "a dedicated storage cluster") rather than enumerating specifics.
- **Environment-specific values**: When referencing config keys, show only the key name
  (e.g. forgejo.base_url) -- never the actual value read from any config or env file.

If you are unsure whether a value is sensitive, replace it with a descriptive placeholder.
When in doubt, omit it.

## Scope Boundary
- READ any file in the repository worktree to understand the codebase.
- ONLY CREATE or MODIFY the documentation targets listed above.
- Do NOT modify source code, config, or test files.
- Do NOT modify {{.BaseBranch}} directly.
- Do NOT open a pull request — sentinel will open it after your push.

## Git Instructions
- Author: {{.GitName}} <{{.GitEmail}}>
- Commit message: docs: generate/update documentation
- Push to: {{.BranchName}}
`

type docGenTemplateData struct {
	ID              string
	Repo            string
	BranchName      string
	BaseBranch      string
	DocTargets      []string
	SourceContext   string
	ObsidianContext string
	GitName         string
	GitEmail        string
}

// RenderDocGenSpec renders the documentation task specification for [AI_ASSISTANT] Code stdin.
func RenderDocGenSpec(id, repo, branch, baseBranch string, docTargets []string, sourceContext, obsidianContext string, cfg *config.Config) (string, error) {
	tmpl, err := template.New("docgen").Parse(docGenTemplate)
	if err != nil {
		return "", fmt.Errorf("parse doc-gen template: %w", err)
	}

	data := docGenTemplateData{
		ID:              id,
		Repo:            repo,
		BranchName:      branch,
		BaseBranch:      baseBranch,
		DocTargets:      docTargets,
		SourceContext:   sourceContext,
		ObsidianContext: obsidianContext,
		GitName:         cfg.Sentinel.GitName,
		GitEmail:        cfg.Sentinel.GitEmail,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render doc-gen template: %w", err)
	}
	return buf.String(), nil
}
