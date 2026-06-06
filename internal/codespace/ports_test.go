package codespace

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeGHRunner struct {
	args []string
	out  string
	err  error
}

func (f *fakeGHRunner) Run(args []string) (string, error) {
	f.args = append([]string(nil), args...)
	return f.out, f.err
}

func (f *fakeGHRunner) RunCtx(_ context.Context, args []string) (string, error) {
	return f.Run(args)
}

func (f *fakeGHRunner) RunMerged(args []string) (string, error) {
	return f.Run(args)
}

func (f *fakeGHRunner) RunInteractive(args []string) (string, error) {
	return f.Run(args)
}

func (f *fakeGHRunner) RunWithInput(args []string, input string) (string, error) {
	return f.Run(args)
}

func TestListPortsParsesAndSorts(t *testing.T) {
	runner := &fakeGHRunner{out: `[
		{"browseUrl":"https://example-8250.app.github.dev","label":"","sourcePort":8250,"visibility":"private"},
		{"browseUrl":"https://example-1455.app.github.dev","label":"codex","sourcePort":1455,"visibility":"private"}
	]`}

	ports, err := ListPorts(runner, "example-codespace")
	if err != nil {
		t.Fatalf("ListPorts: %v", err)
	}

	wantArgs := []string{
		"codespace", "ports",
		"--json", "browseUrl,label,sourcePort,visibility",
		"-c", "example-codespace",
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.args, wantArgs)
	}

	if len(ports) != 2 {
		t.Fatalf("len = %d, want 2", len(ports))
	}
	if ports[0].SourcePort != 1455 || ports[1].SourcePort != 8250 {
		t.Fatalf("ports not sorted by source port: %#v", ports)
	}
	if ports[0].BrowseURL != "https://example-1455.app.github.dev" {
		t.Errorf("browse URL = %q", ports[0].BrowseURL)
	}
}

func TestListPortsRequiresCodespaceName(t *testing.T) {
	_, err := ListPorts(&fakeGHRunner{}, "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListPortsPropagatesRunnerError(t *testing.T) {
	want := errors.New("gh failed")
	_, err := ListPorts(&fakeGHRunner{err: want}, "example")
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestBuildPortForwardArgs(t *testing.T) {
	got := BuildPortForwardArgs("expert-spoon-vwqr5wq4x73xjpj", 1455, 1455)
	want := []string{
		"codespace", "ports", "forward",
		"1455:1455",
		"-c", "expert-spoon-vwqr5wq4x73xjpj",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestPortLabel(t *testing.T) {
	cases := []struct {
		port Port
		want string
	}{
		{Port{Label: "codex", SourcePort: 1455, Visibility: "private"}, "codex (1455)"},
		{Port{SourcePort: 8250, Visibility: "private"}, "8250 (private)"},
		{Port{SourcePort: 3000}, "3000"},
	}
	for _, tc := range cases {
		if got := PortLabel(tc.port); got != tc.want {
			t.Errorf("PortLabel(%#v) = %q, want %q", tc.port, got, tc.want)
		}
	}
}
