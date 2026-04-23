package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/muthuishere/all-purpose-login/internal/oauth"
	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
	"github.com/spf13/cobra"
)

// TokenCmd returns the `apl token <handle> --scope <scope>` command.
func TokenCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "token <handle>",
		Short: "Print the access token for a scoped handle (pipe-safe).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scope == "" {
				return userErr("--scope is required")
			}
			h, err := ParseHandle(args[0])
			if err != nil {
				return userErr("%s", err.Error())
			}
			if err := ValidateProvider(h, reg); err != nil {
				return userErr("%s", err.Error())
			}
			p, _ := reg.Get(h.Provider)
			rec, err := st.Get(cmd.Context(), h.String())
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return authErr("no account for %s. Run: apl login %s", h.String(), h.String())
				}
				return netErr("store get: %s", err.Error())
			}
			tok, updated, err := p.Token(cmd.Context(), rec, scope)
			if err != nil {
				if errors.Is(err, provider.ErrScopeNotGranted) {
					return authErr("scope %q not granted for %s. Run: apl login %s --scope %s --force",
						scope, h.String(), h.String(), scope)
				}
				if errors.Is(err, oauth.ErrInvalidGrant) {
					return authErr("token refresh failed: refresh token expired. Run: apl login %s", h.String())
				}
				return netErr("token: %s", err.Error())
			}
			if updated != nil && updated != rec {
				if perr := st.Put(cmd.Context(), updated); perr != nil {
					fmt.Fprintf(stderr, "warning: failed to persist refreshed token: %s\n", perr.Error())
				}
			}
			fmt.Fprintln(stdout, tok)
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope alias (required)")
	return cmd
}
