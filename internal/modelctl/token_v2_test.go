package modelctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func privateServerFixture(t *testing.T, endpoint string) *Service {
	t.Helper()
	s := fixture(t)
	s.Target = "opencode2"
	s.Binary = filepath.Join(t.TempDir(), "opencode2")
	// Keep stdin open just like the real --stdio server. Record graceful shutdown.
	script := fmt.Sprintf("#!/bin/sh\n[ \"$*\" = 'serve --stdio --hostname 127.0.0.1 --port 0' ] || exit 99\nprintf '%%s\\n' '{\"url\":\"%s\"}'\nwhile IFS= read -r line; do :; done\nprintf done > '%s.done'\n", endpoint, s.Binary)
	put(t, s.Binary, script)
	if err := os.Chmod(s.Binary, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, s.ConfigPath, `{"providers":{"custom":{"models":{"m":{}}}}}`)
	return s
}

func TestV2TokenNativeAPI(t *testing.T) {
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "opencode" || len(password) != 64 {
			t.Error("missing private connection authentication")
		}
		if r.URL.Query().Get("location[directory]") == "" {
			t.Error("missing location")
		}
		if r.Method == http.MethodGet {
			if gets.Add(1) == 1 {
				fmt.Fprint(w, `{"data":null}`)
				return
			}
			fmt.Fprint(w, `{"data":{"id":"custom","methods":[{"type":"key"}]}}`)
			return
		}
		posts.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/integration/custom/connect/key" {
			t.Error("wrong save endpoint")
		}
		var body struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Key != "SENSITIVE-KEY" || body.Label != "modelctl" {
			t.Error("incorrect token payload")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s := privateServerFixture(t, server.URL)
	before := contents(t, s.ConfigPath)
	result, err := s.SetToken("custom", []byte("SENSITIVE-KEY\n"))
	if err != nil || !result.Changed || result.Store != "opencode2" || result.Path != "" {
		t.Fatalf("%+v %v", result, err)
	}
	if gets.Load() != 2 || posts.Load() != 1 {
		t.Fatal("did not wait for integration readiness")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SENSITIVE-KEY") {
		t.Fatal("token leaked into result")
	}
	if contents(t, s.ConfigPath) != before {
		t.Fatal("config was modified")
	}
	if _, err := os.Stat(s.AuthPath); !os.IsNotExist(err) {
		t.Fatal("V1 auth file was written")
	}
	if contents(t, s.Binary+".done") != "done" {
		t.Fatal("private server not stopped")
	}
}

func TestV2TokenFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		post           bool
	}{
		{"malformed", `SENSITIVE-KEY`, 200, false},
		{"wrong provider", `{"data":{"id":"other","methods":[{"type":"key"}]}}`, 200, false},
		{"no key method", `{"data":{"id":"custom","methods":[{"type":"oauth"}]}}`, 200, false},
		{"form required", `{"data":{"id":"custom","methods":[{"type":"key","form":[{"key":"resource"}]}]}}`, 200, false},
		{"save rejected", `{"data":{"id":"custom","methods":[{"type":"key"}]}}`, 500, true},
		{"redirect", `{"data":{"id":"custom","methods":[{"type":"key"}]}}`, 307, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					fmt.Fprint(w, tc.response)
					return
				}
				posts.Add(1)
				w.Header().Set("Location", "http://example.test/secret")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, "SENSITIVE-KEY")
			}))
			defer server.Close()
			s := privateServerFixture(t, server.URL)
			_, err := s.SetToken("custom", []byte("SENSITIVE-KEY"))
			if err == nil || strings.Contains(err.Error(), "SENSITIVE-KEY") {
				t.Fatalf("unsafe error: %v", err)
			}
			if (posts.Load() > 0) != tc.post || posts.Load() > 1 {
				t.Fatal("unexpected token transmission or retry")
			}
		})
	}
}

func TestV2TokenCancellationAndEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:1", "http://example.com:80", "http://localhost:80", "http://127.0.0.1:80/path", "http://user@127.0.0.1:80", "http://127.0.0.1:80?x=1"} {
		if _, err := privateEndpoint(endpoint); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":null}`) }))
	defer server.Close()
	s := privateServerFixture(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.SetTokenContext(ctx, "custom", []byte("test-token")); err == nil {
		t.Fatal("ignored cancellation")
	}
	// Invalid tokens must be rejected before launching a process.
	s.Binary = "missing"
	if _, err := s.SetToken("custom", []byte("bad token")); err == nil || !strings.Contains(err.Error(), "without whitespace") {
		t.Fatal(err)
	}
}
