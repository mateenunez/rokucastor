package cli

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/stupside/rokucastor/internal/device"
	"github.com/stupside/rokucastor/internal/roku"
)

// discoveryBudget covers SSDP's search window plus the device-info lookups.
const discoveryBudget = 20 * time.Second

func devicesCommand() *cli.Command {
	return &cli.Command{
		Name:    "devices",
		Aliases: []string{"ls"},
		Usage:   "List the Rokus found on the local network",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ctx, cancel := context.WithTimeout(ctx, discoveryBudget)
			defer cancel()

			out := cmd.Root().Writer
			fmt.Fprintln(out, "Searching for Rokus on the local network...")
			found := roku.Family{}.Discover(ctx)
			if len(found) == 0 {
				fmt.Fprintln(out, "No Rokus found. Check that the Roku is on and on the same network.")
				return nil
			}

			slices.SortFunc(found, func(a, b device.Info) int {
				return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Address, b.Address))
			})
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tADDRESS")
			for _, info := range found {
				fmt.Fprintf(w, "%s\t%s\n", info.Name, displayAddress(info.Address))
			}
			return w.Flush()
		},
	}
}

func displayAddress(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return address
	}
	return u.Host
}
