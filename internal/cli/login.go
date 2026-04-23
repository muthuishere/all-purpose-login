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

// LoginCmd returns the `apl login <handle>` command.
//
// Dual-mode:
//   - If no record exists, or --force is passed, run the full browser OAuth
//     flow, persist the record, and print the access token to stdout.
//   - If a record exists and the cached access token is still valid (>30s
//     headroom), print the cached token.
//   - If a record exists but the access token is stale, refresh via
//     refresh_token, persist, and print the new token.
//
// stdout: access token + trailing newline ONLY.
// stderr: all human-readable status ("Signed in as …", etc.).
func LoginCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
	var tenant string
	var scopes []string
	var force bool

	cmd := &cobra.Command{
		Use:   "login <handle>",
		Short: "Authenticate and print an access token for provider:label.",
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

			// Fast path: existing record, no --force, no --scope override.
			if !force && len(scopes) == 0 {
				rec, gerr := st.Get(cmd.Context(), h.String())
				if gerr == nil {
					tok, updated, rerr := p.Refresh(cmd.Context(), rec)
					if rerr != nil {
						if errors.Is(rerr, oauth.ErrInvalidGrant) {
							return authErr("token refresh failed: refresh token expired. Run: apl login %s --force", h.String())
						}
						return netErr("token refresh: %s", rerr.Error())
					}
					if updated != nil && updated != rec {
						if perr := st.Put(cmd.Context(), updated); perr != nil {
							fmt.Fprintf(stderr, "warning: failed to persist refreshed token: %s\n", perr.Error())
						}
					}
					fmt.Fprintln(stdout, tok)
					return nil
				} else if !errors.Is(gerr, store.ErrNotFound) {
					return netErr("store get: %s", gerr.Error())
				}
				// fall through to browser flow
			}

			// Scope handling: if user passed --scope, they've taken over
			// (plus OIDC merged by the provider). Otherwise defaults apply.
			var reqScopes []string
			if len(scopes) > 0 {
				exp, err := p.ExpandScopes(scopes)
				if err != nil {
					return userErr("%s", err.Error())
				}
				reqScopes = exp
			}

			opts := provider.LoginOpts{
				Tenant: tenant,
				Scopes: reqScopes,
				Force:  force,
			}

			fmt.Fprintf(stderr, "→ Opening browser for %s sign-in…\n", h.Provider)
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
			fmt.Fprintf(stderr, "✓ Signed in as %s (handle: %s)\n", rec.Subject, rec.HandleString())
			fmt.Fprintln(stdout, rec.AccessToken)
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Microsoft tenant id (ms provider only)")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "scope alias; repeatable (overrides provider defaults)")
	cmd.Flags().BoolVar(&force, "force", false, "force re-consent even if tokens exist")
	return cmd
}
