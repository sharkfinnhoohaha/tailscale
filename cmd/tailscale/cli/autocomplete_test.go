package cli_test

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"tailscale.com/cmd/tailscale/cli"
)

// For cache-busting go test.
//
//go:embed testdata/autocomplete_prog.go
var _ string

func TestInjectAutocomplete(t *testing.T) {
	t.Parallel()

	// Build our test program in testdata.
	exe := filepath.Join(t.TempDir(), "autocomplete-prog")
	build := exec.Command("go", "build", "-o", exe, "./testdata/autocomplete_prog.go")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	err := build.Run()
	if err != nil {
		t.Fatalf("failed building testdata/autocomplete_prog.go: %s", err)
	}

	// Test cases.
	tests := []struct {
		args         []string
		wantComp     []string
		wantDir      cli.ShellCompDirective
		wantInStdout string
		wantInStderr string
	}{
		{
			args:     []string{"__complete", "--", "deb"},
			wantComp: []string{"debug"},
			wantDir:  cli.ShellCompDirectiveNoFileComp,
		},
		{
			args:     []string{"__complete", "--", "-"},
			wantComp: []string{"--root-bool", "--root-str", "-v"},
			wantDir:  cli.ShellCompDirectiveNoFileComp,
		},
		{
			args:     []string{"__complete", "--", "--"},
			wantComp: []string{"--root-bool", "--root-str", "--v"},
			wantDir:  cli.ShellCompDirectiveNoFileComp,
		},
		{
			args:     []string{"__complete", "--", "-r"},
			wantComp: []string{"-root-bool", "-root-str"},
			wantDir:  cli.ShellCompDirectiveNoFileComp,
		},
		{
			args:     []string{"__complete", "--", "--r"},
			wantComp: []string{"--root-bool", "--root-str"},
			wantDir:  cli.ShellCompDirectiveNoFileComp,
		},
	}

	// Run the tests.
	for _, test := range tests {
		test := test
		t.Run(strings.Join(test.args, "␣"), func(t *testing.T) {
			t.Parallel()

			// Capture the binary
			cmd := exec.Command(exe, test.args...)
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			var stderr bytes.Buffer
			cmd.Stderr = &stderr

			// Run it.
			err := cmd.Run()
			var debug strings.Builder
			fmt.Fprintf(&debug, "Run: %s\n", cmd)
			if stdout.Len() > 0 {
				fmt.Fprintf(&debug, "Stdout:\n\t%s\n", strings.ReplaceAll(stdout.String(), "\n", "\n\t"))
			}
			if stderr.Len() > 0 {
				fmt.Fprintf(&debug, "Stderr:\n\t%s\n", strings.ReplaceAll(stderr.String(), "\n", "\n\t"))
			}
			t.Log(strings.TrimSpace(debug.String()))
			if err != nil {
				t.Fatalf("run failed: %s", err)
			}

			// Test the output contained what we expected.
			if !bytes.Contains(stdout.Bytes(), []byte(test.wantInStdout)) {
				t.Errorf("stdout did not contain %q", test.wantInStdout)
			}
			if !bytes.Contains(stderr.Bytes(), []byte(test.wantInStderr)) {
				t.Errorf("stderr did not contain %q", test.wantInStderr)
			}

			// Parse the completion results.
			var dir cli.ShellCompDirective
			complete := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
			if len(complete) > 0 && strings.HasPrefix(complete[len(complete)-1], ":") {
				n, err := strconv.Atoi(complete[len(complete)-1][1:])
				if err != nil {
					t.Fatalf("failed to parse completion directive: %s", err)
				}
				dir = cli.ShellCompDirective(n)
				complete = complete[:len(complete)-1]
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
