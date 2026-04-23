package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
	"github.com/spf13/cobra"
)

// LogoutCmd returns the `apl logout <handle>` command.
func LogoutCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout <handle>",
		Short: "Revoke and delete the stored record for a handle.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
					return authErr("no account for %s", h.String())
				}
				return netErr("store get: %s", err.Error())
			}
			// Best-effort provider revoke.
			if rerr := p.Logout(cmd.Context(), rec); rerr != nil {
				fmt.Fprintf(stderr, "warning: provider revoke failed (%s); local record removed\n", rerr.Error())
			}
			if err := st.Delete(cmd.Context(), h.String()); err != nil {
				return netErr("store delete: %s", err.Error())
			}
			fmt.Fprintf(stdout, "Removed %s\n", h.String())
			return nil
		},
	}
	_ = reg
	_ = provider.ErrUnknownProvider
	return cmd
}
