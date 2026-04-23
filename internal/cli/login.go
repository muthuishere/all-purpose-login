package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
	"github.com/spf13/cobra"
)

// LoginCmd returns the `apl login <handle>` command.
func LoginCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
	var tenant string
	var scopes []string
	var force bool

	cmd := &cobra.Command{
		Use:   "login <handle>",
		Short: "Authenticate a provider:label account and store tokens.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := ParseHandle(args[0])
			if err != nil {
				return userErr("%s", err.Error())
			}
			if err := ValidateProvider(h, reg); err != nil {
				return userErr("%s", err.Error())
			}
			if tenant != "" && h.Provider != "ms" {
				return userErr("--tenant is only valid for the ms provider")
			}
			p, _ := reg.Get(h.Provider)

			// Expand aliases.
			expanded := scopes
			if len(scopes) > 0 {
				exp, err := p.ExpandScopes(scopes)
				if err != nil {
					return userErr("%s", err.Error())
				}
				expanded = exp
			}

			opts := provider.LoginOpts{
				Tenant: tenant,
				Scopes: expanded,
				Force:  force,
			}

			rec, err := p.Login(cmd.Context(), h.Label, opts)
			if err != nil {
				if errors.Is(err, provider.ErrNoClientID) {
					return userErr("provider %q not configured. Run: apl setup %s", h.Provider, h.Provider)
				}
				return netErr("login failed: %s", err.Error())
			}
			if err := st.Put(cmd.Context(), rec); err != nil {
				return netErr("store record: %s", err.Error())
			}
			fmt.Fprintf(stdout, "Signed in as %s (handle: %s)\n", rec.Subject, rec.HandleString())
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Microsoft tenant id (ms provider only)")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "scope alias; repeatable")
	cmd.Flags().BoolVar(&force, "force", false, "force re-consent even if tokens exist")
	return cmd
}
