// Command landfirst answers two questions about a repository with a lot of
// parallel work: did everything that finished actually land, and what should
// merge next.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/engine"
	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/model"
	"github.com/montjeffrey/landfirst/internal/protocol"
	"github.com/montjeffrey/landfirst/internal/tui"
)

// version is injected by the release build (-X main.version=...). Left empty, the
// module's own build info answers instead; see resolveVersion.
var version = ""

// resolveVersion names the running build. An injected version wins; otherwise a
// binary made by `go install module@vX` carries that version in its build info;
// a build from a checkout reports itself as a development build plus the commit.
func resolveVersion(injected string, info *debug.BuildInfo) string {
	if injected != "" {
		return injected
	}
	if info == nil {
		return "(devel)"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return "(devel) " + s.Value[:12]
		}
	}
	return "(devel)"
}

// openExitCode is 2 when the answer is "fix the repository or where you ran
// this" (no repository, no commits, no base, ambiguous remote) and 1 when
// something went wrong inside landfirst or git.
func openExitCode(err error) int {
	for _, target := range []error{git.ErrNotARepo, git.ErrEmptyRepo, git.ErrNoBase, git.ErrNoRemote} {
		if errors.Is(err, target) {
			return 2
		}
	}
	return 1
}

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
		info, _ := debug.ReadBuildInfo()
		fmt.Println("landfirst " + resolveVersion(version, info))
		return
	}

	cwd, _ := os.Getwd()
	eng, err := engine.Open(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "landfirst:", tui.Sanitize(err.Error()))
		// A resolvable-base/remote failure means "fix the repo", not "landfirst
		// crashed" - distinguish it from other Open errors (e.g. not a git repo).
		// engine.Open passes git.Open's error through unwrapped, so the same
		// sentinels still apply here.
		os.Exit(openExitCode(err))
	}
	defer eng.Close()
	// An earlier run killed mid-probe leaves its temp object store behind.
	model.SweepStaleObjectStores(24 * time.Hour)

	stage := protocol.StageFull
	if *noProbe {
		stage = protocol.StageFast
	}
	snapOpt := engine.SnapshotOptions{Stage: stage, MaxPairs: *maxPairs}
	graphOpt := protocol.GraphOptions{Limit: *limit, Branches: *branches, All: *all}

	if *asJSON || *plain {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		snap, err := eng.Snapshot(ctx, snapOpt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "landfirst:", tui.Sanitize(err.Error()))
			os.Exit(1)
		}
		if *asJSON {
			out, err := tui.EncodeJSON(snap)
			if err != nil {
				fmt.Fprintln(os.Stderr, "landfirst:", tui.Sanitize(err.Error()))
				os.Exit(1)
			}
			os.Stdout.Write(out)
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
