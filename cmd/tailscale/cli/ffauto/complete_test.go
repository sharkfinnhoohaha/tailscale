package ffauto_test

import (
	_ "embed"
	"flag"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/peterbourgon/ff/v3/ffcli"
	"tailscale.com/cmd/tailscale/cli/ffauto"
)

func newFlagSet(name string, errh flag.ErrorHandling, flags func(fs *flag.FlagSet)) *flag.FlagSet {
	fs := flag.NewFlagSet(name, errh)
	if flags != nil {
		flags(fs)
	}
	return fs
}

func TestComplete(t *testing.T) {
	t.Parallel()

	// Build our test program in testdata.
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
					fs.Bool("debug-bool", false, "debug bool")
					fs.String("enum", "", "a flag that takes several specific values")
					ffauto.Flag(fs, "enum", ffauto.Fixed(ffauto.ShellCompDirectiveNoFileComp, "alpha", "beta", "charlie"))
				}),
			},
		},
	}

	tests := []struct {
		args     []string
		wantComp []string
		wantDir  ffauto.ShellCompDirective
	}{
		{
			args:     []string{"deb"},
			wantComp: []string{"debug"},
		},
		{
			args:     []string{"-"},
			wantComp: []string{"--root-bool", "--root-str", "-v"},
		},
		{
			args:     []string{"--"},
			wantComp: []string{"--root-bool", "--root-str", "--v"},
		},
		{
			args:     []string{"-r"},
			wantComp: []string{"-root-bool", "-root-str"},
		},
		{
			args:     []string{"--r"},
			wantComp: []string{"--root-bool", "--root-str"},
		},
		{
			args:     []string{"--root-str=s", "--r"},
			wantComp: []string{"--root-bool"}, // omits --root-str which is already set
		},
		{
			args:     []string{"--root-str", "--", "--r"},
			wantComp: []string{"--root-bool"},
		},
		{
			// "--" disables flag parsing, so we shouldn't suggest flags.
			args:     []string{"--", "--root"},
			wantComp: nil,
		},
		{
			// "--" here is a flag value, so doesn't disable flag parsing.
			args:     []string{"--root-str", "--", "--root"},
			wantComp: []string{"--root-bool"},
		},
		{
			// Equivalent to {"--root-str=--", "--", "--r"} meaning "--r" is not
			// a flag because it's preceded by a "--" argument:
			// https://go.dev/play/p/UCtftQqVhOD.
			args:     []string{"--root-str", "--", "--", "--r"},
			wantComp: nil,
		},
		{
			args:     []string{"--root-bool="},
			wantComp: []string{"true", "false"},
		},
		{
			args:     []string{"--root-bool=t"},
			wantComp: []string{"true"},
		},
		{
			args:     []string{"--root-bool=T"},
			wantComp: []string{"TRUE"},
		},
		{
			args:     []string{"debug", "--de"},
			wantComp: []string{"--debug-bool"},
		},
		{
			args:     []string{"debug", "--enum="},
			wantComp: []string{"alpha", "beta", "charlie"},
			wantDir:  ffauto.ShellCompDirectiveNoFileComp,
		},
		{
			args:     []string{"debug", "--enum=al"},
			wantComp: []string{"alpha"},
			wantDir:  ffauto.ShellCompDirectiveNoFileComp,
		},
	}

	// Run the tests.
	for _, test := range tests {
		test := test
		t.Run(strings.Join(test.args, "␣"), func(t *testing.T) {
			// Capture the binary
			complete, dir, err := ffauto.Complete(root, test.args)
			if err != nil {
				t.Fatalf("completion error: %s", err)
			}

			// Test the results match our expectation.
			if test.wantComp != nil {
				if diff := cmp.Diff(test.wantComp, complete); diff != "" {
					t.Errorf("unexpected completion directives (-want +got):\n%s", diff)
				}
			}
			if test.wantDir != dir {
				t.Errorf("got shell completion directive %[1]d (%[1]s), want %[2]d (%[2]s)", dir, test.wantDir)
			}
		})
	}
}
