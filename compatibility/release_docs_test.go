package compatibility_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var staleGoVersion = regexp.MustCompile(`(?i)\bgo\s+1\.24(?:\.0)?\b`)

func TestCurrentDocumentationRequiresGo125(t *testing.T) {
	t.Parallel()

	paths := []string{"../README.md", "../CONTRIBUTING.md"}
	err := filepath.WalkDir("../docs", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".md" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk documentation: %v", err)
	}

	for _, path := range paths {
		content := readDocumentation(t, path)
		if staleGoVersion.MatchString(content) {
			t.Errorf("%s contains a stale Go 1.24 requirement", path)
		}
	}
}

func TestReleaseDocumentationContract(t *testing.T) {
	t.Parallel()

	release := readDocumentation(t, "../docs/release.md")
	assertContainsAll(t, "release guide", release,
		"Go 1.25", "Iris 2.0", "Integration tests", "Release checklist",
		"Rollback", "iris-compatibility.md", "compatibility.md", "upgrade-to-1.0.md",
	)

	upgrade := readDocumentation(t, "../docs/upgrade-to-1.0.md")
	assertContainsAll(t, "upgrade guide", upgrade,
		"Go 1.25", "Iris v1.0.0", "schema_version", "PetalFlow-API-Version",
		"Migration", "Rollback", "MCP", "unsupported",
	)
}

func TestReleaseDocumentationLocalLinksResolve(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"../docs/README.md", "../docs/release.md", "../docs/upgrade-to-1.0.md"} {
		assertLocalLinksResolve(t, path, readDocumentation(t, path))
	}
}

func readDocumentation(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func assertContainsAll(t *testing.T, name, content string, required ...string) {
	t.Helper()
	normalized := strings.ToLower(content)
	for _, phrase := range required {
		if !strings.Contains(normalized, strings.ToLower(phrase)) {
			t.Errorf("%s is missing %q", name, phrase)
		}
	}
}

func assertLocalLinksResolve(t *testing.T, source, content string) {
	t.Helper()
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	for _, match := range linkPattern.FindAllStringSubmatch(content, -1) {
		target := strings.SplitN(match[1], "#", 2)[0]
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(source), target))
		if _, err := os.Stat(resolved); err != nil {
			t.Errorf("%s links to missing local target %q: %v", source, target, err)
		}
	}
}
