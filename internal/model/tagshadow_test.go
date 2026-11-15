package model

import "testing"

// A tag named origin/<branch> makes git print a correct upstream in its short
// form as remotes/origin/<branch>. The verdict must not flip to WrongUpstream.
func TestTagNamedLikeTheUpstreamDoesNotMakeItWrong(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/tagged")
	f.commit("w.txt", "work", "work")
	f.run("push", "-q", "-u", "origin", "feat/tagged")
	f.run("tag", "origin/feat/tagged")
	f.run("checkout", "-q", "main")

	m := f.build()
	if got := verdictOf(m, "feat/tagged"); got == WrongUpstream {
		t.Fatalf("a correctly tracked branch was reported as %q because of a tag", got)
	}
	var ref string
	for _, u := range m.Units {
		if u.Branch == "feat/tagged" {
			ref = u.UpstreamRef
		}
	}
	if ref != "refs/remotes/origin/feat/tagged" {
		t.Fatalf("UpstreamRef = %q, want the full remote-tracking ref", ref)
	}
}
