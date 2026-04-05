package pipeline

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// DependencyBump represents a detected dependency version bump.
type DependencyBump struct {
	Dep     string
	OldVer  string
	NewVer  string
	File    string
	Content []byte
}

// ParseGoDependencyBump identifies a version bump in go.mod content.
// Returns nil if no bump is detected.
func ParseGoDependencyBump(filename string, content []byte) *DependencyBump {
	if filepath.Base(filename) != "go.mod" {
		return nil
	}

	// Look for require statements: "github.com/foo/bar v1.2.3"
	re := regexp.MustCompile(`^\s+([\w./\-]+)\s+(v[\w.\-]+)`)
	for _, line := range strings.Split(string(content), "\n") {
		m := re.FindStringSubmatch(line)
		if m != nil {
			return &DependencyBump{
				Dep:    m[1],
				NewVer: m[2],
				File:   filename,
			}
		}
	}
	return nil
}

// BumpVersion updates a dependency version in a manifest file's content.
// Returns the updated content.
func BumpVersion(content []byte, dep, newVer string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		if strings.Contains(line, dep) && strings.Contains(line, "v") {
			// Replace version in this line.
			re := regexp.MustCompile(`v[\w.\-]+`)
			lines[i] = re.ReplaceAllString(line, newVer)
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("dependency %q not found in manifest", dep)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// DependencyPRTitle formats the PR title for a dependency bump.
func DependencyPRTitle(dep, newVer string) string {
	return fmt.Sprintf("chore(deps): bump %s to %s", dep, newVer)
}
