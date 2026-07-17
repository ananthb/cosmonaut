package codespace

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Port is a forwarded Codespaces port as reported by `gh codespace ports`.
type Port struct {
	BrowseURL  string `json:"browseUrl"`
	Label      string `json:"label"`
	SourcePort int    `json:"sourcePort"`
	Visibility string `json:"visibility"`
}

// ListPorts returns the forwarded ports for a codespace.
func ListPorts(runner GHRunner, codespaceName string) ([]Port, error) {
	return ListPortsCtx(context.Background(), runner, codespaceName)
}

// ListPortsCtx is the context-aware variant of ListPorts, so callers can
// cap how long a hung `gh codespace ports` blocks them.
func ListPortsCtx(ctx context.Context, runner GHRunner, codespaceName string) ([]Port, error) {
	if codespaceName == "" {
		return nil, fmt.Errorf("codespace name is required")
	}

	out, err := runner.RunCtx(ctx, []string{
		"codespace", "ports",
		"--json", "browseUrl,label,sourcePort,visibility",
		"-c", codespaceName,
	})
	if err != nil {
		return nil, err
	}

	var ports []Port
	if err := json.Unmarshal([]byte(out), &ports); err != nil {
		return nil, fmt.Errorf("parsing codespace ports: %w", err)
	}

	sort.Slice(ports, func(i, j int) bool {
		return ports[i].SourcePort < ports[j].SourcePort
	})
	return ports, nil
}

// BuildPortForwardArgs builds args for `gh codespace ports forward`.
func BuildPortForwardArgs(codespaceName string, remotePort, localPort int) []string {
	return []string{
		"codespace", "ports", "forward",
		fmt.Sprintf("%d:%d", remotePort, localPort),
		"-c", codespaceName,
	}
}

// PortLabel returns a compact display label for a forwarded port.
func PortLabel(port Port) string {
	if port.Label != "" {
		return fmt.Sprintf("%s (%d)", port.Label, port.SourcePort)
	}
	if port.Visibility != "" {
		return fmt.Sprintf("%d (%s)", port.SourcePort, port.Visibility)
	}
	return fmt.Sprintf("%d", port.SourcePort)
}
