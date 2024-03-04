// Package ffauto provides shell tab-completion of subcommands, flags and
// arguments for Go programs written with [ffcli].
//
// The shell integration scripts have been extracted from Cobra
// (https://cobra.dev/), whose authors deserve most of the credit for this work.
// These shell completion functions invoke `$0 completion __complete -- ...`
// which is wired up to [Complete].
package ffauto

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffcli"
)

// Inject adds the '__command' and 'completion' subcommands to the root command
// which provide the user with shell scripts for calling `__command` to provide
// tab-completion suggestions.
//
// root.Name needs to match the command that the user is tab-completing for the
// shell script to work as expected by default.
func Inject(root *ffcli.Command, usageFunc func(*ffcli.Command) string) {
	root.Subcommands = append(
		root.Subcommands,
		&ffcli.Command{
			Name:      "completion",
			ShortHelp: "Shell tab-completion scripts.",

			Subcommands: []*ffcli.Command{
				{
					Name: "bash",
					Exec: func(ctx context.Context, args []string) error {
						_, err := fmt.Fprintf(
							os.Stdout, bashTemplate,
							root.Name, "completion __complete --",
							ShellCompDirectiveError, ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp,
							ShellCompDirectiveFilterFileExt, ShellCompDirectiveFilterDirs, ShellCompDirectiveKeepOrder,
							"_activeHelp_",
						)
						return err
					},
				},

				{
					Name:      "__complete",
					ShortHelp: "__complete provides autocomplete suggestions to interactive shells.",
					Exec: func(ctx context.Context, args []string) error {
						// Set up debug logging for the rest of this function call.
						if t := os.Getenv("BASH_COMP_DEBUG_FILE"); t != "" {
							tf, err := os.OpenFile(t, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
							if err != nil {
								return fmt.Errorf("opening debug file: %w", err)
							}
							defer func(origW io.Writer, origPrefix string, origFlags int) {
								log.SetOutput(origW)
								log.SetFlags(origFlags)
								log.SetPrefix(origPrefix)
								tf.Close()
							}(log.Writer(), log.Prefix(), log.Flags())
							log.SetOutput(tf)
							log.SetFlags(log.Lshortfile)
							log.SetPrefix("debug: ")
						}

						// Send back the results to the shell.
						words, dir, err := Complete(root, args)
						if err != nil {
							return err
						}
						for _, word := range words {
							fmt.Println(word)
						}
						fmt.Println(":" + strconv.Itoa(int(dir)))
						return nil
					},
				},
			},
		},
	)
}

// Complete returns the autocomplete suggestions for the root program and args.
//
// The returned words do not necessarily need to be prefixed with the last arg
// which is being completed. For example, '--bool-flag=' will have completions
// 'true' and 'false'.
//
// TODO: What's the behaviour if a command's FlagSet contains flag.ExitOnError?
func Complete(root *ffcli.Command, args []string) (words []string, dir ShellCompDirective, err error) {
	// Set up the arguments.
	if len(args) == 0 {
		args = []string{""}
	}
	completeArg := args[len(args)-1]
	completeFlag := completeArg == "" || strings.HasPrefix(completeArg, "-")

	// Replace the argument we're completing with '--' which we'll
	// check for later. If this '--' remains, there was another
	// preceding it, telling us that completeArg is not a flag.
	args[len(args)-1] = "--"

	// Traverse the command-tree to find the parent command whose
	// subcommand, flags, or arguments are being completed.
	parent := root
walk:
	for {
		log.Println("walk", parent.Name, args)
		if parent.FlagSet == nil {
			parent.FlagSet = flag.NewFlagSet(parent.Name, flag.ContinueOnError)
		}
		err := ff.Parse(parent.FlagSet, args, parent.Options...)
		if err != nil {
			return nil, 0, fmt.Errorf("%s flag parsing: %w", parent.Name, err)
		}

		args = parent.FlagSet.Args()
		if len(args) == 0 || (len(args) == 1 && args[0] == "--") {
			break
		}

		for _, sub := range parent.Subcommands {
			if strings.EqualFold(sub.Name, args[0]) {
				args = args[1:]
				parent = sub
				continue walk
			}
		}
		break
	}
	if len(args) > 0 && args[len(args)-1] == "--" {
		completeFlag = false
	}

	// TODO: '-flag arg...' -- Might need to `break walk` above when
	// args[len(args)-1] is a valid flag which requires an argument
	// but

	// Complete '-flag=...'.
	if completeFlag {
		if dashFlag, completeVal, ok := strings.Cut(completeArg, "="); ok {
			// Don't complete '-flag' later on as the
			// flag name is terminated by a '='.
			completeFlag = false

			_, flagName := cutDash(dashFlag)
			flag := parent.FlagSet.Lookup(flagName)
			if flag != nil {
				if isBoolFlag(flag) {
					// Complete true/false.
				opt:
					for _, vals := range [][]string{
						{"true", "TRUE", "True", "1"},
						{"false", "FALSE", "False", "0"},
					} {
						for _, val := range vals {
							if strings.HasPrefix(val, completeVal) {
								words = append(words, val)
								continue opt
							}
						}
					}
				} else if comp := completeFlags[flag]; comp != nil {
					// Complete custom completions.
					var err error
					words, dir, err = comp(completeVal)
					if err != nil {
						return nil, 0, fmt.Errorf("completing %s flag %s: %w", parent.Name, flag.Name, err)
					}
				}
			}
		}
	}

	// Complete '-flag...'.
	if completeFlag {
		used := make(map[string]struct{})
		parent.FlagSet.Visit(func(f *flag.Flag) {
			used[f.Name] = struct{}{}
		})

		cd, cf := cutDash(completeArg)
		parent.FlagSet.VisitAll(func(f *flag.Flag) {
			if !strings.HasPrefix(f.Name, cf) {
				return
			}
			// Skip flags already set by the user.
			if _, seen := used[f.Name]; seen {
				return
			}
			// Suggest single-dash '-v' for single-char flags and
			// double-dash '--verbose' for longer.
			d := cd
			if (d == "" || d == "-") && cf == "" && len(f.Name) > 1 {
				d = "--"
			}
			words = append(words, d+f.Name)
		})
	}

	if len(parent.Subcommands) > 0 {
		// Complete 'sub...'.
		for _, sub := range parent.Subcommands {
			if strings.HasPrefix(sub.Name, completeArg) {
				words = append(words, sub.Name)
			}
		}
	}

	return words, dir, nil
}

func cutDash(s string) (dashes, flag string) {
	if strings.HasPrefix(s, "-") {
		if strings.HasPrefix(s[1:], "-") {
			return "--", s[2:]
		}
		return "-", s[1:]
	}
	return "", s
}

var completeFlags map[*flag.Flag]CompleteFunc

// Flag registers a completion function for the flag in fs with given name.
//
// comp will be called to return suggestions when the user tries to tab-complete
// '--name=<TAB>' or '--name <TAB>' for the commands using fs.
func Flag(fs *flag.FlagSet, name string, comp CompleteFunc) {
	f := fs.Lookup(name)
	if f == nil {
		panic(fmt.Errorf("CompleteFlag: flag %s not found", name))
	}
	if completeFlags == nil {
		completeFlags = make(map[*flag.Flag]CompleteFunc)
	}
	completeFlags[f] = comp
}

var completeCmds map[*ffcli.Command]CompleteFunc

// Arg registers a completion function for the args of cmd.
//
// comp will be called to return suggestions when the user tries to tab-complete
// `prog <TAB>` or `prog subcmd arg1 <TAB>`, for example.
func Arg(cmd *ffcli.Command, comp CompleteFunc) {
	completeCmds[cmd] = comp
}

// FIXME: taking a single word makes sense for flags, but for args the value
// being completed may depend on the preceding arguments, and maybe we should
// pass those through too...
type CompleteFunc func(word string) ([]string, ShellCompDirective, error)

// Fixed returns a CompleteFunc which suggests the given words.
func Fixed(dir ShellCompDirective, words ...string) CompleteFunc {
	return func(prefix string) ([]string, ShellCompDirective, error) {
		matches := make([]string, 0, len(words))
		for _, word := range words {
			if strings.HasPrefix(word, prefix) {
				matches = append(matches, word)
			}
		}
		return matches, dir, nil
	}
}

// FilesWithExtensions returns a CompleteFunc that tells the shell to limit file
// suggestions to those with the given extensions.
func FilesWithExtensions(exts ...string) CompleteFunc {
	return Fixed(ShellCompDirectiveFilterFileExt, exts...)
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface {
		IsBoolFlag() bool
	})
	return ok && bf.IsBoolFlag()
}
