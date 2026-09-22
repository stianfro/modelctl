package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stianfro/modelctl/internal/modelctl"
	"golang.org/x/term"
)

type usageError struct{ error }

func ExitCode(err error) int {
	var usage usageError
	if errors.As(err, &usage) {
		return 2
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}

func args(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, values []string) error {
		if err := check(cmd, values); err != nil {
			return usageError{err}
		}
		return nil
	}
}

type app struct {
	in     io.Reader
	out    io.Writer
	errout io.Writer
	config string
	json   bool
	target string
	binary string
}

func New(in io.Reader, out, errout io.Writer) *cobra.Command {
	a := &app{in: in, out: out, errout: errout, target: os.Getenv("MODELCTL_TARGET")}
	return a.command()
}

func (a *app) command() *cobra.Command {
	root := &cobra.Command{
		Use:           "modelctl",
		Version:       "dev",
		Short:         "Switch OpenCode models and API tokens",
		Long:          "Edit OpenCode's default model, custom providers, and API tokens.\nRun without a command in a terminal to open the interactive menu.\nChanges do not switch existing OpenCode sessions or remove project overrides.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.json || !terminalReader(a.in) || !terminalWriter(a.out) {
				return usageError{errors.New("interactive mode needs a terminal; use a command such as 'modelctl current --json' or 'modelctl --help'")}
			}
			s, err := a.service()
			if err != nil {
				return err
			}
			return runUI(cmd.Context(), s, a.in, a.out)
		},
	}
	root.SetIn(a.in)
	root.SetVersionTemplate("modelctl {{.Version}}\n")
	root.SetOut(a.out)
	root.SetErr(a.errout)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	if a.target == "" {
		a.target = "auto"
	}
	root.PersistentFlags().StringVar(&a.target, "target", a.target, "OpenCode target: auto, opencode or opencode2 (MODELCTL_TARGET)")
	root.PersistentFlags().StringVar(&a.binary, "opencode-bin", "", "OpenCode executable name or path (default: detect from PATH)")
	root.PersistentFlags().StringVar(&a.config, "config", "", "config file to edit (default: global OpenCode config)")
	root.PersistentFlags().BoolVar(&a.json, "json", false, "write command results as JSON")
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(a.currentCommand(), a.listCommand(), a.useCommand(), a.providerCommand(), a.tokenCommand())
	return root
}

func terminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func terminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (a *app) currentCommand() *cobra.Command {
	return &cobra.Command{
		Use: "current", Short: "Show the default model in the selected config file", Args: args(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := a.service()
			if err != nil {
				return err
			}
			c, err := s.Current()
			if err != nil {
				return err
			}
			if a.json {
				return json.NewEncoder(a.out).Encode(c)
			}
			if c.Model == "" {
				_, err = fmt.Fprintln(a.out, "(not set)")
			} else {
				_, err = fmt.Fprintln(a.out, c.Model)
			}
			return err
		},
	}
}

func (a *app) listCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List configured and discovered OpenCode models", Args: args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.service()
			if err != nil {
				return err
			}
			models, err := s.List(cmd.Context())
			if err != nil {
				if len(models) == 0 {
					return err
				}
				if _, writeErr := fmt.Fprintf(a.errout, "Warning: %s; showing configured model IDs only.\n", err); writeErr != nil {
					return writeErr
				}
			}
			if a.json {
				return json.NewEncoder(a.out).Encode(models)
			}
			for _, model := range models {
				if _, err := fmt.Fprintln(a.out, model); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func (a *app) useCommand() *cobra.Command {
	return &cobra.Command{
		Use: "use PROVIDER/MODEL", Short: "Set the default model for new work", Args: args(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, values []string) error {
			s, err := a.service()
			if err != nil {
				return err
			}
			result, err := s.Use(values[0])
			return a.result(result, err)
		},
	}
}

func (a *app) providerCommand() *cobra.Command {
	var o modelctl.ProviderOptions
	parent := &cobra.Command{Use: "provider", Short: "Configure custom providers", Args: args(cobra.NoArgs)}
	set := &cobra.Command{
		Use: "set ID", Short: "Add or update a custom provider without removing other settings", Args: args(cobra.ExactArgs(1)),
		Example: "  modelctl provider set custom --base-url https://api.example.com/v1 --model MODEL",
		RunE: func(cmd *cobra.Command, values []string) error {
			for _, name := range []string{"base-url", "name", "npm"} {
				v, _ := cmd.Flags().GetString(name)
				if cmd.Flags().Changed(name) && v == "" {
					return usageError{fmt.Errorf("--%s must not be empty", name)}
				}
			}
			o.ID = values[0]
			s, err := a.service()
			if err != nil {
				return err
			}
			result, err := s.SetProvider(o)
			return a.result(result, err)
		},
	}
	set.Flags().StringVar(&o.BaseURL, "base-url", "", "provider endpoint, including /v1 where required")
	set.Flags().StringArrayVar(&o.Models, "model", nil, "model ID to add (repeat for multiple models)")
	set.Flags().StringVar(&o.Name, "name", "", "provider display name")
	set.Flags().StringVar(&o.Package, "npm", "", "AI SDK package (new providers default to @ai-sdk/openai-compatible)")
	parent.AddCommand(set)
	return parent
}

func (a *app) tokenCommand() *cobra.Command {
	var stdin bool
	parent := &cobra.Command{Use: "token", Short: "Store API keys in OpenCode's global credential file", Args: args(cobra.NoArgs)}
	set := &cobra.Command{
		Use: "set PROVIDER", Short: "Save a token from hidden terminal input or --stdin", Args: args(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, values []string) error {
			if err := modelctl.ValidateProvider(values[0]); err != nil {
				return usageError{err}
			}
			s, err := a.service()
			if err != nil {
				return err
			}
			if s.Target == "opencode2" {
				if stdin || a.json || !terminalReader(a.in) || !terminalWriter(a.errout) {
					return usageError{errors.New("V2 tokens require OpenCode's interactive login; run modelctl --target opencode2 token set PROVIDER without --stdin or --json")}
				}
				login := s.LoginCommand(cmd.Context(), values[0])
				login.Stdin, login.Stdout, login.Stderr = a.in, a.errout, a.errout
				if err := login.Run(); err != nil {
					if cmd.Context().Err() != nil {
						return cmd.Context().Err()
					}
					return errors.New("OpenCode 2 login did not complete")
				}
				return nil
			}
			var token []byte
			if stdin {
				token, err = readToken(cmd.Context(), a.in)
				if err != nil {
					return err
				}
			} else {
				if !terminalReader(a.in) || !terminalWriter(a.errout) || a.json {
					return usageError{errors.New("use --stdin to supply a token without a terminal prompt")}
				}
				token, err = readPassword(cmd.Context(), a.in, a.errout)
				if err != nil {
					return err
				}
			}
			defer clear(token)
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			result, err := s.SetToken(values[0], token)
			return a.result(result, err)
		},
	}
	set.Flags().BoolVar(&stdin, "stdin", false, "read the token from stdin, never from a command argument")
	parent.AddCommand(set)
	return parent
}

func readToken(ctx context.Context, in io.Reader) ([]byte, error) {
	type answer struct {
		token []byte
		err   error
	}
	done := make(chan answer)
	go func() {
		token, err := io.ReadAll(io.LimitReader(in, modelctl.MaxTokenSize+3))
		select {
		case done <- answer{token, err}:
		case <-ctx.Done():
			clear(token)
		}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			clear(result.token)
			return nil, errors.New("could not read token from stdin")
		}
		return result.token, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *app) result(result modelctl.Result, err error) error {
	if err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintln(a.errout, "warning:", warning); err != nil {
			return err
		}
	}
	if a.json {
		return json.NewEncoder(a.out).Encode(result)
	}
	_, err = fmt.Fprintln(a.out, resultText(result))
	return err
}

func resultText(r modelctl.Result) string {
	prefix := "Saved"
	if !r.Changed {
		prefix = "Unchanged:"
	}
	switch r.Action {
	case "use":
		return fmt.Sprintf("%s default model %s in %s", prefix, r.Model, r.Path)
	case "provider":
		return fmt.Sprintf("%s provider %s in %s", prefix, r.Provider, r.Path)
	case "token":
		return fmt.Sprintf("%s token for %s in %s", prefix, r.Provider, r.Path)
	default:
		return strings.TrimSpace(prefix)
	}
}

func (a *app) service() (*modelctl.Service, error) {
	if a.target != "auto" && a.target != "opencode" && a.target != "opencode2" {
		return nil, usageError{errors.New("target must be auto, opencode or opencode2")}
	}
	return modelctl.NewTarget(a.config, a.target, a.binary)
}
