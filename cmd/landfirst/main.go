// Command landfirst answers two questions about a repository with a lot of
// parallel work: did everything that finished actually land, and what should
// merge next.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/model"
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
	repo, err := git.Open(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "landfirst:", err)
		os.Exit(1)
	}
	opt := model.Options{Probe: !*noProbe, MaxPairs: *maxPairs, Limit: *limit, MaxBranches: *branches}
	if *all {
		opt.Limit, opt.MaxBranches = -1, -1
	}

	if *asJSON || *plain {
		m := model.Build(repo, opt)
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(m)
			return
		}
		fmt.Print(tui.PlainReport(m))
		return
	}

	// The panel takes the alternate screen so it behaves like a docked app and
	// leaves the shell scrollback untouched on exit.
	app := tui.New(repo, opt)
	app.SetDensity(tui.ParseDensity(*density))
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "landfirst:", err)
		os.Exit(1)
	}
}
