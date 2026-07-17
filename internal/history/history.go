// Package history tracks recently used repositories so the interactive
// repo picker can sort them by recency. The history is stored as JSON
// in ~/.config/cosmonaut/history.json.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/adrg/xdg"

	"github.com/linuskendall/cosmonaut/internal/fileutil"
)

// Entry records a single repo usage.
type Entry struct {
	Repository string    `json:"repository"`
	LastUsed   time.Time `json:"lastUsed"`
}

// History tracks recently used repositories.
type History struct {
	Entries []Entry `json:"entries"`
}

func defaultPath() string {
	return filepath.Join(xdg.StateHome, "cosmonaut", "history.json")
}

// Load reads the history file. Returns empty history if missing.
func Load() *History {
	return LoadFrom(defaultPath())
}

// LoadFrom reads history from a specific path. A missing or unparseable
// file yields an empty History — the launcher should run even when
// history is corrupt.
func LoadFrom(path string) *History {
	h := &History{}
	data, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	_ = json.Unmarshal(data, h)
	return h
}

// Save writes the history file.
func (h *History) Save() error {
	return h.SaveTo(defaultPath())
}

// SaveTo writes history to a specific path.
func (h *History) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, append(data, '\n'), 0o644)
}

// Touch records a repository as just used.
func (h *History) Touch(repo string) {
	now := time.Now()
	for i, e := range h.Entries {
		if e.Repository == repo {
			h.Entries[i].LastUsed = now
			return
		}
	}
	h.Entries = append(h.Entries, Entry{Repository: repo, LastUsed: now})
}

// SortRepos reorders repos by recency. Repos not in history go to the end,
// preserving their original order.
func (h *History) SortRepos(repos []string) []string {
	lastUsed := make(map[string]time.Time)
	for _, e := range h.Entries {
		lastUsed[e.Repository] = e.LastUsed
	}

	result := make([]string, len(repos))
	copy(result, repos)

	sort.SliceStable(result, func(i, j int) bool {
		ti, oki := lastUsed[result[i]]
		tj, okj := lastUsed[result[j]]
		if oki && okj {
			return ti.After(tj)
		}
		if oki {
			return true
		}
		return false
	})

	return result
}
