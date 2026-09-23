package modelctl

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// setV2Token uses the same key-connection API as OpenCode's login command. The
// API key travels only in an authenticated loopback request body, never argv,
// environment, config files, or child output. OpenCode owns database updates.
func (s *Service) setV2Token(ctx context.Context, provider, key string) (Result, error) {
	result := Result{Action: "token", Provider: provider, Store: "opencode2"}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	passwordBytes := make([]byte, 32)
	if _, err := rand.Read(passwordBytes); err != nil {
		return result, errors.New("cannot create private OpenCode connection")
	}
	password := hex.EncodeToString(passwordBytes)
	clear(passwordBytes)
	binary := s.Binary
	if binary == "" {
		binary = "opencode2"
	}
	cmd := exec.CommandContext(ctx, binary, "serve", "--stdio", "--hostname", "127.0.0.1", "--port", "0")
	cmd.WaitDelay = time.Second
	env := os.Environ()
	for _, name := range []string{"OPENCODE_PASSWORD", "OPENCODE_SERVER_PASSWORD", "OPENCODE_CONFIG"} {
		env = withoutEnv(env, name)
	}
	cmd.Env = append(env, "OPENCODE_PASSWORD="+password, "OPENCODE_SERVER_PASSWORD="+password, "OPENCODE_CONFIG="+s.ConfigPath)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return result, errors.New("cannot open private OpenCode connection")
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result, errors.New("cannot read private OpenCode connection")
	}
	defer stdout.Close()
	if err := cmd.Start(); err != nil {
		return result, errors.New("cannot start OpenCode 2; check --opencode-bin")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		stdin.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			cmd.Process.Kill()
			<-done
		}
	}()
	line, err := bufio.NewReader(io.LimitReader(stdout, 4096)).ReadString('\n')
	if err != nil {
		return result, errors.New("OpenCode 2 did not start its private API; check your installation")
	}
	var endpoint struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(line), &endpoint) != nil {
		return result, errors.New("unexpected OpenCode 2 startup response (output hidden)")
	}
	base, err := privateEndpoint(endpoint.URL)
	if err != nil {
		return result, err
	}
	// Drain diagnostics without retaining or displaying potentially sensitive data.
	go io.Copy(io.Discard, stdout)
	directory, err := os.Getwd()
	if err != nil {
		return result, errors.New("cannot resolve current directory")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	if err := connectV2Token(ctx, client, base, password, directory, provider, key); err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

func privateEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("refusing non-local OpenCode 2 API endpoint")
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return nil, errors.New("invalid private OpenCode 2 API endpoint")
	}
	return u, nil
}

func connectV2Token(ctx context.Context, client *http.Client, base *url.URL, password, directory, provider, key string) error {
	path := "/api/integration/" + url.PathEscape(provider)
	request := func(method, path string, body []byte) (*http.Response, error) {
		u := *base
		u.Path = path
		u.RawPath = ""
		u.RawQuery = url.Values{"location[directory]": []string{directory}}.Encode()
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("cannot create OpenCode 2 token request")
		}
		req.SetBasicAuth("opencode", password)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New("could not confirm the OpenCode 2 token operation (response hidden)")
		}
		return response, nil
	}
	// New V2 locations register integrations asynchronously. Wait before sending
	// the key. Older previews return null while loading; newer versions return 404.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := request(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var envelope struct {
			Data *struct {
				ID      string `json:"id"`
				Methods []struct {
					Type string          `json:"type"`
					Form json.RawMessage `json:"form"`
				} `json:"methods"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope)
		response.Body.Close()
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound {
			return fmt.Errorf("OpenCode 2 provider lookup failed (HTTP %d; response hidden)", response.StatusCode)
		}
		if response.StatusCode == http.StatusOK && decodeErr != nil {
			return errors.New("unexpected OpenCode 2 provider response (output hidden)")
		}
		if response.StatusCode == http.StatusOK && envelope.Data != nil {
			if envelope.Data.ID != provider {
				return errors.New("OpenCode 2 returned a different provider; token was not sent")
			}
			ready := false
			for _, method := range envelope.Data.Methods {
				if method.Type != "key" {
					continue
				}
				form := strings.TrimSpace(string(method.Form))
				if form != "" && form != "null" && form != "[]" {
					return errors.New("this provider requires additional login details; use OpenCode auth login")
				}
				ready = true
			}
			if !ready {
				return errors.New("this provider does not support API key login")
			}
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("OpenCode 2 provider did not become ready; check the provider ID and config")
		case <-ticker.C:
		}
	}
	body, err := json.Marshal(struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}{key, "modelctl"})
	if err != nil {
		return errors.New("cannot encode token request")
	}
	defer clear(body)
	response, err := request(http.MethodPost, path+"/connect/key", body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("OpenCode 2 did not confirm the token save (HTTP %d; response hidden)", response.StatusCode)
	}
	return nil
}
