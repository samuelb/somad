package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"

	"somad/internal/channels"
)

// The completion scripts also live as plain files so packages can install
// them directly; `soma completion <shell>` prints them for manual setups.
var (
	//go:embed completions/soma.bash
	bashCompletion string
	//go:embed completions/soma.zsh
	zshCompletion string
)

// runCompletion prints the completion script for the given shell. The
// undocumented "channels" form backs the scripts' channel-argument
// completion.
func runCompletion(args []string) {
	if len(args) != 1 {
		failUsage("usage: soma completion <bash|zsh>")
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "channels":
		printChannelCompletions(os.Stdout)
	default:
		failUsage("unsupported shell %q; usage: soma completion <bash|zsh>", args[0])
	}
}

// printChannelCompletions writes one "id<TAB>title" line per channel from the
// local catalog cache. Completion must be instant and side-effect free, so it
// never spawns the daemon or touches the network — without a cache (soma has
// not played anything yet) it completes nothing.
func printChannelCompletions(w io.Writer) {
	catalog, err := channels.PeekChannelsFromCache()
	if err != nil {
		return
	}
	for _, ch := range catalog.Channels {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", ch.ID, ch.Title)
	}
}
