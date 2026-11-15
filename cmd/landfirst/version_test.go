package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	info := func(v string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}, Settings: settings}
	}
	cases := []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		want     string
	}{
		{"injected by the release build wins", "v0.4.0", info("v9.9.9"), "v0.4.0"},
		{"go install uses the module version", "", info("v0.4.0"), "v0.4.0"},
		{"a source build says devel and the commit", "", info("(devel)", debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"}), "(devel) 0123456789ab"},
		{"a source build with no vcs data", "", info("(devel)"), "(devel)"},
		{"no build info at all", "", nil, "(devel)"},
	}
	for _, c := range cases {
		if got := resolveVersion(c.injected, c.info); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
