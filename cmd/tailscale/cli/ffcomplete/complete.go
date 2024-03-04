// Package ffcomplete provides shell tab-completion of subcommands, flags and
// arguments for Go programs written with [ffcli].
//
// The shell integration scripts have been extracted from Cobra
// (https://cobra.dev/), whose authors deserve most of the credit for this work.
// These shell completion functions invoke `$0 completion __complete -- ...`
// which is wired up to [Complete].
package ffcomplete

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
	const (
		compCmd          = "completion __complete --"
		activeHelpEnvvar = "_activeHelp_" // FIXME what should this be?
	)

	nameForVar := root.Name
	nameForVar = strings.ReplaceAll(nameForVar, "-", "_")
	nameForVar = strings.ReplaceAll(nameForVar, ":", "_")

	root.Subcommands = append(
		root.Subcommands,
		&ffcli.Command{
			Name:      "completion",
			ShortHelp: "Shell tab-completion scripts.",
			LongHelp:  fmt.Sprintf(usageTemplate, root.Name),

			// Print help if run without args.
			Exec: func(ctx context.Context, args []string) error { return flag.ErrHelp },

			// Omit the '__complete' subcommand from the 'completion' help.
			UsageFunc: func(c *ffcli.Command) string {
				// Filter the subcommands to omit '__complete'.
				s := make([]*ffcli.Command, 0, len(c.Subcommands))
				for _, sub := range c.Subcommands {
					if !strings.HasPrefix(sub.Name, "__") {
						s = append(s, sub)
					}
				}

				// Swap in the filtered subcommands list for the rest of the call.
				defer func(r []*ffcli.Command) { c.Subcommands = r }(c.Subcommands)
				c.Subcommands = s

				// Render the usage.
				if usageFunc == nil {
					return ffcli.DefaultUsageFunc(c)
				}
				return usageFunc(c)
			},

			Subcommands: []*ffcli.Command{
				// Subcommands for generating shell integration scripts.
				{
					Name:       "bash",
					ShortHelp:  "Generate bash shell completion script.",
					ShortUsage: ". <( " + root.Name + " completion bash )",
					UsageFunc:  usageFunc,
					Exec: func(ctx context.Context, args []string) error {
						_, err := fmt.Fprintf(
							os.Stdout, bashTemplate,
							root.Name, compCmd,
							ShellCompDirectiveError, ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp,
							ShellCompDirectiveFilterFileExt, ShellCompDirectiveFilterDirs, ShellCompDirectiveKeepOrder,
							activeHelpEnvvar,
						)
						return err
					},
				},
				{
					Name:       "zsh",
					ShortHelp:  "Generate zsh shell completion script.",
					ShortUsage: ". <( " + root.Name + " completion zsh )",
					UsageFunc:  usageFunc,
					Exec: func(ctx context.Context, args []string) error {
						_, err := fmt.Fprintf(
							os.Stdout, zshTemplate,
							root.Name, compCmd,
							ShellCompDirectiveError, ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp,
							ShellCompDirectiveFilterFileExt, ShellCompDirectiveFilterDirs, ShellCompDirectiveKeepOrder,
							activeHelpEnvvar,
						)
						return err
					},
				},
				{
					Name:       "fish",
					ShortHelp:  "Generate fish shell completion script.",
					ShortUsage: root.Name + " completion fish | source",
					UsageFunc:  usageFunc,
					Exec: func(ctx context.Context, args []string) error {
						_, err := fmt.Fprintf(
							os.Stdout, fishTemplate,
							nameForVar, root.Name, compCmd,
							ShellCompDirectiveError, ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp,
							ShellCompDirectiveFilterFileExt, ShellCompDirectiveFilterDirs, ShellCompDirectiveKeepOrder, activeHelpEnvvar,
						)
						return err
					},
				},
				{
					Name:       "powershell",
					ShortHelp:  "Generate powershell completion script.",
					ShortUsage: root.Name + " completion powershell | Out-String | Invoke-Expression",
					UsageFunc:  usageFunc,
					Exec: func(ctx context.Context, args []string) error {
						_, err := fmt.Fprintf(
							os.Stdout, powershellTemplate,
							root.Name, nameForVar, compCmd,
							ShellCompDirectiveError, ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp,
							ShellCompDirectiveFilterFileExt, ShellCompDirectiveFilterDirs, ShellCompDirectiveKeepOrder,
							activeHelpEnvvar,
						)
						return err
					},
				},

				// Subcommand which generates the shell completion arguments.
				{
					Name:      "__complete",
					ShortHelp: "__complete provides autocomplete suggestions to interactive shells.",
					UsageFunc: usageFunc,
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
	// Explicitly log panics.
	defer func() {
		if r := recover(); r != nil {
			if rerr, ok := err.(error); ok {
				err = fmt.Errorf("panic: %w", rerr)
			} else {
				err = fmt.Errorf("panic: %v", r)
			}
		}
	}()

	// Set up the arguments.
	if len(args) == 0 {
		args = []string{""}
	}

	// Completion criteria.
	completeArg := args[len(args)-1]
	args = args[:len(args)-1]
	emitFlag := true
	emitArgs := true

	// Traverse the command-tree to find the cmd command whose
	// subcommand, flags, or arguments are being completed.
	cmd := root
walk:
	for {
		// Ensure there's a flagset with ContinueOnError set.
		if cmd.FlagSet == nil {
			cmd.FlagSet = flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
		}
		cmd.FlagSet.Init(cmd.FlagSet.Name(), flag.ContinueOnError)

		// Manually split the args so we know when we're completing flags/args.
		flagArgs, argArgs, flagNeedingValue := splitFlagArgs(cmd.FlagSet, args)
		if flagNeedingValue != "" {
			completeArg = flagNeedingValue + "=" + completeArg
		}
		args = argArgs

		// Parse the arguments.
		err := ff.Parse(cmd.FlagSet, flagArgs, cmd.Options...)
		if err != nil {
			return nil, 0, fmt.Errorf("%s flag parsing: %w", cmd.Name, err)
		}
		if cmd.FlagSet.NArg() > 0 {
			// This shouldn't happen if splitFlagArgs is accurately finding the
			// split between flags and args.
			_ = false
		}
		if len(args) == 0 {
			break
		}

		for _, sub := range cmd.Subcommands {
			if strings.EqualFold(sub.Name, args[0]) {
				args = args[1:]
				cmd = sub
				continue walk
			}
		}
		break
	}
	if len(args) > 0 {
		emitFlag = false
	}

	// Complete '-flag=...'.
	if emitFlag && strings.HasPrefix(completeArg, "-") && strings.Contains(completeArg, "=") {
		// Don't complete '-flag' later on as the
		// flag name is terminated by a '='.
		emitFlag = false
		emitArgs = false

		dashFlag, completeVal, _ := strings.Cut(completeArg, "=")
		_, f := cutDash(dashFlag)
		flag := cmd.FlagSet.Lookup(f)
		if flag != nil {
			if comp := completeFlags[flag]; comp != nil {
				// Complete custom completions.
				var err error
				words, dir, err = comp(completeVal)
				if err != nil {
					return nil, 0, fmt.Errorf("completing %s flag %s: %w", cmd.Name, flag.Name, err)
				}
			} else if isBoolFlag(flag) {
				// Complete true/false.
				for _, vals := range [][]string{
					{"true", "TRUE", "True", "1"},
					{"false", "FALSE", "False", "0"},
				} {
					for _, val := range vals {
						if strings.HasPrefix(val, completeVal) {
							words = append(words, val)
							break
						}
					}
				}
			}
		}
	}

	// Complete '-flag...'.
	if emitFlag {
		used := make(map[string]struct{})
		cmd.FlagSet.Visit(func(f *flag.Flag) {
			used[f.Name] = struct{}{}
		})

		cd, cf := cutDash(completeArg)
		cmd.FlagSet.VisitAll(func(f *flag.Flag) {
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

	// Complete 'sub...'.
	if emitArgs {
		for _, sub := range cmd.Subcommands {
			if strings.HasPrefix(sub.Name, completeArg) {
				words = append(words, sub.Name)
			}
		}

		if comp := completeCmds[cmd]; comp != nil {
			w, d, err := comp(completeArg)
			if err != nil {
				return nil, 0, fmt.Errorf("completing %s args: %w", cmd.Name, err)
			}
			dir = d
			words = append(words, w...)
		}
	}
	return words, dir, nil
}

// splitFlagArgs separates a list of command-line arguments into arguments
// comprising flags and their values, preceding arguments to be passed to the
// command. This follows the stdlib 'flag' parsing conventions. If the final
// argument is a flag name which takes a value but has no value specified, it is
// omitted from flagArgs and argArgs and instead returned in needValue.
func splitFlagArgs(fs *flag.FlagSet, args []string) (flagArgs, argArgs []string, flagNeedingValue string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return args[:i], args[i+1:], ""
		}

		d, f := cutDash(a)
		if d == "" {
			return args[:i], args[i:], ""
		}
		if strings.Contains(f, "=") {
			continue
		}

		flag := fs.Lookup(f)
		if flag == nil {
			return args[:i], args[i:], ""
		}
		if isBoolFlag(flag) {
			continue
		}

		// Consume an extra argument for the flag value.
		if i == len(args)-1 {
			return args[:i], nil, args[i]
		}
		i++
	}
	return args, nil, ""
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

// Args registers a completion function for the args of cmd.
//
// comp will be called to return suggestions when the user tries to tab-complete
// `prog <TAB>` or `prog subcmd arg1 <TAB>`, for example.
func Args(cmd *ffcli.Command, comp CompleteFunc) *ffcli.Command {
	if completeCmds == nil {
		completeCmds = make(map[*ffcli.Command]CompleteFunc)
	}
	completeCmds[cmd] = comp
	return cmd
}

// FIXME: taking a single word makes sense for flags, but for args the value
// being completed may depend on the preceding arguments, and maybe we should
// pass those through too...
type CompleteFunc func(word string) ([]string, ShellCompDirective, error)

// Fixed returns a CompleteFunc which suggests the given words.
func Fixed(words ...string) CompleteFunc {
	return func(prefix string) ([]string, ShellCompDirective, error) {
		matches := make([]string, 0, len(words))
		for _, word := range words {
			if strings.HasPrefix(word, prefix) {
				matches = append(matches, word)
			}
		}
		return matches, ShellCompDirectiveNoFileComp, nil
	}
}

// FilesWithExtensions returns a CompleteFunc that tells the shell to limit file
// suggestions to those with the given extensions.
func FilesWithExtensions(exts ...string) CompleteFunc {
	return func(word string) ([]string, ShellCompDirective, error) {
		return exts, ShellCompDirectiveFilterFileExt, nil
	}
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface {
		IsBoolFlag() bool
	})
	return ok && bf.IsBoolFlag()
}
