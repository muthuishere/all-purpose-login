package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/muthuishere/all-purpose-login/internal/cli"
	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
	"github.com/muthuishere/all-purpose-login/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "apl",
		Short:         "all-purpose-login — unified OAuth token broker for Google and Microsoft",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("apl %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		},
	})

	// setup command (owned by setup-agent)
	root.AddCommand(cli.NewSetupCommand())

	// Config is optional for version/scopes/accounts/setup; commands that need
	// a client_id will error at Login/Token time via provider.ErrNoClientID.
	cfg, cerr := config.Load()
	if cerr != nil && !errors.Is(cerr, config.ErrNotConfigured) {
		fmt.Fprintln(os.Stderr, cerr)
		os.Exit(1)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	st, sterr := store.New()
	if sterr != nil {
		fmt.Fprintln(os.Stderr, sterr)
		os.Exit(1)
	}

	reg, _ := provider.DefaultRegistry(cfg)

	root.AddCommand(cli.LoginCmd(reg, st, os.Stdout, os.Stderr))
	root.AddCommand(cli.LogoutCmd(reg, st, os.Stdout, os.Stderr))
	root.AddCommand(cli.AccountsCmd(reg, st, os.Stdout, os.Stderr))
	root.AddCommand(cli.ScopesCmd(reg, os.Stdout, os.Stderr))
	root.AddCommand(cli.CallCmd(reg, st, os.Stdout, os.Stderr))

	if err := root.Execute(); err != nil {
		var ce *cli.CLIError
		if errors.As(err, &ce) {
			fmt.Fprintln(os.Stderr, ce.Msg)
			os.Exit(ce.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
