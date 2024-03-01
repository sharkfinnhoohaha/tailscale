// Program autocomplete_prog is a test autocomplete program for assessing the
// behaviour of tab-completion results.
//
// This is a separate program so that we can test the behaviour of
// flag.ExitOnError which calls os.Exit in the face of flag-parsing errors.
package main

import (
	"context"
	"flag"
	"os"

	"github.com/peterbourgon/ff/v3/ffcli"
	"tailscale.com/cmd/tailscale/cli"
)

func newFlagSet(name string, errh flag.ErrorHandling, flags func(fs *flag.FlagSet)) *flag.FlagSet {
	fs := flag.NewFlagSet(name, errh)
	if flags != nil {
		flags(fs)
	}
	return fs
}

func main() {
	root := &ffcli.Command{
		Name: "prog",
		FlagSet: newFlagSet("prog", flag.ContinueOnError, func(fs *flag.FlagSet) {
			fs.Bool("v", false, "verbose")
			fs.Bool("root-bool", false, "root `bool`")
			fs.String("root-str", "", "some `text`")
		}),
		Subcommands: []*ffcli.Command{
			{
				Name: "debug",
				FlagSet: newFlagSet("prog debug", flag.ContinueOnError, func(fs *flag.FlagSet) {
					fs.String("cpu-profile", "", "write cpu profile to `file`")
				}),
			},
		},
	}
	cli.InjectAutocomplete(root)
	root.ParseAndRun(context.Background(), os.Args[1:])
}
