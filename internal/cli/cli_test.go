package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stianfro/modelctl/internal/modelctl"
)

func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OPENCODE_AUTH_CONTENT", "")
	t.Setenv("PATH", filepath.Join(home, "bin"))
	return home
}

func execute(input string, args ...string) (string, string, error) {
	var out, stderr bytes.Buffer
	cmd := New(strings.NewReader(input), &out, &stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), stderr.String(), err
}

func TestCLIRoundTrip(t *testing.T) {
	home := sandbox(t)
	project := filepath.Join(home, "project", "opencode.jsonc")
	commands := [][]string{
		{"provider", "set", "custom", "--base-url", "https://example.test/v1", "--model", "org/model", "--model", "second"},
		{"use", "custom/org/model"},
		{"current"},
	}
	for _, args := range commands {
		out, stderr, err := execute("", append(args, "--config", project, "--json")...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !json.Valid([]byte(out)) || stderr != "" || strings.Contains(out, "\x1b") {
			t.Fatalf("not clean JSON: %q / %q", out, stderr)
		}
	}
	out, _, err := execute("", "current", "--config", project)
	if err != nil || out != "custom/org/model\n" {
		t.Fatalf("current: %q, %v", out, err)
	}
	out, stderr, err := execute("fake-test-token\n", "token", "set", "custom", "--stdin", "--json", "--config", project)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out+stderr, "fake-test-token") || !json.Valid([]byte(out)) {
		t.Fatal("token leaked or invalid output")
	}
	data, err := os.ReadFile(filepath.Join(home, "data", "opencode", "auth.json"))
	if err != nil || !strings.Contains(string(data), "fake-test-token") {
		t.Fatalf("wrong auth path: %v", err)
	}
	config, err := os.ReadFile(project)
	if err != nil || strings.Contains(string(config), "fake-test-token") {
		t.Fatal("token in project config")
	}
}

func TestCommandsDoNotPromptWithoutTerminal(t *testing.T) {
	sandbox(t)
	for _, args := range [][]string{nil, {"--json"}, {"use"}, {"provider", "set"}, {"token", "set", "custom"}, {"token", "set", "custom", "--json"}, {"use", "a/b", "extra"}, {"--unknown"}} {
		out, stderr, err := execute("never-consume-this-token", args...)
		if err == nil {
			t.Fatalf("accepted %v", args)
		}
		if ExitCode(err) != 2 {
			t.Fatalf("wrong usage exit code for %v: %v", args, err)
		}
		if strings.Contains(out+stderr+err.Error(), "never-consume-this-token") {
			t.Fatal("token leaked")
		}
	}
}

func TestTokenLimits(t *testing.T) {
	sandbox(t)
	for _, token := range []string{"", "two\nlines", strings.Repeat("x", modelctl.MaxTokenSize+1)} {
		out, _, err := execute(token, "token", "set", "p", "--stdin", "--json")
		if err == nil || out != "" {
			t.Fatal("accepted invalid token")
		}
	}
}

func TestStdinReadCanBeCanceled(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readToken(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestListJSONWithFakeOpenCode(t *testing.T) {
	home := sandbox(t)
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\nprintf 'z/model\\na/model\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := execute("", "list", "--json")
	if err != nil || stderr != "" || out != "[\"a/model\",\"z/model\"]\n" {
		t.Fatalf("list: %q %q %v", out, stderr, err)
	}
}

func TestErrorsAndHelp(t *testing.T) {
	sandbox(t)
	_, _, err := execute("", "list")
	if err == nil || ExitCode(err) != 1 {
		t.Fatal("missing opencode should fail")
	}
	out, _, err := execute("", "--help")
	if err != nil || !strings.Contains(out, "--config") || !strings.Contains(out, "token") {
		t.Fatal("missing help")
	}
	if ExitCode(context.Canceled) != 130 {
		t.Fatal("wrong cancellation exit code")
	}
	_, _, err = execute("", "provider", "set", "p", "--base-url", "")
	if err == nil || ExitCode(err) != 2 {
		t.Fatal("accepted empty flag")
	}
}

func TestCommandBranding(t *testing.T) {
	sandbox(t)
	version, _, err := execute("", "--version")
	if err != nil || version != "modelctl dev\n" {
		t.Fatalf("version: %q, %v", version, err)
	}
	cmd := New(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if cmd.Name() != "modelctl" {
		t.Fatalf("command name = %q", cmd.Name())
	}
	out, _, err := execute("", "--help")
	if err != nil || !strings.Contains(out, "modelctl [command]") {
		t.Fatalf("root help: %q, %v", out, err)
	}
	out, _, err = execute("", "provider", "set", "--help")
	if err != nil || !strings.Contains(out, "modelctl provider set custom") {
		t.Fatalf("provider help: %q, %v", out, err)
	}
	m := uiFixture(t)
	if !strings.HasPrefix(m.View().Content, "modelctl\n") {
		t.Fatal("interactive title does not match the command name")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken output") }

func TestOutputErrors(t *testing.T) {
	sandbox(t)
	cmd := New(strings.NewReader(""), brokenWriter{}, &bytes.Buffer{})
	cmd.SetArgs([]string{"current", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("lost output failure")
	}
}

func uiFixture(t *testing.T) *ui {
	t.Helper()
	sandbox(t)
	s, err := modelctl.New("")
	if err != nil {
		t.Fatal(err)
	}
	s.RunModels = func(context.Context, string) ([]byte, error) { return []byte("p/first\np/second\n"), nil }
	return newUI(context.Background(), s)
}

func press(m tea.Model, code rune) tea.Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: code})
	return next
}

func TestUICancelAndPasswordMask(t *testing.T) {
	m := uiFixture(t)
	m.form(tokenScreen, []string{"Provider", "Token"}, []string{"p", "SENSITIVE-TEST-KEY"})
	if strings.Contains(m.View().Content, "SENSITIVE-TEST-KEY") {
		t.Fatal("token visible")
	}
	press(m, tea.KeyEscape)
	if m.page != menuScreen || len(m.fields) != 0 {
		t.Fatal("form not cleared")
	}
	if _, err := os.Stat(m.s.AuthPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel wrote credentials")
	}
	if _, err := os.Stat(m.s.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel wrote config")
	}
	p := passwordUI{input: newInput(true)}
	p.input.SetValue("SENSITIVE-TEST-KEY")
	if strings.Contains(p.View().Content, "SENSITIVE-TEST-KEY") {
		t.Fatal("standalone token prompt leaked")
	}
	canceled := press(p, tea.KeyEscape).(passwordUI)
	if canceled.accepted || canceled.input.Value() != "" {
		t.Fatal("cancel kept the token")
	}
	accepted := press(p, tea.KeyEnter).(passwordUI)
	if !accepted.accepted || accepted.input.Value() != "SENSITIVE-TEST-KEY" {
		t.Fatal("token prompt did not accept")
	}
	if strings.Contains(accepted.View().Content, "SENSITIVE-TEST-KEY") {
		t.Fatal("final view leaked")
	}
}

func TestUIFilteringAndSelection(t *testing.T) {
	m := uiFixture(t)
	m.page = pickerScreen
	m.models = []string{"p/first", "p/second"}
	m.search.SetValue("SECOND")
	if found := m.filtered(); len(found) != 1 || found[0] != "p/second" {
		t.Fatal(found)
	}
	press(m, tea.KeyEnter)
	c, err := m.s.Current()
	if err != nil || c.Model != "p/second" || m.page != menuScreen {
		t.Fatalf("selection: %+v, %v", c, err)
	}
}

func TestUIForms(t *testing.T) {
	m := uiFixture(t)
	m.form(providerScreen, []string{"ID", "URL", "Models", "Name", "Package"}, []string{"p", "https://example.test/v1", "a, org/b", "Test", ""})
	m.submit()
	if m.page != menuScreen {
		t.Fatal(m.notice)
	}
	m.form(modelScreen, []string{"Model"}, []string{"p/org/b"})
	m.submit()
	c, err := m.s.Current()
	if err != nil || c.Model != "p/org/b" {
		t.Fatalf("model: %+v %v", c, err)
	}
	m.form(tokenScreen, []string{"ID", "Token"}, []string{"p", "fake-token"})
	m.submit()
	if m.page != menuScreen || strings.Contains(m.View().Content, "fake-token") {
		t.Fatal("token save failed or leaked")
	}
	if _, err := os.Stat(m.s.AuthPath); err != nil {
		t.Fatal(err)
	}
}
