package daemon

import (
	"testing"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

func TestReplaceWorkspacesByProvider(t *testing.T) {
	current := []provider.Workspace{
		{Provider: provider.NameGitHub, Name: "gh-one"},
		{Provider: provider.NameCoder, Name: "coder-old"},
	}
	replacement := []provider.Workspace{
		{Provider: provider.NameCoder, Name: "coder-new"},
	}

	got := replaceWorkspacesByProvider(current, provider.NameCoder, replacement)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Provider != provider.NameGitHub || got[0].Name != "gh-one" {
		t.Fatalf("github workspace was not preserved: %#v", got)
	}
	if got[1].Provider != provider.NameCoder || got[1].Name != "coder-new" {
		t.Fatalf("coder replacement not applied: %#v", got)
	}
}
