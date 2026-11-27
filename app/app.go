// Package app is the start-up pipeline of corekit: it turns an application
// name, an options object and a set of subcommands into a command-line program.
//
// Every subcommand runs the same fixed pipeline: parse flags → load the
// configuration → decode it into the options object → complete → validate →
// initialise logging → log the version and the redacted effective
// configuration → run the subcommand. The built-in `version` and
// `config print` subcommands are always present; they are operations
// subcommands, so the pipeline logs to stderr for them and their stdout
// holds only their result. Other subcommands log to log.output.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	Name  string
	Short string
	Run   func(ctx context.Context) error
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
		root.PrintErrf("%s: %v\n", a.name, err)
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
			return a.start(cmd.Context(), cmd, configFile, optionFlags)
		},
	}
	root.PersistentFlags().StringVarP(&configFile, "config", "c", "", "configuration file (yaml, json or toml)")
	root.PersistentFlags().AddFlagSet(optionFlags)

	root.AddCommand(
		&cobra.Command{
			Use:         "version",
			Short:       "Print the version and build information",
			Args:        cobra.NoArgs,
			Annotations: opsAnnotation,
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
func (a *App) start(ctx context.Context, cmd *cobra.Command, configFile string, optionFlags *pflag.FlagSet) error {
	src := config.Source{File: configFile, EnvPrefix: envPrefix(a.name), Flags: optionFlags}
	if err := config.Load(ctx, a.opts, src); err != nil {
		return err
	}
	logOpts := *log.NewOptions()
	if l, ok := a.opts.(Logging); ok {
		logOpts = *l.Logging()
	}
	if isOps(cmd) {
		logOpts.Output = "stderr"
	}
	logger, err := log.New(&logOpts)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	logger.InfoContext(ctx, "starting",
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
		Use:         "print",
		Short:       "Print the effective configuration, secrets redacted",
		Args:        cobra.NoArgs,
		Annotations: opsAnnotation,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printJSON(cmd.Context(), cmd.OutOrStdout(), config.Redacted(opts))
		},
	})
	return cmd
}

// opsAnnotation marks an operations subcommand, whose stdout is its result.
var opsAnnotation = map[string]string{"corekit.ops": "true"}

func isOps(cmd *cobra.Command) bool { return cmd.Annotations["corekit.ops"] == "true" }

// printJSON writes v to w as indented JSON unless ctx is already done.
func printJSON(ctx context.Context, w io.Writer, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cobraCommand(c Command) *cobra.Command {
	return &cobra.Command{
		Use:   c.Name,
		Short: c.Short,
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return c.Run(cmd.Context()) },
	}
}

func envPrefix(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}
