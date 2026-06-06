package daemon

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

func TestValidatePortForwardKey(t *testing.T) {
	if err := validatePortForwardKey(portForwardKey{Provider: provider.NameGitHub, Workspace: "cs", Protocol: "tcp", RemotePort: 1455, LocalPort: 1455}); err != nil {
		t.Fatalf("valid key: %v", err)
	}
	for _, key := range []portForwardKey{
		{RemotePort: 1455, LocalPort: 1455},
		{Provider: provider.NameGitHub, Workspace: "cs", Protocol: "tcp", RemotePort: 0, LocalPort: 1455},
		{Provider: provider.NameGitHub, Workspace: "cs", Protocol: "tcp", RemotePort: 1455, LocalPort: 70000},
		{Provider: provider.NameGitHub, Workspace: "cs", Protocol: "icmp", RemotePort: 1455, LocalPort: 1455},
	} {
		if err := validatePortForwardKey(key); err == nil {
			t.Fatalf("validatePortForwardKey(%#v) expected error", key)
		}
	}
}

func TestFriendlyPortForwardMessageClassifiesBindErrors(t *testing.T) {
	key := portForwardKey{Provider: provider.NameGitHub, Workspace: "expert-spoon", Protocol: "tcp", RemotePort: 1455, LocalPort: 1455}
	msg := friendlyPortForwardMessage(key, errors.New("exit status 1"), "listen tcp 127.0.0.1:1455: bind: address already in use")
	if !strings.Contains(msg, "localhost port 1455 is already in use") {
		t.Fatalf("msg = %q", msg)
	}
	if !strings.Contains(msg, "workspace forward") {
		t.Fatalf("msg should mention workspace forwards: %q", msg)
	}
}

func TestPortForwardManagerRejectsManagedLocalPortConflict(t *testing.T) {
	manager := newPortForwardManager()
	manager.forwards[portForwardKey{Provider: provider.NameGitHub, Workspace: "one", Protocol: "tcp", RemotePort: 1455, LocalPort: 1455}] = &managedPortForward{}

	err := manager.Start(provider.NameGitHub, "two", 1455, 1455)
	if err == nil {
		t.Fatal("expected local port conflict")
	}
	if !strings.Contains(err.Error(), "already forwarded to one:1455") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureLocalPortAvailableDetectsBoundPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port
	err = ensureLocalPortAvailable("tcp", port)
	if err == nil {
		t.Fatal("expected bound port error")
	}
	if !strings.Contains(err.Error(), "localhost port "+strconv.Itoa(port)+" is already in use") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPortForwardCommandCoder(t *testing.T) {
	key := portForwardKey{Provider: provider.NameCoder, Workspace: "demo", Protocol: "tcp", RemotePort: 3000, LocalPort: 8080}
	command, args := buildPortForwardCommand(key)
	if command != "coder" {
		t.Fatalf("command = %q, want coder", command)
	}
	want := []string{"port-forward", "demo", "--tcp", "8080:3000"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestBuildPortForwardCommandGitHub(t *testing.T) {
	key := portForwardKey{Provider: provider.NameGitHub, Workspace: "expert-spoon", Protocol: "tcp", RemotePort: 1455, LocalPort: 1455}
	command, args := buildPortForwardCommand(key)
	if command != "gh" {
		t.Fatalf("command = %q, want gh", command)
	}
	want := []string{"codespace", "ports", "forward", "1455:1455", "-c", "expert-spoon"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
