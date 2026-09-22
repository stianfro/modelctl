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

	"github.com/tailscale/hujson"
)

const MaxTokenSize = 64 << 10

type Service struct {
	Target         string
	Binary         string
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
	Path     string   `json:"path,omitempty"`
	Store    string   `json:"store,omitempty"`
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
	return NewTarget(config, "auto", "")
}

// NewTarget selects or detects a config dialect. It never migrates a file.
func NewTarget(config, target, binary string) (*Service, error) {
	if target != "auto" && target != "opencode" && target != "opencode2" {
		return nil, errors.New("target must be auto, opencode or opencode2")
	}

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
	if target == "auto" {
		target, binary, err = detectTarget(config, binary)
		if err != nil {
			return nil, err
		}
	}
	if binary == "" {
		binary = target
	}
	return &Service{
		Target:         target,
		Binary:         binary,
		ConfigPath:     config,
		AuthPath:       filepath.Join(dataHome, "opencode", "auth.json"),
		ExplicitConfig: explicit,
		Getenv:         os.Getenv,
		RunModels: func(ctx context.Context, config string) ([]byte, error) {
			return runModelsBinary(ctx, config, binary, target == "opencode2")
		},
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
	c.Model, err = s.modelRef(d)
	if err == nil && c.Model != "" {
		err = s.validateCurrentRef(c.Model)
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
	if current, err := s.modelRef(d); err != nil {
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
	root, options, pkg, err := s.providerLayout(d)
	if err != nil {
		return r, err
	}
	if root == "providers" && o.Package != "" && !strings.HasPrefix(o.Package, "aisdk:") {
		o.Package = "aisdk:" + o.Package
	}
	for _, path := range [][]string{{root}, {root, o.ID}, {root, o.ID, options}, {root, o.ID, "models"}} {
		if err := d.object(path...); err != nil {
			return r, err
		}
	}
	if d.get(root, o.ID) == nil {
		if o.BaseURL == "" || len(o.Models) == 0 {
			return r, errors.New("a new custom provider needs --base-url and at least one --model")
		}
		if o.Package == "" {
			o.Package = "@ai-sdk/openai-compatible"
			if root == "providers" {
				o.Package = "aisdk:" + o.Package
			}
		}
	} else if o.BaseURL == "" && o.Name == "" && o.Package == "" && len(o.Models) == 0 {
		return r, errors.New("supply at least one provider setting")
	}
	for _, field := range []struct {
		path  []string
		value string
	}{
		{[]string{root, o.ID, options, "baseURL"}, o.BaseURL},
		{[]string{root, o.ID, "name"}, o.Name},
		{[]string{root, o.ID, pkg}, o.Package},
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
		path := []string{root, o.ID, "models", model}
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
	return s.SetTokenContext(context.Background(), provider, token)
}

func (s *Service) SetTokenContext(ctx context.Context, provider string, token []byte) (Result, error) {
	r := Result{Action: "token", Path: s.AuthPath, Provider: provider}
	if err := ValidateProvider(provider); err != nil {
		return r, err
	}
	key := strings.TrimSuffix(strings.TrimSuffix(string(token), "\n"), "\r")
	if key == "" || len(key) > MaxTokenSize || !utf8.ValidString(key) || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return r, errors.New("token must contain 1 to 65536 bytes without whitespace (one final newline is allowed)")
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if s.Target == "opencode2" {
		return s.setV2Token(ctx, provider, key)
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

// List returns configured IDs even when runtime discovery fails. Callers must
// show the error as a warning when a partial list is returned.
func (s *Service) List(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	configured, err := s.configuredModels()
	if err != nil {
		return nil, err
	}
	override := ""
	if s.ExplicitConfig {
		override = s.ConfigPath
	}
	data, err := s.RunModels(ctx, override)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return configured, err
	}
	models := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if ValidateModelRef(line) != nil {
			return configured, errors.New("unexpected output from OpenCode models; check your OpenCode installation")
		}
		models = append(models, line)
	}
	if len(models) == 0 && len(configured) > 0 {
		return configured, errors.New("OpenCode returned no models")
	}
	models = append(models, configured...)
	slices.Sort(models)
	return slices.Compact(models), nil
}

func (s *Service) configuredModels() ([]string, error) {
	d, err := loadDocument(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	root, _, _, err := s.providerLayout(d)
	if err != nil {
		return nil, err
	}
	if err := d.object(root); err != nil {
		return nil, err
	}
	models := []string{}
	if v := d.get(root); v != nil {
		for _, provider := range v.Value.(*hujson.Object).Members {
			id := provider.Name.Value.(hujson.Literal).String()
			if ValidateProvider(id) != nil {
				continue
			}
			if err := d.object(root, id, "models"); err != nil {
				return nil, err
			}
			if entries := d.get(root, id, "models"); entries != nil {
				for _, model := range entries.Value.(*hujson.Object).Members {
					ref := id + "/" + model.Name.Value.(hujson.Literal).String()
					if ValidateModelRef(ref) == nil {
						models = append(models, ref)
					}
				}
			}
		}
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
	return runModelsBinary(ctx, config, "opencode", false)
}

func runModelsBinary(ctx context.Context, config, binary string, v2 bool) ([]byte, error) {
	args := []string{"models"}
	// A private V2 server reads this process's config and environment, not a
	// long-running background server's stale state.
	if v2 {
		args = append(args, "--standalone")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	if config != "" {
		cmd.Env = append(withoutEnv(os.Environ(), "OPENCODE_CONFIG"), "OPENCODE_CONFIG="+config)
	}
	var output boundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard // OpenCode parse errors can contain full configs, including secrets.
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s is not installed or not on PATH; select a binary with --opencode-bin", binary)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("opencode models: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%s models failed; run it directly in this directory to inspect the error (child output hidden to protect secrets)", binary)
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

func (s *Service) providerLayout(d *document) (root, options, pkg string, err error) {
	if d.get("provider") != nil && d.get("providers") != nil {
		return "", "", "", errors.New("config contains both provider and providers; use separate V1 and V2 config files")
	}
	if s.Target != "opencode2" && d.get("providers") != nil {
		return "", "", "", errors.New("this is a V2 provider config; use --target opencode2")
	}
	if s.Target == "opencode2" && d.get("provider") == nil {
		return "providers", "settings", "package", nil
	}
	return "provider", "options", "npm", nil
}

func (s *Service) modelRef(d *document) (string, error) {
	if s.Target == "opencode2" {
		if v := d.get("model"); v != nil {
			if _, ok := v.Value.(*hujson.Object); ok {
				provider, err := d.string("model", "providerID")
				if err != nil {
					return "", err
				}
				model, err := d.string("model", "model")
				if err != nil {
					return "", err
				}
				ref := provider + "/" + model
				variant, err := d.string("model", "variant")
				if err != nil {
					return "", err
				}
				if variant != "" {
					ref += "#" + variant
				}
				return ref, s.validateCurrentRef(ref)
			}
		}
	}
	return d.string("model")
}

func (s *Service) validateCurrentRef(ref string) error {
	if s.Target == "opencode2" {
		model, variant, found := strings.Cut(ref, "#")
		if found {
			if err := validateModel(variant); err != nil {
				return err
			}
			return ValidateModelRef(model)
		}
	}
	return ValidateModelRef(ref)
}
