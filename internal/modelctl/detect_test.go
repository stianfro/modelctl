package modelctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeVersion(t *testing.T, dir, name, version string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	put(t, path, "#!/bin/sh\n[ \"$1\" = --version ] || exit 99\nprintf '%s\\n' '"+version+"'\n")
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectTarget(t *testing.T) {
	for _, tc := range []struct {
		name, config, v1, v2, want, binary, problem string
	}{
		{name: "V1 config with both", config: `{"provider":{}}`, v1: "1.18.32", v2: "opencode2 v0.0.0-beta-19059", want: "opencode", binary: "opencode"},
		{name: "V2 config with both", config: `{"providers":{}}`, v1: "1.18.32", v2: "opencode2 v0.0.0-beta-19059", want: "opencode2", binary: "opencode2"},
		{name: "V2 model object", config: `{"model":{"providerID":"p","model":"m"}}`, v1: "1.18.32", v2: "opencode2 v0.0.0-beta-19059", want: "opencode2", binary: "opencode2"},
		{name: "V2 variant", config: `{"model":"p/m#fast"}`, v2: "opencode2 v0.0.0-beta-19059", want: "opencode2", binary: "opencode2"},
		{name: "V2 named opencode", config: `{}`, v1: "2.0.0", want: "opencode2", binary: "opencode"},
		{name: "V2 preview named opencode", config: `{}`, v1: "opencode2 v0.0.0-beta-19059", want: "opencode2", binary: "opencode"},
		{name: "only V1", config: `{}`, v1: "opencode v1.18.32", want: "opencode", binary: "opencode"},
		{name: "only V2 legacy config", config: `{"provider":{}}`, v2: "opencode2 v0.0.0-beta-19059", want: "opencode2", binary: "opencode2"},
		{name: "both ambiguous", config: `{"model":"p/m"}`, v1: "1.18.32", v2: "2.0.0", problem: "both OpenCode versions"},
		{name: "V2 config only V1 binary", config: `{"providers":{}}`, v1: "1.18.32", problem: "no V2 binary"},
		{name: "mixed", config: `{"provider":{},"providers":{}}`, problem: "mixes V1 and V2"},
		{name: "offline V2", config: `{"providers":{}}`, want: "opencode2", binary: "opencode2"},
		{name: "offline default", config: `{}`, want: "opencode", binary: "opencode"},
		{name: "unknown", config: `{}`, v1: "SENSITIVE-SECRET", problem: "unrecognized"},
		{name: "broken irrelevant binary", config: `{"providers":{}}`, v1: "unknown", v2: "2.0.0", want: "opencode2", binary: "opencode2"},
		{name: "invalid config", config: `{"secret":"SENSITIVE-SECRET",`, problem: "invalid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			config := filepath.Join(dir, "opencode.jsonc")
			put(t, config, tc.config)
			if tc.v1 != "" {
				fakeVersion(t, dir, "opencode", tc.v1)
			}
			if tc.v2 != "" {
				fakeVersion(t, dir, "opencode2", tc.v2)
			}
			s, err := NewTarget(config, "auto", "")
			if tc.problem != "" {
				if err == nil || !strings.Contains(err.Error(), tc.problem) || strings.Contains(err.Error(), "SENSITIVE-SECRET") {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err != nil || s.Target != tc.want || filepath.Base(s.Binary) != tc.binary {
				t.Fatalf("got %+v, %v", s, err)
			}
			if contents(t, config) != tc.config {
				t.Fatal("detection changed config")
			}
		})
	}
}

func TestDetectionOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	config := filepath.Join(dir, "config.jsonc")
	put(t, config, `{"provider":{}}`)
	binary := fakeVersion(t, dir, "preview", "opencode2 v0.0.0-beta-19059")
	s, err := NewTarget(config, "auto", binary)
	if err != nil || s.Target != "opencode2" || s.Binary != binary {
		t.Fatalf("%+v %v", s, err)
	}
	// An explicit target must not probe or require an installed binary.
	s, err = NewTarget(config, "opencode2", "missing")
	if err != nil || s.Target != "opencode2" || s.Binary != "missing" {
		t.Fatalf("%+v %v", s, err)
	}
	put(t, config, `{"providers":{}}`)
	v1 := fakeVersion(t, dir, "v1", "1.18.32")
	if _, err = NewTarget(config, "auto", v1); err == nil {
		t.Fatal("accepted V1 binary for V2 config")
	}
}

func TestVersionOutputBounded(t *testing.T) {
	output := &versionOutput{}
	if _, err := output.Write(make([]byte, 4097)); err == nil {
		t.Fatal("version output was not bounded")
	}
}
