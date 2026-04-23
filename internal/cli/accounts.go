package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
	"github.com/spf13/cobra"
)

// accountJSON is the stable JSON shape for `apl accounts --json`.
type accountJSON struct {
	Provider  string    `json:"provider"`
	Label     string    `json:"label"`
	Handle    string    `json:"handle"`
	Email     string    `json:"email"`
	Tenant    *string   `json:"tenant"`
	Stored    string    `json:"stored"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AccountsCmd returns the `apl accounts [--json]` command.
func AccountsCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "List stored accounts.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			recs, err := st.List(cmd.Context())
			if err != nil {
				return netErr("store list: %s", err.Error())
			}
			sort.Slice(recs, func(i, j int) bool {
				if recs[i].Provider != recs[j].Provider {
					return recs[i].Provider < recs[j].Provider
				}
				return recs[i].Label < recs[j].Label
			})
			if asJSON {
				out := make([]accountJSON, 0, len(recs))
				for _, r := range recs {
					var tenant *string
					if r.Tenant != "" {
						t := r.Tenant
						tenant = &t
					}
					out = append(out, accountJSON{
						Provider:  r.Provider,
						Label:     r.Label,
						Handle:    r.HandleString(),
						Email:     r.Subject,
						Tenant:    tenant,
						Stored:    "keychain",
						Scopes:    r.Scopes,
						ExpiresAt: r.ExpiresAt,
					})
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			if len(recs) == 0 {
				fmt.Fprintln(stdout, "No accounts. Run: apl login <provider>:<label>")
				return nil
			}
			showTenant := false
			for _, r := range recs {
				if r.Tenant != "" {
					showTenant = true
					break
				}
			}
			tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
			if showTenant {
				fmt.Fprintln(tw, "PROVIDER\tLABEL\tEMAIL\tTENANT\tSTORED")
			} else {
				fmt.Fprintln(tw, "PROVIDER\tLABEL\tEMAIL\tSTORED")
			}
			for _, r := range recs {
				if showTenant {
					t := r.Tenant
					if t == "" {
						t = "-"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Provider, r.Label, r.Subject, t, "keychain")
				} else {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Provider, r.Label, r.Subject, "keychain")
				}
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON array to stdout")
	return cmd
}
