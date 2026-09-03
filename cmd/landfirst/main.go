// Command landfirst answers two questions about a repository with a lot of
// parallel work: did everything that finished actually land, and what should
// merge next.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/engine"
	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/protocol"
	"github.com/montjeffrey/landfirst/internal/tui"
)

var version = "0.2.0-dev"

func main() {
	var (
		asJSON   = flag.Bool("json", false, "print the model as JSON and exit")
		plain    = flag.Bool("plain", false, "print a one-shot text report and exit")
		noProbe  = flag.Bool("no-probe", false, "skip conflict probing (faster, no merge plan conflicts)")
		maxPairs = flag.Int("max-pairs", 200, "cap on pairwise conflict probes")
		showVer  = flag.Bool("version", false, "print version and exit")
		density  = flag.String("density", "comfortable", "graph spacing: compact | comfortable | spacious")
		limit    = flag.Int("limit", 400, "commits to draw; -1 for the entire history")
		branches = flag.Int("branches", 10, "branches given their own lane; -1 for every branch")
		all      = flag.Bool("all", false, "draw the entire topology: every branch, every commit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("landfirst " + version)
		return
	}

	cwd, _ := os.Getwd()
	eng, err := engine.Open(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "landfirst:", err)
		// A resolvable-base/remote failure means "fix the repo", not "landfirst
		// crashed" - distinguish it from other Open errors (e.g. not a git repo).
		// engine.Open passes git.Open's error through unwrapped, so the same
		// sentinels still apply here.
		code := 1
		if errors.Is(err, git.ErrNoBase) || errors.Is(err, git.ErrNoRemote) {
			code = 2
		}
		os.Exit(code)
	}
	defer eng.Close()

	stage := protocol.StageFull
	if *noProbe {
		stage = protocol.StageFast
	}
	snapOpt := engine.SnapshotOptions{Stage: stage, MaxPairs: *maxPairs}
	graphOpt := protocol.GraphOptions{Limit: *limit, Branches: *branches, All: *all}

	if *asJSON || *plain {
		snap, err := eng.Snapshot(context.Background(), snapOpt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "landfirst:", err)
			os.Exit(1)
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(snap)
			return
		}
		fmt.Print(tui.PlainReport(snap))
		return
	}

	// The panel takes the alternate screen so it behaves like a docked app and
	// leaves the shell scrollback untouched on exit.
	app := tui.New(eng)
	app.SetDensity(tui.ParseDensity(*density))
	app.SetGraphOptions(graphOpt)
	app.SetSnapshotOptions(snapOpt)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "landfirst:", err)
		os.Exit(1)
	}
}
