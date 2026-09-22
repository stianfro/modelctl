package ocswitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
	"golang.org/x/sys/unix"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	return &Service{ConfigPath: filepath.Join(dir, "opencode.jsonc"), AuthPath: filepath.Join(dir, "data", "auth.json"), Getenv: func(string) string { return "" }, RunModels: func(context.Context, string) ([]byte, error) { return []byte("custom/model\n"), nil }}
}

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decoded(t *testing.T, text string) map[string]any {
	t.Helper()
	data, err := hujson.Standardize([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestConfigSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	check := func(want string) {
		t.Helper()
		s, err := New("")
		if err != nil {
			t.Fatal(err)
		}
		if s.ConfigPath != want {
			t.Fatalf("config = %q, want %q", s.ConfigPath, want)
		}
		if s.AuthPath != filepath.Join(home, ".local/share/opencode/auth.json") {
			t.Fatal(s.AuthPath)
		}
	}
	dir := filepath.Join(home, ".config/opencode")
	check(filepath.Join(dir, "opencode.jsonc"))
	put(t, filepath.Join(dir, "opencode.json"), "{}")
	check(filepath.Join(dir, "opencode.json"))
	put(t, filepath.Join(dir, "opencode.jsonc"), "{}")
	check(filepath.Join(dir, "opencode.jsonc"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigPath != filepath.Join(home, "config/opencode/opencode.jsonc") || s.AuthPath != filepath.Join(home, "data/opencode/auth.json") {
		t.Fatalf("wrong XDG paths: %+v", s)
	}
	explicit := filepath.Join(home, "project/config.json")
	s, err = New(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigPath != explicit || !s.ExplicitConfig || s.AuthPath != filepath.Join(home, "data/opencode/auth.json") {
		t.Fatalf("wrong explicit paths: %+v", s)
	}
}

func TestUsePreservesConfigAndIsIdempotent(t *testing.T) {
	s := fixture(t)
	before := `{
  // Keep this comment.
  "model": /* default */ "old/model",
  "agent": {"reviewer": {"model": "other/model"}},
  "provider": {"existing": {"options": {"apiKey": "fake-secret"}}},
  "unknown": [{"keep": true}],
}`
	put(t, s.ConfigPath, before)
	if err := os.Chmod(s.ConfigPath, 0o640); err != nil {
		t.Fatal(err)
	}
	r, err := s.Use("openrouter/org/model")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed {
		t.Fatal("expected a change")
	}
	after := contents(t, s.ConfigPath)
	if !strings.Contains(after, "Keep this comment.") || !strings.Contains(after, "/* default */") {
		t.Fatal("comments lost")
	}
	want := decoded(t, before)
	want["model"] = "openrouter/org/model"
	if !reflect.DeepEqual(decoded(t, after), want) {
		t.Fatalf("unrelated data changed: %s", after)
	}
	info, _ := os.Stat(s.ConfigPath)
	if info.Mode().Perm() != 0o640 {
		t.Fatal("config permissions changed")
	}
	c, err := s.Current()
	if err != nil || c.Model != "openrouter/org/model" {
		t.Fatalf("current: %+v, %v", c, err)
	}
	r, err = s.Use(c.Model)
	if err != nil || r.Changed {
		t.Fatalf("repeat use: %+v, %v", r, err)
	}
	if contents(t, s.ConfigPath) != after {
		t.Fatal("no-op rewrote config")
	}
}

func TestMissingConfigAndStrictJSON(t *testing.T) {
	s := fixture(t)
	s.ConfigPath = filepath.Join(t.TempDir(), "new", "opencode.json")
	c, err := s.Current()
	if err != nil || c.Model != "" {
		t.Fatalf("current: %+v, %v", c, err)
	}
	if _, err := os.Stat(s.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created config")
	}
	if _, err := s.Use("custom/model"); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(contents(t, s.ConfigPath))) {
		t.Fatal(".json is not strict JSON")
	}
}

func TestBadConfigIsNotChangedOrLeaked(t *testing.T) {
	for i, text := range []string{`{"token":"SENSITIVE", broken}`, `[]`, `{"model":null}`, `{"model":"a/b","model":"c/d"}`, `{"nested":[{"a":1,"a":2}]}`} {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			s := fixture(t)
			put(t, s.ConfigPath, text)
			_, err := s.Use("custom/model")
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), "SENSITIVE") {
				t.Fatal("leaked config contents")
			}
			if contents(t, s.ConfigPath) != text {
				t.Fatal("changed bad config")
			}
		})
	}
}

func TestModelReferences(t *testing.T) {
	for _, ref := range []string{"a/b", "openrouter/org/model", "a/model~1", "a/model:latest"} {
		if err := ValidateModelRef(ref); err != nil {
			t.Errorf("%q: %v", ref, err)
		}
	}
	for _, ref := range []string{"", "model", "/model", "a/", "a//b", "a/b#high", "a/model\n", "a/model x", "a/\x1b[31m"} {
		if ValidateModelRef(ref) == nil {
			t.Errorf("accepted %q", ref)
		}
	}
}

func TestProviderCreateAndMerge(t *testing.T) {
	s := fixture(t)
	o := ProviderOptions{ID: "custom", BaseURL: "https://example.test/v1", Models: []string{"org/model~test"}, Name: "Custom"}
	if _, err := s.SetProvider(o); err != nil {
		t.Fatal(err)
	}
	d, err := loadDocument(s.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if npm, _ := d.string("provider", "custom", "npm"); npm != "@ai-sdk/openai-compatible" {
		t.Fatal(npm)
	}
	if d.get("provider", "custom", "models", "org/model~test") == nil {
		t.Fatal("model ID was treated as a path")
	}
	if err := d.set([]string{"provider", "custom", "models", "org/model~test", "limit"}, map[string]int{"context": 12345}); err != nil {
		t.Fatal(err)
	}
	if err := d.set([]string{"provider", "custom", "options", "apiKey"}, "{env:KEY}"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.save(false); err != nil {
		t.Fatal(err)
	}
	o = ProviderOptions{ID: "custom", BaseURL: "http://localhost:8000/v1", Models: []string{"org/model~test", "second"}}
	if _, err := s.SetProvider(o); err != nil {
		t.Fatal(err)
	}
	d, _ = loadDocument(s.ConfigPath)
	if name, _ := d.string("provider", "custom", "name"); name != "Custom" {
		t.Fatal("name lost")
	}
	if d.get("provider", "custom", "models", "org/model~test", "limit") == nil {
		t.Fatal("model options lost")
	}
	if key, _ := d.string("provider", "custom", "options", "apiKey"); key != "{env:KEY}" {
		t.Fatal("credential reference lost")
	}
	r, err := s.SetProvider(o)
	if err != nil || r.Changed {
		t.Fatalf("not idempotent: %+v, %v", r, err)
	}
}

func TestProviderValidation(t *testing.T) {
	for _, o := range []ProviderOptions{
		{ID: "new"},
		{ID: "../bad", BaseURL: "https://example.test", Models: []string{"m"}},
		{ID: "new", BaseURL: "file:///tmp/model", Models: []string{"m"}},
		{ID: "new", BaseURL: "https://user:secret@example.test", Models: []string{"m"}},
		{ID: "new", BaseURL: "https://example.test?key=secret", Models: []string{"m"}},
		{ID: "new", BaseURL: "https://example.test", Models: []string{""}},
	} {
		s := fixture(t)
		if _, err := s.SetProvider(o); err == nil {
			t.Errorf("accepted %+v", o)
		}
		if _, err := os.Stat(s.ConfigPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid input created a file")
		}
	}
	s := fixture(t)
	put(t, s.ConfigPath, `{"provider":{"p":{"options":null}}}`)
	if _, err := s.SetProvider(ProviderOptions{ID: "p", BaseURL: "https://example.test"}); err == nil {
		t.Fatal("accepted null options")
	}
}

func TestTokenStorageAndOverrides(t *testing.T) {
	s := fixture(t)
	put(t, s.AuthPath, `{"custom":{"type":"api","key":"old","metadata":{"keep":"yes"}},"other":{"type":"oauth","access":"untouched"}}`)
	if err := os.Chmod(s.AuthPath, 0o644); err != nil {
		t.Fatal(err)
	}
	put(t, s.ConfigPath, `{"provider":{"custom":{"options":{"apiKey":"{env:KEY}"}}}}`)
	r, err := s.SetToken("custom", []byte("new-test-token\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 {
		t.Fatal("missing override warning")
	}
	data := contents(t, s.AuthPath)
	if !json.Valid([]byte(data)) {
		t.Fatal("auth is not strict JSON")
	}
	d := decoded(t, data)
	provider := d["custom"].(map[string]any)
	if provider["key"] != "new-test-token" || provider["metadata"].(map[string]any)["keep"] != "yes" {
		t.Fatal("token or metadata incorrect")
	}
	if d["other"].(map[string]any)["access"] != "untouched" {
		t.Fatal("changed other credential")
	}
	info, _ := os.Stat(s.AuthPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("insecure token permissions")
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "new-test-token") {
		t.Fatal("token in result")
	}
	r, err = s.SetToken("custom", []byte("new-test-token"))
	if err != nil || r.Changed {
		t.Fatalf("token write not idempotent: %+v, %v", r, err)
	}
	if _, err := s.SetToken("other", []byte("replacement")); err == nil {
		t.Fatal("replaced OAuth")
	}
	if contents(t, s.AuthPath) != data {
		t.Fatal("OAuth attempt changed the file")
	}
	s.Getenv = func(key string) string {
		if key == "OPENCODE_AUTH_CONTENT" {
			return "secret"
		}
		return ""
	}
	if _, err := s.SetToken("custom", []byte("new")); err == nil {
		t.Fatal("ignored auth override")
	}
}

func TestBadTokensAndMalformedAuth(t *testing.T) {
	for _, token := range []string{"", " ", "two words", "two\nlines", "nul\x00", strings.Repeat("x", MaxTokenSize+1)} {
		s := fixture(t)
		if _, err := s.SetToken("p", []byte(token)); err == nil {
			t.Fatal("accepted bad token")
		}
		if _, err := os.Stat(s.AuthPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created auth on error")
		}
	}
	s := fixture(t)
	put(t, s.AuthPath, `{"p":{"key":"SENSITIVE"`)
	_, err := s.SetToken("p", []byte("replacement"))
	if err == nil || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatalf("error: %v", err)
	}
}

func TestConcurrentChangeLockAndSymlink(t *testing.T) {
	s := fixture(t)
	put(t, s.ConfigPath, "{}")
	d, err := loadDocument(s.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.set([]string{"model"}, "p/m"); err != nil {
		t.Fatal(err)
	}
	put(t, s.ConfigPath, `{"keep":true}`)
	if _, err := d.save(false); err == nil {
		t.Fatal("overwrote concurrent edit")
	}
	if contents(t, s.ConfigPath) != `{"keep":true}` {
		t.Fatal("concurrent edit lost")
	}
	lock, err := os.OpenFile(s.ConfigPath+".ocswitch.lock", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Use("p/m"); err == nil {
		t.Fatal("ignored lock")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	target := s.ConfigPath
	s.ConfigPath += ".link"
	if err := os.Symlink(target, s.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Use("p/m"); err == nil {
		t.Fatal("replaced symlink")
	}
	if contents(t, target) != `{"keep":true}` {
		t.Fatal("changed symlink target")
	}
}

func TestList(t *testing.T) {
	s := fixture(t)
	s.ExplicitConfig = true
	s.RunModels = func(ctx context.Context, config string) ([]byte, error) {
		if config != s.ConfigPath {
			t.Fatal("missing explicit config")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing timeout")
		}
		return []byte("z/model\r\na/org/model\nz/model\n\n"), nil
	}
	models, err := s.List(context.Background())
	if err != nil || !reflect.DeepEqual(models, []string{"a/org/model", "z/model"}) {
		t.Fatalf("list: %v, %v", models, err)
	}
	s.RunModels = func(context.Context, string) ([]byte, error) { return []byte("SENSITIVE bad output"), nil }
	if _, err := s.List(context.Background()); err == nil || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatal("unexpected output was not hidden")
	}
}

func TestModelSubprocess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	_, err := runModels(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("missing opencode: %v", err)
	}
	path := filepath.Join(dir, "opencode")
	put(t, path, "#!/bin/sh\n[ \"$1\" = models ] || exit 2\n[ \"$OPENCODE_CONFIG\" = /tmp/explicit.json ] || exit 3\nprintf 'p/m\\n'\n")
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_CONFIG", "wrong")
	data, err := runModels(context.Background(), "/tmp/explicit.json")
	if err != nil || string(data) != "p/m\n" {
		t.Fatalf("subprocess: %q, %v", data, err)
	}
	put(t, path, "#!/bin/sh\nprintf SENSITIVE >&2\nexit 1\n")
	_, err = runModels(context.Background(), "")
	if err == nil || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatalf("child failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runModels(ctx, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
