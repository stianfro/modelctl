package modelctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxTokenSize = 64 << 10

type Service struct {
	ConfigPath     string
	AuthPath       string
	ExplicitConfig bool
	Getenv         func(string) string
	RunModels      func(context.Context, string) ([]byte, error)
}

type Current struct {
	ConfigPath string `json:"config_path"`
	Model      string `json:"model"`
}

type Result struct {
	Action   string   `json:"action"`
	Path     string   `json:"path"`
	Changed  bool     `json:"changed"`
	Model    string   `json:"model,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type ProviderOptions struct {
	ID      string
	BaseURL string
	Models  []string
	Name    string
	Package string
}

func New(config string) (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	explicit := config != ""
	if !explicit {
		config = filepath.Join(configHome, "opencode", "opencode.jsonc")
		if _, err := os.Lstat(config); errors.Is(err, os.ErrNotExist) {
			candidate := filepath.Join(configHome, "opencode", "opencode.json")
			if _, err := os.Lstat(candidate); err == nil {
				config = candidate
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	}
	config, err = filepath.Abs(config)
	if err != nil {
		return nil, err
	}
	return &Service{
		ConfigPath:     config,
		AuthPath:       filepath.Join(dataHome, "opencode", "auth.json"),
		ExplicitConfig: explicit,
		Getenv:         os.Getenv,
		RunModels:      runModels,
	}, nil
}

var providerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ValidateProvider(id string) error {
	if !providerPattern.MatchString(id) {
		return errors.New("provider ID must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	return nil
}

func validateModel(id string) error {
	if id == "" || strings.Contains(id, "#") || strings.HasPrefix(id, "/") || strings.HasSuffix(id, "/") ||
		strings.Contains(id, "//") || !utf8.ValidString(id) || strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("model ID must be nonempty, without whitespace or a #variant")
	}
	return nil
}

func ValidateModelRef(ref string) error {
	provider, model, ok := strings.Cut(ref, "/")
	if !ok {
		return errors.New("use a model reference in provider/model format")
	}
	if err := ValidateProvider(provider); err != nil {
		return err
	}
	return validateModel(model)
}

func (s *Service) Current() (Current, error) {
	c := Current{ConfigPath: s.ConfigPath}
	d, err := loadDocument(s.ConfigPath)
	if err != nil {
		return c, err
	}
	c.Model, err = d.string("model")
	if err == nil && c.Model != "" {
		err = ValidateModelRef(c.Model)
	}
	return c, err
}

func (s *Service) Use(model string) (Result, error) {
	r := Result{Action: "use", Path: s.ConfigPath, Model: model}
	if err := ValidateModelRef(model); err != nil {
		return r, err
	}
	d, err := loadDocument(s.ConfigPath)
	if err != nil {
		return r, err
	}
	if current, err := d.string("model"); err != nil {
		return r, err
	} else if current != model {
		if err := d.set([]string{"model"}, model); err != nil {
			return r, err
		}
	}
	r.Changed, err = d.save(false)
	return r, err
}

func (s *Service) SetProvider(o ProviderOptions) (Result, error) {
	r := Result{Action: "provider", Path: s.ConfigPath, Provider: o.ID}
	if err := ValidateProvider(o.ID); err != nil {
		return r, err
	}
	if o.BaseURL != "" {
		u, err := url.Parse(o.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return r, errors.New("base URL must be an HTTP(S) URL without credentials, query parameters or a fragment")
		}
	}
	if strings.IndexFunc(o.Name, unicode.IsControl) >= 0 || strings.IndexFunc(o.Package, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return r, errors.New("invalid provider name or package")
	}
	for _, model := range o.Models {
		if err := validateModel(model); err != nil {
			return r, err
		}
	}
	d, err := loadDocument(s.ConfigPath)
	if err != nil {
		return r, err
	}
	for _, path := range [][]string{{"provider"}, {"provider", o.ID}, {"provider", o.ID, "options"}, {"provider", o.ID, "models"}} {
		if err := d.object(path...); err != nil {
			return r, err
		}
	}
	if d.get("provider", o.ID) == nil {
		if o.BaseURL == "" || len(o.Models) == 0 {
			return r, errors.New("a new custom provider needs --base-url and at least one --model")
		}
		if o.Package == "" {
			o.Package = "@ai-sdk/openai-compatible"
		}
	} else if o.BaseURL == "" && o.Name == "" && o.Package == "" && len(o.Models) == 0 {
		return r, errors.New("supply at least one provider setting")
	}
	for _, field := range []struct {
		path  []string
		value string
	}{
		{[]string{"provider", o.ID, "options", "baseURL"}, o.BaseURL},
		{[]string{"provider", o.ID, "name"}, o.Name},
		{[]string{"provider", o.ID, "npm"}, o.Package},
	} {
		if field.value == "" {
			continue
		}
		old, err := d.string(field.path...)
		if err != nil {
			return r, err
		}
		if old != field.value {
			if err := d.set(field.path, field.value); err != nil {
				return r, err
			}
		}
	}
	for _, model := range o.Models {
		path := []string{"provider", o.ID, "models", model}
		if err := d.object(path...); err != nil {
			return r, err
		}
		if d.get(path...) == nil {
			if err := d.set(path, map[string]any{}); err != nil {
				return r, err
			}
		}
	}
	r.Changed, err = d.save(false)
	return r, err
}

func (s *Service) SetToken(provider string, token []byte) (Result, error) {
	r := Result{Action: "token", Path: s.AuthPath, Provider: provider}
	if err := ValidateProvider(provider); err != nil {
		return r, err
	}
	key := strings.TrimSuffix(strings.TrimSuffix(string(token), "\n"), "\r")
	if key == "" || len(key) > MaxTokenSize || !utf8.ValidString(key) || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return r, errors.New("token must contain 1 to 65536 bytes without whitespace (one final newline is allowed)")
	}
	if s.Getenv != nil && s.Getenv("OPENCODE_AUTH_CONTENT") != "" {
		return r, errors.New("OPENCODE_AUTH_CONTENT overrides the credential file; unset it before saving a token")
	}
	config, err := loadDocument(s.ConfigPath)
	if err != nil {
		return r, err
	}
	if config.get("provider", provider, "options", "apiKey") != nil {
		r.Warnings = append(r.Warnings, "provider.options.apiKey in the selected config can override this token; remove that setting to use the stored credential")
	}
	d, err := loadDocument(s.AuthPath)
	if err != nil {
		return r, err
	}
	if err := d.object(provider); err != nil {
		return r, err
	}
	if d.get(provider) != nil {
		typ, err := d.string(provider, "type")
		if err != nil {
			return r, err
		}
		if typ != "api" {
			return r, errors.New("refusing to replace a non-API credential; manage OAuth and other login methods with OpenCode")
		}
	}
	if err := d.set([]string{provider, "type"}, "api"); err != nil {
		return r, err
	}
	if err := d.set([]string{provider, "key"}, key); err != nil {
		return r, err
	}
	// auth.json must remain strict JSON, even if a user added comments to it.
	d.tree.Standardize()
	r.Changed, err = d.save(true)
	return r, err
}

func (s *Service) List(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	override := ""
	if s.ExplicitConfig {
		override = s.ConfigPath
	}
	data, err := s.RunModels(ctx, override)
	if err != nil {
		return nil, err
	}
	models := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if ValidateModelRef(line) != nil {
			return nil, errors.New("unexpected output from opencode models; check your OpenCode installation")
		}
		models = append(models, line)
	}
	slices.Sort(models)
	return slices.Compact(models), nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxFileSize {
		return 0, errors.New("model list is too large")
	}
	return b.Buffer.Write(p)
}

func runModels(ctx context.Context, config string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "opencode", "models")
	cmd.WaitDelay = time.Second
	if config != "" {
		cmd.Env = append(withoutEnv(os.Environ(), "OPENCODE_CONFIG"), "OPENCODE_CONFIG="+config)
	}
	var output boundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard // OpenCode parse errors can contain full configs, including secrets.
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("opencode is not installed or not on PATH; direct config and token commands still work")
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("opencode models: %w", ctx.Err())
		}
		return nil, errors.New("opencode models failed; check your OpenCode config and credentials (child output was hidden to protect secrets)")
	}
	return output.Bytes(), nil
}

func withoutEnv(env []string, key string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			result = append(result, entry)
		}
	}
	return result
}
