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

func cutDash(s string) (dashes, flag string) {
	if strings.HasPrefix(s, "-") {
		if strings.HasPrefix(s[1:], "-") {
			return "--", s[2:]
		}
		return "-", s[1:]
	}
	return "", s
}

func Inject(root *ffcli.Command) {
	root.Subcommands = append(
		root.Subcommands,
		&ffcli.Command{
			Name:      "__complete",
			ShortHelp: "HIDDEN: __complete provides autocomplete suggestions to interactive shells.",
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
				var words []string
				var dir ShellCompDirective

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
						return fmt.Errorf("%s flag parsing: %w", parent.Name, err)
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
									return fmt.Errorf("completing %s flag %s: %w", parent.Name, flag.Name, err)
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

				for _, word := range words {
					fmt.Println(word)
				}
				fmt.Println(":" + strconv.Itoa(int(dir)))
				return nil
			},
		},
		&ffcli.Command{
			Name:      "completion",
			ShortHelp: "Shell tab-completion scripts.",
			Subcommands: []*ffcli.Command{
				{
					Name: "bash",
					Exec: func(ctx context.Context, args []string) error {
						_, err := fmt.Fprintf(os.Stdout, `# bash completion V2 for %-36[1]s -*- shell-script -*-

__%[1]s_debug()
{
if [[ -n ${BASH_COMP_DEBUG_FILE-} ]]; then
echo "$*" >> "${BASH_COMP_DEBUG_FILE}"
fi
}

# Macs have bash3 for which the bash-completion package doesn't include
# _init_completion. This is a minimal version of that function.
__%[1]s_init_completion()
{
COMPREPLY=()
_get_comp_words_by_ref "$@" cur prev words cword
}

# This function calls the %[1]s program to obtain the completion
# results and the directive.  It fills the 'out' and 'directive' vars.
__%[1]s_get_completion_results() {
local requestComp lastParam lastChar args

# Prepare the command to request completions for the program.
# Calling ${words[0]} instead of directly %[1]s allows handling aliases
args=("${words[@]:1}")
requestComp="${words[0]} %[2]s -- ${args[*]}"

lastParam=${words[$((${#words[@]}-1))]}
lastChar=${lastParam:$((${#lastParam}-1)):1}
__%[1]s_debug "lastParam ${lastParam}, lastChar ${lastChar}"

if [[ -z ${cur} && ${lastChar} != = ]]; then
# If the last parameter is complete (there is a space following it)
# We add an extra empty parameter so we can indicate this to the go method.
__%[1]s_debug "Adding extra empty parameter"
requestComp="${requestComp} ''"
fi

# When completing a flag with an = (e.g., %[1]s -n=<TAB>)
# bash focuses on the part after the =, so we need to remove
# the flag part from $cur
if [[ ${cur} == -*=* ]]; then
cur="${cur#*=}"
fi

__%[1]s_debug "Calling ${requestComp}"
# Use eval to handle any environment variables and such
out=$(eval "${requestComp}" 2>/dev/null)

# Extract the directive integer at the very end of the output following a colon (:)
directive=${out##*:}
# Remove the directive
out=${out%%:*}
if [[ ${directive} == "${out}" ]]; then
# There is not directive specified
directive=0
fi
__%[1]s_debug "The completion directive is: ${directive}"
__%[1]s_debug "The completions are: ${out}"
}

__%[1]s_process_completion_results() {
local shellCompDirectiveError=%[3]d
local shellCompDirectiveNoSpace=%[4]d
local shellCompDirectiveNoFileComp=%[5]d
local shellCompDirectiveFilterFileExt=%[6]d
local shellCompDirectiveFilterDirs=%[7]d
local shellCompDirectiveKeepOrder=%[8]d

if (((directive & shellCompDirectiveError) != 0)); then
# Error code.  No completion.
__%[1]s_debug "Received error from custom completion go code"
return
else
if (((directive & shellCompDirectiveNoSpace) != 0)); then
	if [[ $(type -t compopt) == builtin ]]; then
		__%[1]s_debug "Activating no space"
		compopt -o nospace
	else
		__%[1]s_debug "No space directive not supported in this version of bash"
	fi
fi
if (((directive & shellCompDirectiveKeepOrder) != 0)); then
	if [[ $(type -t compopt) == builtin ]]; then
		# no sort isn't supported for bash less than < 4.4
		if [[ ${BASH_VERSINFO[0]} -lt 4 || ( ${BASH_VERSINFO[0]} -eq 4 && ${BASH_VERSINFO[1]} -lt 4 ) ]]; then
			__%[1]s_debug "No sort directive not supported in this version of bash"
		else
			__%[1]s_debug "Activating keep order"
			compopt -o nosort
		fi
	else
		__%[1]s_debug "No sort directive not supported in this version of bash"
	fi
fi
if (((directive & shellCompDirectiveNoFileComp) != 0)); then
	if [[ $(type -t compopt) == builtin ]]; then
		__%[1]s_debug "Activating no file completion"
		compopt +o default
	else
		__%[1]s_debug "No file completion directive not supported in this version of bash"
	fi
fi
fi

# Separate activeHelp from normal completions
local completions=()
local activeHelp=()
__%[1]s_extract_activeHelp

if (((directive & shellCompDirectiveFilterFileExt) != 0)); then
# File extension filtering
local fullFilter filter filteringCmd

# Do not use quotes around the $completions variable or else newline
# characters will be kept.
for filter in ${completions[*]}; do
	fullFilter+="$filter|"
done

filteringCmd="_filedir $fullFilter"
__%[1]s_debug "File filtering command: $filteringCmd"
$filteringCmd
elif (((directive & shellCompDirectiveFilterDirs) != 0)); then
# File completion for directories only

local subdir
subdir=${completions[0]}
if [[ -n $subdir ]]; then
	__%[1]s_debug "Listing directories in $subdir"
	pushd "$subdir" >/dev/null 2>&1 && _filedir -d && popd >/dev/null 2>&1 || return
else
	__%[1]s_debug "Listing directories in ."
	_filedir -d
fi
else
__%[1]s_handle_completion_types
fi

__%[1]s_handle_special_char "$cur" :
__%[1]s_handle_special_char "$cur" =

# Print the activeHelp statements before we finish
if ((${#activeHelp[*]} != 0)); then
printf "\n";
printf "%%s\n" "${activeHelp[@]}"
printf "\n"

# The prompt format is only available from bash 4.4.
# We test if it is available before using it.
if (x=${PS1@P}) 2> /dev/null; then
	printf "%%s" "${PS1@P}${COMP_LINE[@]}"
else
	# Can't print the prompt.  Just print the
	# text the user had typed, it is workable enough.
	printf "%%s" "${COMP_LINE[@]}"
fi
fi
}

# Separate activeHelp lines from real completions.
# Fills the $activeHelp and $completions arrays.
__%[1]s_extract_activeHelp() {
local activeHelpMarker="%[9]s"
local endIndex=${#activeHelpMarker}

while IFS='' read -r comp; do
if [[ ${comp:0:endIndex} == $activeHelpMarker ]]; then
	comp=${comp:endIndex}
	__%[1]s_debug "ActiveHelp found: $comp"
	if [[ -n $comp ]]; then
		activeHelp+=("$comp")
	fi
else
	# Not an activeHelp line but a normal completion
	completions+=("$comp")
fi
done <<<"${out}"
}

__%[1]s_handle_completion_types() {
__%[1]s_debug "__%[1]s_handle_completion_types: COMP_TYPE is $COMP_TYPE"

case $COMP_TYPE in
37|42)
# Type: menu-complete/menu-complete-backward and insert-completions
# If the user requested inserting one completion at a time, or all
# completions at once on the command-line we must remove the descriptions.
# https://github.com/spf13/cobra/issues/1508
local tab=$'\t' comp
while IFS='' read -r comp; do
	[[ -z $comp ]] && continue
	# Strip any description
	comp=${comp%%%%$tab*}
	# Only consider the completions that match
	if [[ $comp == "$cur"* ]]; then
		COMPREPLY+=("$comp")
	fi
done < <(printf "%%s\n" "${completions[@]}")
;;

*)
# Type: complete (normal completion)
__%[1]s_handle_standard_completion_case
;;
esac
}

__%[1]s_handle_standard_completion_case() {
local tab=$'\t' comp

# Short circuit to optimize if we don't have descriptions
if [[ "${completions[*]}" != *$tab* ]]; then
IFS=$'\n' read -ra COMPREPLY -d '' < <(compgen -W "${completions[*]}" -- "$cur")
return 0
fi

local longest=0
local compline
# Look for the longest completion so that we can format things nicely
while IFS='' read -r compline; do
[[ -z $compline ]] && continue
# Strip any description before checking the length
comp=${compline%%%%$tab*}
# Only consider the completions that match
[[ $comp == "$cur"* ]] || continue
COMPREPLY+=("$compline")
if ((${#comp}>longest)); then
	longest=${#comp}
fi
done < <(printf "%%s\n" "${completions[@]}")

# If there is a single completion left, remove the description text
if ((${#COMPREPLY[*]} == 1)); then
__%[1]s_debug "COMPREPLY[0]: ${COMPREPLY[0]}"
comp="${COMPREPLY[0]%%%%$tab*}"
__%[1]s_debug "Removed description from single completion, which is now: ${comp}"
COMPREPLY[0]=$comp
else # Format the descriptions
__%[1]s_format_comp_descriptions $longest
fi
}

__%[1]s_handle_special_char()
{
local comp="$1"
local char=$2
if [[ "$comp" == *${char}* && "$COMP_WORDBREAKS" == *${char}* ]]; then
local word=${comp%%"${comp##*${char}}"}
local idx=${#COMPREPLY[*]}
while ((--idx >= 0)); do
	COMPREPLY[idx]=${COMPREPLY[idx]#"$word"}
done
fi
}

__%[1]s_format_comp_descriptions()
{
local tab=$'\t'
local comp desc maxdesclength
local longest=$1

local i ci
for ci in ${!COMPREPLY[*]}; do
comp=${COMPREPLY[ci]}
# Properly format the description string which follows a tab character if there is one
if [[ "$comp" == *$tab* ]]; then
	__%[1]s_debug "Original comp: $comp"
	desc=${comp#*$tab}
	comp=${comp%%%%$tab*}

	# $COLUMNS stores the current shell width.
	# Remove an extra 4 because we add 2 spaces and 2 parentheses.
	maxdesclength=$(( COLUMNS - longest - 4 ))

	# Make sure we can fit a description of at least 8 characters
	# if we are to align the descriptions.
	if ((maxdesclength > 8)); then
		# Add the proper number of spaces to align the descriptions
		for ((i = ${#comp} ; i < longest ; i++)); do
			comp+=" "
		done
	else
		# Don't pad the descriptions so we can fit more text after the completion
		maxdesclength=$(( COLUMNS - ${#comp} - 4 ))
	fi

	# If there is enough space for any description text,
	# truncate the descriptions that are too long for the shell width
	if ((maxdesclength > 0)); then
		if ((${#desc} > maxdesclength)); then
			desc=${desc:0:$(( maxdesclength - 1 ))}
			desc+="…"
		fi
		comp+="  ($desc)"
	fi
	COMPREPLY[ci]=$comp
	__%[1]s_debug "Final comp: $comp"
fi
done
}

__start_%[1]s()
{
local cur prev words cword split

COMPREPLY=()

# Call _init_completion from the bash-completion package
# to prepare the arguments properly
if declare -F _init_completion >/dev/null 2>&1; then
_init_completion -n =: || return
else
__%[1]s_init_completion -n =: || return
fi

__%[1]s_debug
__%[1]s_debug "========= starting completion logic =========="
__%[1]s_debug "cur is ${cur}, words[*] is ${words[*]}, #words[@] is ${#words[@]}, cword is $cword"

# The user could have moved the cursor backwards on the command-line.
# We need to trigger completion from the $cword location, so we need
# to truncate the command-line ($words) up to the $cword location.
words=("${words[@]:0:$cword+1}")
__%[1]s_debug "Truncated words[*]: ${words[*]},"

local out directive
__%[1]s_get_completion_results
__%[1]s_process_completion_results
}

if [[ $(type -t compopt) = "builtin" ]]; then
complete -o default -F __start_%[1]s %[1]s
else
complete -o default -o nospace -F __start_%[1]s %[1]s
fi

# ex: ts=4 sw=4 et filetype=sh
`, "tailscale", "__complete",
							ShellCompDirectiveError, ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp,
							ShellCompDirectiveFilterFileExt, ShellCompDirectiveFilterDirs, ShellCompDirectiveKeepOrder,
							"_activeHelp_")
						return err
					},
				},
			},
		},
	)
}

// ShellCompDirective is a bit map representing the different behaviors the shell
// can be instructed to have once completions have been provided.
type ShellCompDirective int

const (
	// ShellCompDirectiveError indicates an error occurred and completions should be ignored.
	ShellCompDirectiveError ShellCompDirective = 1 << iota

	// ShellCompDirectiveNoSpace indicates that the shell should not add a space
	// after the completion even if there is a single completion provided.
	ShellCompDirectiveNoSpace

	// ShellCompDirectiveNoFileComp indicates that the shell should not provide
	// file completion even when no completion is provided.
	ShellCompDirectiveNoFileComp

	// ShellCompDirectiveFilterFileExt indicates that the provided completions
	// should be used as file extension filters.
	// For flags, using Command.MarkFlagFilename() and Command.MarkPersistentFlagFilename()
	// is a shortcut to using this directive explicitly.  The BashCompFilenameExt
	// annotation can also be used to obtain the same behavior for flags.
	ShellCompDirectiveFilterFileExt

	// ShellCompDirectiveFilterDirs indicates that only directory names should
	// be provided in file completion.  To request directory names within another
	// directory, the returned completions should specify the directory within
	// which to search.  The BashCompSubdirsInDir annotation can be used to
	// obtain the same behavior but only for flags.
	ShellCompDirectiveFilterDirs

	// ShellCompDirectiveKeepOrder indicates that the shell should preserve the order
	// in which the completions are provided
	ShellCompDirectiveKeepOrder

	// ===========================================================================

	// All directives using iota should be above this one.
	// For internal use.
	shellCompDirectiveMaxValue

	// ShellCompDirectiveDefault indicates to let the shell perform its default
	// behavior after completions have been provided.
	// This one must be last to avoid messing up the iota count.
	ShellCompDirectiveDefault ShellCompDirective = 0
)

// Returns a string listing the different directive enabled in the specified parameter
func (d ShellCompDirective) String() string {
	var directives []string
	if d&ShellCompDirectiveError != 0 {
		directives = append(directives, "ShellCompDirectiveError")
	}
	if d&ShellCompDirectiveNoSpace != 0 {
		directives = append(directives, "ShellCompDirectiveNoSpace")
	}
	if d&ShellCompDirectiveNoFileComp != 0 {
		directives = append(directives, "ShellCompDirectiveNoFileComp")
	}
	if d&ShellCompDirectiveFilterFileExt != 0 {
		directives = append(directives, "ShellCompDirectiveFilterFileExt")
	}
	if d&ShellCompDirectiveFilterDirs != 0 {
		directives = append(directives, "ShellCompDirectiveFilterDirs")
	}
	if d&ShellCompDirectiveKeepOrder != 0 {
		directives = append(directives, "ShellCompDirectiveKeepOrder")
	}
	if len(directives) == 0 {
		directives = append(directives, "ShellCompDirectiveDefault")
	}

	if d >= shellCompDirectiveMaxValue {
		return fmt.Sprintf("ERROR: unexpected ShellCompDirective value: %d", d)
	}
	return strings.Join(directives, " | ")
}

type CompleteFunc func(word string) ([]string, ShellCompDirective, error)

var completeFlags map[*flag.Flag]CompleteFunc

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

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface {
		IsBoolFlag() bool
	})
	return ok && bf.IsBoolFlag()
}
