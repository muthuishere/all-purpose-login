package cli

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/spf13/cobra"
)

// ScopesCmd returns the `apl scopes <provider>` command.
func ScopesCmd(reg *provider.Registry, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scopes <provider>",
		Short: "List scope aliases for a provider.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, err := reg.Get(name); err != nil {
				return userErr(`unknown provider %q. Known providers: %v`, name, reg.Names())
			}
			var aliases map[string]string
			switch name {
			case "google":
				aliases = googleAliasesView()
			case "ms":
				aliases = microsoftAliasesView()
			default:
				return userErr(`unknown provider %q`, name)
			}
			keys := make([]string, 0, len(aliases))
			for k := range aliases {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
			for _, k := range keys {
				fmt.Fprintf(tw, "%s\t%s\n", k, aliases[k])
			}
			return tw.Flush()
		},
	}
	return cmd
}

// googleAliasesView returns a snapshot copy so the scopes command can iterate
// without exposing provider internals.
func googleAliasesView() map[string]string {
	return provider.GoogleScopeAliases()
}

func microsoftAliasesView() map[string]string {
	return provider.MicrosoftScopeAliases()
}
