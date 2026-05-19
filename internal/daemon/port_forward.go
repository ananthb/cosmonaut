package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

type portForwardKey struct {
	Provider   string
	Workspace  string
	Protocol   string
	RemotePort int
	LocalPort  int
}

type managedPortForward struct {
	key    portForwardKey
	cancel context.CancelFunc
	output *boundedBuffer
}

// PortForwardManager supervises long-running workspace port-forward
// processes started by the daemon.
type PortForwardManager struct {
	mu       sync.Mutex
	forwards map[portForwardKey]*managedPortForward
	lastErr  map[portForwardKey]string
}

func newPortForwardManager() *PortForwardManager {
	return &PortForwardManager{
		forwards: make(map[portForwardKey]*managedPortForward),
		lastErr:  make(map[portForwardKey]string),
	}
}

func (m *PortForwardManager) Start(providerName, workspaceName string, remotePort, localPort int) error {
	return m.StartProtocol(providerName, workspaceName, "tcp", remotePort, localPort)
}

func (m *PortForwardManager) StartProtocol(providerName, workspaceName, protocol string, remotePort, localPort int) error {
	key := portForwardKey{
		Provider:   normalizePortForwardProvider(providerName),
		Workspace:  workspaceName,
		Protocol:   normalizePortForwardProtocol(protocol),
		RemotePort: remotePort,
		LocalPort:  localPort,
	}
	if err := validatePortForwardKey(key); err != nil {
		return err
	}

	m.mu.Lock()
	if _, ok := m.forwards[key]; ok {
		m.mu.Unlock()
		return nil
	}
	if other, ok := m.localPortOwnerLocked(localPort); ok {
		m.mu.Unlock()
		return fmt.Errorf("localhost port %d is already forwarded to %s:%d", localPort, other.Workspace, other.RemotePort)
	}
	m.mu.Unlock()

	if err := ensureLocalPortAvailable(key.Protocol, localPort); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	command, args := buildPortForwardCommand(key)
	cmd := exec.CommandContext(ctx, command, args...)
	output := &boundedBuffer{limit: 16 * 1024}
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("starting port forward: %w", err)
	}

	managed := &managedPortForward{key: key, cancel: cancel, output: output}
	m.mu.Lock()
	m.forwards[key] = managed
	delete(m.lastErr, key)
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		done <- err

		detail := strings.TrimSpace(output.String())
		m.mu.Lock()
		if current := m.forwards[key]; current == managed {
			delete(m.forwards, key)
		}
		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			m.lastErr[key] = friendlyPortForwardMessage(key, err, detail)
		}
		m.mu.Unlock()

		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			log.Printf("port forward: %s", friendlyPortForwardMessage(key, err, detail))
		}
	}()

	select {
	case err := <-done:
		cancel()
		detail := strings.TrimSpace(output.String())
		if err != nil {
			return errors.New(friendlyPortForwardMessage(key, err, detail))
		}
		return fmt.Errorf("port forward for localhost:%d exited immediately", localPort)
	case <-time.After(750 * time.Millisecond):
		return nil
	}
}

func (m *PortForwardManager) Stop(providerName, workspaceName string, remotePort, localPort int) bool {
	return m.StopProtocol(providerName, workspaceName, "tcp", remotePort, localPort)
}

func (m *PortForwardManager) StopProtocol(providerName, workspaceName, protocol string, remotePort, localPort int) bool {
	key := portForwardKey{
		Provider:   normalizePortForwardProvider(providerName),
		Workspace:  workspaceName,
		Protocol:   normalizePortForwardProtocol(protocol),
		RemotePort: remotePort,
		LocalPort:  localPort,
	}

	m.mu.Lock()
	managed, ok := m.forwards[key]
	if ok {
		delete(m.forwards, key)
	}
	m.mu.Unlock()

	if !ok {
		return false
	}
	managed.cancel()
	return true
}

func (m *PortForwardManager) StopAll() {
	m.mu.Lock()
	forwards := make([]*managedPortForward, 0, len(m.forwards))
	for _, managed := range m.forwards {
		forwards = append(forwards, managed)
	}
	m.forwards = make(map[portForwardKey]*managedPortForward)
	m.mu.Unlock()

	for _, managed := range forwards {
		managed.cancel()
	}
}

func (m *PortForwardManager) IsActive(providerName, workspaceName string, remotePort, localPort int) bool {
	return m.IsActiveProtocol(providerName, workspaceName, "tcp", remotePort, localPort)
}

func (m *PortForwardManager) IsActiveProtocol(providerName, workspaceName, protocol string, remotePort, localPort int) bool {
	key := portForwardKey{
		Provider:   normalizePortForwardProvider(providerName),
		Workspace:  workspaceName,
		Protocol:   normalizePortForwardProtocol(protocol),
		RemotePort: remotePort,
		LocalPort:  localPort,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.forwards[key]
	return ok
}

func (m *PortForwardManager) localPortOwnerLocked(localPort int) (portForwardKey, bool) {
	for key := range m.forwards {
		if key.LocalPort == localPort {
			return key, true
		}
	}
	return portForwardKey{}, false
}

func validatePortForwardKey(key portForwardKey) error {
	if key.Provider == "" {
		return fmt.Errorf("workspace provider is required")
	}
	if key.Provider != provider.NameGitHub && key.Provider != provider.NameCoder {
		return fmt.Errorf("unsupported workspace provider %q", key.Provider)
	}
	if key.Workspace == "" {
		return fmt.Errorf("workspace name is required")
	}
	if key.Protocol != "tcp" && key.Protocol != "udp" {
		return fmt.Errorf("port forward protocol must be tcp or udp")
	}
	if key.RemotePort <= 0 || key.RemotePort > 65535 {
		return fmt.Errorf("remote port must be between 1 and 65535")
	}
	if key.LocalPort <= 0 || key.LocalPort > 65535 {
		return fmt.Errorf("local port must be between 1 and 65535")
	}
	return nil
}

func normalizePortForwardProvider(providerName string) string {
	if providerName == "" {
		return provider.NameGitHub
	}
	return strings.ToLower(strings.TrimSpace(providerName))
}

func normalizePortForwardProtocol(protocol string) string {
	if protocol == "" {
		return "tcp"
	}
	return strings.ToLower(strings.TrimSpace(protocol))
}

func buildPortForwardCommand(key portForwardKey) (string, []string) {
	switch key.Provider {
	case provider.NameCoder:
		flag := "--tcp"
		if key.Protocol == "udp" {
			flag = "--udp"
		}
		return "coder", []string{
			"port-forward",
			key.Workspace,
			flag,
			fmt.Sprintf("%d:%d", key.LocalPort, key.RemotePort),
		}
	default:
		return "gh", codespace.BuildPortForwardArgs(key.Workspace, key.RemotePort, key.LocalPort)
	}
}

func ensureLocalPortAvailable(protocol string, port int) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if normalizePortForwardProtocol(protocol) == "udp" {
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("localhost port %d is already in use; another process or workspace forward may already be bound to it", port)
		}
		return conn.Close()
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("localhost port %d is already in use; another process or workspace forward may already be bound to it", port)
	}
	return ln.Close()
}

func friendlyPortForwardMessage(key portForwardKey, err error, detail string) string {
	msg := strings.TrimSpace(detail)
	if msg == "" && err != nil {
		msg = err.Error()
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "address already in use"),
		strings.Contains(lower, "bind:"),
		strings.Contains(lower, "only one usage of each socket address"),
		strings.Contains(lower, "listen tcp"):
		return fmt.Sprintf("localhost port %d is already in use; another process or workspace forward may already be bound to it", key.LocalPort)
	case msg != "":
		return fmt.Sprintf("forwarding localhost:%d to %s:%d failed: %s", key.LocalPort, key.Workspace, key.RemotePort, msg)
	default:
		return fmt.Sprintf("forwarding localhost:%d to %s:%d failed", key.LocalPort, key.Workspace, key.RemotePort)
	}
}

type boundedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.limit <= 0 {
		return n, nil
	}
	if b.buf.Len()+len(p) > b.limit {
		overflow := b.buf.Len() + len(p) - b.limit
		current := b.buf.Bytes()
		if overflow >= len(current) {
			b.buf.Reset()
		} else {
			kept := append([]byte(nil), current[overflow:]...)
			b.buf.Reset()
			_, _ = b.buf.Write(kept)
		}
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
