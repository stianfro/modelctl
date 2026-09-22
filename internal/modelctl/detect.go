package modelctl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/tailscale/hujson"
)

// detectTarget uses only the selected config and --version. It never starts an
// OpenCode server or opens the credential store. Explicit --target bypasses detection.
func detectTarget(config, binary string) (string, string, error) {
	d, err := loadDocument(config)
	if err != nil {
		return "", "", err
	}
	hint, err := configTarget(d)
	if err != nil {
		return "", "", err
	}
	if binary != "" {
		target, err := binaryTarget(binary)
		if err != nil {
			return "", "", err
		}
		if hint == "opencode2" && target != hint {
			return "", "", errors.New("selected binary is OpenCode 1 but the config uses V2 fields; select a V2 binary with --opencode-bin")
		}
		return target, binary, nil
	}
	type candidate struct{ target, binary string }
	var candidates []candidate
	var probeErr error
	for _, name := range []string{"opencode", "opencode2"} {
		path, err := exec.LookPath(name)
		if errors.Is(err, exec.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", "", errors.New("cannot resolve OpenCode executable; set --target and --opencode-bin explicitly")
		}
		target, err := binaryTarget(path)
		if err != nil {
			probeErr = err
			continue
		}
		candidates = append(candidates, candidate{target, path})
	}
	for _, c := range candidates {
		if c.target == hint {
			return c.target, c.binary, nil
		}
	}
	if probeErr != nil {
		return "", "", probeErr
	}
	if len(candidates) > 0 {
		first := candidates[0]
		if hint == "opencode2" {
			return "", "", errors.New("V2 config detected, but no V2 binary was found; select one with --opencode-bin or use --target opencode2 for offline edits")
		}
		for _, c := range candidates[1:] {
			if c.target != first.target {
				return "", "", errors.New("both OpenCode versions are installed and this config does not identify one; choose --target opencode or --target opencode2 (or set MODELCTL_TARGET)")
			}
		}
		// V2 accepts existing V1 configs. Do not force V1 when only V2 is installed.
		return first.target, first.binary, nil
	}
	// Keep direct config editing usable without OpenCode installed.
	if hint == "" {
		hint = "opencode"
	}
	return hint, hint, nil
}

func configTarget(d *document) (string, error) {
	v1, v2 := d.get("provider") != nil, d.get("providers") != nil
	for _, key := range []string{"agents", "plugins", "snapshots", "attachments"} {
		v2 = v2 || d.get(key) != nil
	}
	if model := d.get("model"); model != nil {
		_, object := model.Value.(*hujson.Object)
		v2 = v2 || object
		if literal, ok := model.Value.(hujson.Literal); ok && literal.Kind() == '"' {
			v2 = v2 || strings.Contains(literal.String(), "#")
		}
	}
	for _, key := range []string{"agent", "plugin", "small_model", "enabled_providers", "disabled_providers"} {
		v1 = v1 || d.get(key) != nil
	}
	if v1 && v2 {
		return "", errors.New("config mixes V1 and V2 fields; select --target explicitly before editing")
	}
	if v2 {
		return "opencode2", nil
	}
	if v1 {
		return "opencode", nil
	}
	return "", nil
}

var versionLine = regexp.MustCompile(`^(?:(opencode2|opencode)\s+)?v?([0-9]+)\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.+-]+)?$`)

func binaryTarget(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.WaitDelay = 100 * time.Millisecond
	output := &versionOutput{}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.New("could not detect OpenCode version; set --target and --opencode-bin explicitly (version output hidden)")
	}
	match := versionLine.FindStringSubmatch(strings.TrimSpace(output.String()))
	if match != nil {
		if match[1] == "opencode2" || match[2] == "2" {
			return "opencode2", nil
		}
		if match[2] == "1" {
			return "opencode", nil
		}
	}
	return "", errors.New("unrecognized OpenCode version; set --target explicitly (version output hidden)")
}

type versionOutput struct{ buffer bytes.Buffer }

func (b *versionOutput) String() string { return b.buffer.String() }

func (b *versionOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 4096 {
		return 0, errors.New("version output too large")
	}
	return b.buffer.Write(p)
}
