// Package docs handles documentation generation, changelog management,
// and Obsidian vault integration for sentinel-managed repos.
package docs

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ReadVaultContext reads Markdown files from the vault that are relevant to repoName
// and returns them concatenated as a context string.
// It prefers files whose names contain repoName, then falls back to top-level .md files.
// Returns an empty string (no error) if the vault path is not configured or doesn't exist.
func ReadVaultContext(vaultPath, repoName string, maxFiles int) string {
	if vaultPath == "" {
		return ""
	}
	if _, err := os.Stat(vaultPath); os.IsNotExist(err) {
		return ""
	}

	var matched, general []string

	_ = filepath.WalkDir(vaultPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		// Skip .git and .obsidian internals.
		rel, _ := filepath.Rel(vaultPath, path)
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		lower := strings.ToLower(d.Name())
		if strings.Contains(lower, strings.ToLower(repoName)) {
			matched = append(matched, path)
		} else {
			general = append(general, path)
		}
		return nil
	})

	// Prefer repo-specific files; fill remainder from general pool.
	candidates := append(matched, general...)
	if len(candidates) > maxFiles {
		candidates = candidates[:maxFiles]
	}

	var sb strings.Builder
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(vaultPath, path)
		sb.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", rel, strings.TrimSpace(string(data))))
	}
	return sb.String()
}

// WriteChangelog appends a changelog entry to the vault's per-repo changelog file.
// Path: <vaultPath>/<changelogDir>/<repoName>.md
func WriteChangelog(vaultPath, changelogDir, repoName, entry string) error {
	if vaultPath == "" {
		return nil
	}
	dir := filepath.Join(vaultPath, changelogDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir obsidian changelog dir: %w", err)
	}

	path := filepath.Join(dir, repoName+".md")

	// Ensure file has a header on first creation.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := fmt.Sprintf("# Changelog — %s\n\n", repoName)
		if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
			return fmt.Errorf("create obsidian changelog: %w", err)
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open obsidian changelog: %w", err)
	}
	defer f.Close()

	timestamp := time.Now().UTC().Format("2006-01-02")
	_, err = fmt.Fprintf(f, "\n## %s\n\n%s\n", timestamp, strings.TrimSpace(entry))
	return err
}

// WriteDocSnapshot writes a documentation file snapshot to the vault.
// Path: <vaultPath>/<docsDir>/<repoName>/<docFile>
func WriteDocSnapshot(vaultPath, docsDir, repoName, docFile string, content []byte) error {
	if vaultPath == "" {
		return nil
	}
	// Flatten nested paths: docs/architecture.md → architecture.md inside repo subdir.
	dir := filepath.Join(vaultPath, docsDir, repoName, filepath.Dir(docFile))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir obsidian doc dir: %w", err)
	}

	path := filepath.Join(vaultPath, docsDir, repoName, docFile)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write obsidian doc snapshot: %w", err)
	}
	return nil
}

// CommitVaultChanges stages all changes in the vault repo and commits them.
// This is best-effort — failures are logged but do not block the caller.
func CommitVaultChanges(vaultPath, message string) {
	if vaultPath == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(vaultPath, ".git")); os.IsNotExist(err) {
		return // not a git repo
	}

	add := exec.Command("git", "-C", vaultPath, "add", "-A")
	if out, err := add.CombinedOutput(); err != nil {
		slog.Warn("obsidian vault: git add failed", "err", err, "out", string(out))
		return
	}

	commit := exec.Command("git", "-C", vaultPath, "commit", "-m", message, "--allow-empty-message")
	if out, err := commit.CombinedOutput(); err != nil {
		// "nothing to commit" exits non-zero — treat as non-fatal.
		if !strings.Contains(string(out), "nothing to commit") {
			slog.Warn("obsidian vault: git commit failed", "err", err, "out", string(out))
		}
	}
}
