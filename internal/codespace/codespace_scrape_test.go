package codespace

import "testing"

// TestCodespaceNameScrape locks in the interactive-create name scrape: gh
// echoes repo/branch strings in its prompts, and the scrape must find the
// real petname+hash codespace name (last match), never a hyphenated word
// like a branch name.
func TestCodespaceNameScrape(t *testing.T) {
	combined := `? Choose repository: acme/data-pipeline
? Choose branch: fix-timeout-handling
? Choose machine type: basicLinux32gb
✓ Codespaces usage for this repository is paid for by acme
expert-spoon-vwqr5wq4x73xjpj
`
	matches := codespaceNameRe.FindAllString(combined, -1)
	if len(matches) == 0 {
		t.Fatal("no codespace name found")
	}
	if got := matches[len(matches)-1]; got != "expert-spoon-vwqr5wq4x73xjpj" {
		t.Errorf("scraped %q, want expert-spoon-vwqr5wq4x73xjpj", got)
	}
	// The branch name must not match the pattern at all.
	for _, m := range matches {
		if m == "fix-timeout-handling" || m == "data-pipeline" {
			t.Errorf("pattern matched non-codespace string %q", m)
		}
	}
}
