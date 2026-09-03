package state

import (
	"testing"
	"time"
)

// TestDerive covers every row of the precedence table in Derive's doc
// comment, plus the interactions between rows that a plain "one case per
// row" test would miss: which row wins when more than one condition is
// technically satisfiable at once.
//
// Every case sets Op explicitly, even to OpNone: InProgressOp's Go zero
// value is "" (not OpNone's "none"), and a real caller always resolves Op to
// one or the other before building an Observation - never leaves it unset.
func TestDerive(t *testing.T) {
	cases := []struct {
		name string
		obs  Observation
		want Verdict
	}{
		// One case per row, in order.
		{
			name: "row 1: an in-progress op is InProgress",
			obs:  Observation{Op: OpRebase},
			want: InProgress,
		},
		{
			name: "row 2: upstream gone with real unlanded work is Orphaned",
			obs:  Observation{Op: OpNone, UpstreamGone: true, Unlanded: 2},
			want: Orphaned,
		},
		{
			name: "row 3: upstream gone with nothing unlanded is SafeToDelete",
			obs:  Observation{Op: OpNone, UpstreamGone: true, Unlanded: 0},
			want: SafeToDelete,
		},
		{
			name: "row 4: configured upstream that mismatches is WrongUpstream",
			obs:  Observation{Op: OpNone, UpstreamConfigured: true, UpstreamMismatch: true, Unlanded: 3},
			want: WrongUpstream,
		},
		{
			name: "row 5: never configured with unlanded work is NeverPushed",
			obs:  Observation{Op: OpNone, UpstreamConfigured: false, Unlanded: 1},
			want: NeverPushed,
		},
		{
			name: "row 6: never configured with nothing unlanded is SafeToDelete",
			obs:  Observation{Op: OpNone, UpstreamConfigured: false, Unlanded: 0},
			want: SafeToDelete,
		},
		{
			name: "row 7: ahead and behind is Diverged",
			obs:  Observation{Op: OpNone, UpstreamConfigured: true, Ahead: 2, Behind: 3},
			want: Diverged,
		},
		{
			name: "row 8: ahead only is Unpushed",
			obs:  Observation{Op: OpNone, UpstreamConfigured: true, Ahead: 2, Behind: 0},
			want: Unpushed,
		},
		{
			name: "row 9: behind only is Behind",
			obs:  Observation{Op: OpNone, UpstreamConfigured: true, Ahead: 0, Behind: 2},
			want: Behind,
		},
		{
			name: "row 10: neither ahead nor behind is Landed",
			obs:  Observation{Op: OpNone, UpstreamConfigured: true, Ahead: 0, Behind: 0},
			want: Landed,
		},

		// Precedence: prove the ordering, not just the isolated conditions.
		{
			name: "row 1 overrides every later row, even upstream-gone-with-unlanded-work",
			obs:  Observation{Op: OpMerge, UpstreamGone: true, Unlanded: 5},
			want: InProgress,
		},
		{
			name: "row 2 (upstream gone) is checked before row 4 (mismatch), even if both are set",
			obs:  Observation{Op: OpNone, UpstreamGone: true, UpstreamMismatch: true, Unlanded: 1},
			want: Orphaned,
		},
		{
			name: "row 4 (mismatch) fires before row 5 would, even when the no-upstream+unlanded shape also matches",
			obs:  Observation{Op: OpNone, UpstreamConfigured: false, UpstreamMismatch: true, Unlanded: 5},
			want: WrongUpstream,
		},
		{
			name: "row 7 (diverged) beats what would read as row 8 or row 9 alone",
			obs:  Observation{Op: OpNone, UpstreamConfigured: true, Ahead: 1, Behind: 1},
			want: Diverged,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Derive(c.obs); got != c.want {
				t.Errorf("Derive(%+v) = %q, want %q", c.obs, got, c.want)
			}
		})
	}
}

// TestIsStale is row 11: a fact about the repo's fetch recency, orthogonal
// to Derive's per-branch verdict.
func TestIsStale(t *testing.T) {
	if !IsStale(time.Now().Add(-2*time.Hour), time.Hour) {
		t.Error("a fetch two hours old should be stale against a one-hour threshold")
	}
	if IsStale(time.Now().Add(-2*time.Minute), time.Hour) {
		t.Error("a fetch two minutes old should not be stale against a one-hour threshold")
	}
	if !IsStale(time.Time{}, time.Hour) {
		t.Error("a zero (never fetched) time should read as stale, not as an error case")
	}
}
