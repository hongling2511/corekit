// Package app is the start-up pipeline of corekit: it turns an application
// name, an options object and a set of subcommands into a command-line program.
//
// Every subcommand runs the same fixed pipeline: parse flags → load the
// configuration → decode it into the options object → complete → validate →
// initialise logging → log the version and the redacted effective
// configuration → run the subcommand. The built-in `version` and
// `config print` subcommands are always present.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/config"
	"github.com/hongling2511/corekit/log"
	"github.com/hongling2511/corekit/version"
)

// Logging is implemented by an options object that embeds the log options
// block (under the key "log"); the pipeline initialises logging from it.
// Without it, logging uses the default log options.
type Logging interface {
	Logging() *log.Options
}

// Command is a subcommand. Run receives the context of the program; the
// options object has been loaded and validated when Run is called.
type Command struct {
	Name     string
	Short    string
	Run      func(ctx context.Context) error
	Commands []Command // nested subcommands
}

// App is a command-line program assembled from a name, an options object and
// subcommands.
type App struct {
	name     string
	opts     config.Options
	commands []Command
}

// New assembles an App. opts is the options object, a pointer to a struct;
// see the config package for how it is loaded. The name also forms the environment variable prefix:
// "my-app" reads MY_APP_LOG_LEVEL for the key log.level.
func New(name string, opts config.Options, commands ...Command) *App {
	return &App{name: name, opts: opts, commands: commands}
}

// Run executes the program with args (without the program name) and returns
// the process exit code. Errors are written to stderr.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.rootCommand()
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", a.name, err)
		return 1
	}
	return 0
}

func (a *App) rootCommand() *cobra.Command {
	var configFile string
	optionFlags := pflag.NewFlagSet(a.name, pflag.ContinueOnError)
	a.opts.AddFlags(optionFlags)

	root := &cobra.Command{
		Use:           a.name,
		SilenceErrors: true,
		SilenceUsage:  true,
		// Only the built-in and declared subcommands; no shell completion command.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.start(cmd, configFile, optionFlags)
		},
	}
	root.PersistentFlags().StringVarP(&configFile, "config", "c", "", "configuration file (yaml, json or toml)")
	root.PersistentFlags().AddFlagSet(optionFlags)

	root.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version and build information",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprint(cmd.OutOrStdout(), version.Get().String())
				return err
			},
		},
		configCommand(a.opts),
	)
	for _, c := range a.commands {
		root.AddCommand(cobraCommand(c))
	}
	return root
}

// start runs the pipeline steps between flag parsing and the subcommand.
func (a *App) start(cmd *cobra.Command, configFile string, optionFlags *pflag.FlagSet) error {
	src := config.Source{File: configFile, EnvPrefix: envPrefix(a.name), Flags: optionFlags}
	if err := config.Load(a.opts, src); err != nil {
		return err
	}
	logOpts := log.NewOptions()
	if l, ok := a.opts.(Logging); ok {
		logOpts = l.Logging()
	}
	logger, err := log.New(logOpts)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	logger.InfoContext(cmd.Context(), "starting",
		"app", a.name,
		"command", cmd.CommandPath(),
		"version", version.Get().Version,
		"config", config.Redacted(a.opts),
	)
	return nil
}

func configCommand(opts config.Options) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect the configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Print the effective configuration, secrets redacted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printJSON(cmd.OutOrStdout(), config.Redacted(opts))
		},
	})
	return cmd
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cobraCommand(c Command) *cobra.Command {
	cmd := &cobra.Command{Use: c.Name, Short: c.Short}
	if c.Run != nil {
		cmd.Args = cobra.NoArgs
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return c.Run(cmd.Context()) }
	}
	for _, sub := range c.Commands {
		cmd.AddCommand(cobraCommand(sub))
	}
	return cmd
}

func envPrefix(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}
