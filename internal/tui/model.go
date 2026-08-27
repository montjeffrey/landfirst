package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/model"
)

type tab int

const (
	tabTopology tab = iota
	tabGraph
	tabLanding
	tabPlan
	tabWorktrees
	tabCount
)

var tabNames = []string{"topology", "graph", "landing", "merge plan", "worktrees"}

// App is the whole panel state.
type App struct {
	repo *git.Repo
	m    *model.Model
	opt  model.Options

	tab     tab
	density Density
	rows    []GraphRow // legacy box-drawing rows, kept for the plain report
	cursor int
	top    int // first visible row, for vertical scrolling
	panX   int // first visible canvas column, for horizontal scrolling
	w, h   int

	status   string
	loading  bool
	confirm  *pending // an action awaiting y/n
	quitting bool
}

// pending is an action the user must confirm. Holding the exact argv here -
// the same slice shown on screen - means the confirmation and the execution
// can never drift apart.
type pending struct {
	unit    model.Unit
	summary string
	args    []string
}

type builtMsg struct{ m *model.Model }
type actionDoneMsg struct {
	label string
	err   error
}

func New(repo *git.Repo, opt model.Options) *App {
	return &App{repo: repo, opt: opt, density: Comfortable, loading: true, status: "reading repository…"}
}

// SetDensity chooses vertical spacing before the program starts.
func (a *App) SetDensity(d Density) { a.density = d }

// ParseDensity maps the flag value. Unknown values fall back to comfortable
// rather than failing: this is a display preference, not a correctness one.
func ParseDensity(s string) Density {
	switch s {
	case "compact":
		return Compact
	case "spacious":
		return Spacious
	}
	return Comfortable
}

func (a *App) Init() tea.Cmd { return a.rebuild() }

func (a *App) rebuild() tea.Cmd {
	return func() tea.Msg { return builtMsg{model.Build(a.repo, a.opt)} }
}

// rows is what the current tab shows, so cursor movement and rendering always
// agree on the length.
func (a *App) rowCount() int {
	if a.m == nil {
		return 0
	}
	switch a.tab {
	case tabTopology:
		return len(a.topologyRows())
	case tabGraph:
		if a.m.Graph == nil {
			return 0
		}
		return len(a.m.Graph.Commits)
	case tabLanding:
		return len(a.m.Units)
	case tabPlan:
		return len(a.m.Plan)
	case tabWorktrees:
		return len(a.m.Worktrees)
	}
	return 0
}

func (a *App) selectedUnit() (model.Unit, bool) {
	if a.m == nil || a.tab != tabLanding || a.cursor >= len(a.m.Units) {
		return model.Unit{}, false
	}
	return a.m.Units[a.cursor], true
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		return a, nil

	case builtMsg:
		a.m = msg.m
		a.rows = nil
		if msg.m.Graph != nil {
			a.rows = BuildGraphRows(msg.m.Graph)
		}
		a.loading = false
		a.status = fmt.Sprintf("read in %dms · git %s · conflict probing: %s",
			msg.m.BuildMillis, msg.m.GitVersion, msg.m.ConflictMode)
		if a.cursor >= a.rowCount() {
			a.cursor = maxInt(0, a.rowCount()-1)
		}
		return a, nil

	case actionDoneMsg:
		if msg.err != nil {
			a.status = "failed: " + msg.err.Error()
		} else {
			a.status = msg.label + " — done"
		}
		a.loading = true
		return a, a.rebuild()

	case tea.KeyMsg:
		return a.onKey(msg)
	}
	return a, nil
}

func (a *App) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A confirmation swallows every key except the answer, so an action can
	// never be triggered by a keystroke meant for the list underneath.
	if a.confirm != nil {
		switch msg.String() {
		case "y", "Y", "enter":
			p := a.confirm
			a.confirm = nil
			a.status = "running git " + strings.Join(p.args, " ") + " …"
			return a, a.run(p)
		default:
			a.confirm = nil
			a.status = "cancelled"
			return a, nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c", "esc":
		a.quitting = true
		return a, tea.Quit
	case "tab":
		a.tab = (a.tab + 1) % tabCount
		a.cursor, a.top, a.panX = 0, 0, 0
	case "shift+tab":
		a.tab = (a.tab + tabCount - 1) % tabCount
		a.cursor, a.top, a.panX = 0, 0, 0
	case "1":
		a.tab, a.cursor, a.top = tabTopology, 0, 0
	case "2":
		a.tab, a.cursor, a.top = tabGraph, 0, 0
	case "3":
		a.tab, a.cursor, a.top = tabLanding, 0, 0
	case "4":
		a.tab, a.cursor, a.top = tabPlan, 0, 0
	case "5":
		a.tab, a.cursor, a.top = tabWorktrees, 0, 0
	case "h", "left":
		// Pan the canvas rather than changing tab: at full topology the graph
		// is far wider than any dock, so horizontal movement is the common
		// need and tab-switching has the number keys.
		if a.tab == tabGraph {
			a.panX = maxInt(0, a.panX-4)
		} else {
			a.tab = (a.tab + tabCount - 1) % tabCount
			a.cursor, a.top = 0, 0
		}
	case "l", "right":
		if a.tab == tabGraph {
			a.panX += 4
		} else {
			a.tab = (a.tab + 1) % tabCount
			a.cursor, a.top = 0, 0
		}
	case "H":
		a.panX = 0
	case "j", "down":
		if a.cursor < a.rowCount()-1 {
			a.cursor++
		}
	case "k", "up":
		if a.cursor > 0 {
			a.cursor--
		}
	case "g", "home":
		a.cursor = 0
	case "G", "end":
		a.cursor = maxInt(0, a.rowCount()-1)
	case "d":
		// Cycle how much vertical room each commit gets. A docked panel is
		// short, so this is the trade between smooth curves and visible history.
		a.density = (a.density + 1) % 3
		a.status = []string{"density: compact", "density: comfortable", "density: spacious"}[a.density]
	case "r":
		a.loading = true
		a.status = "refreshing…"
		return a, a.rebuild()
	case "p", "enter":
		return a, a.propose()
	}
	return a, nil
}

// propose stages an action for confirmation. It never runs anything.
func (a *App) propose() tea.Cmd {
	u, ok := a.selectedUnit()
	if !ok || u.Remedy == nil || len(u.Remedy.Args) == 0 {
		a.status = "nothing to do for this one"
		return nil
	}
	if !u.Remedy.Safe {
		a.status = "this one rewrites local history — do it by hand: git " + strings.Join(u.Remedy.Args, " ")
		return nil
	}
	a.confirm = &pending{unit: u, summary: u.Remedy.Summary, args: u.Remedy.Args}
	return nil
}

func (a *App) run(p *pending) tea.Cmd {
	return func() tea.Msg {
		_, err := git.Run(a.repo.Root, p.args...)
		return actionDoneMsg{label: "git " + strings.Join(p.args, " "), err: err}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}
