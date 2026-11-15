package git

import (
	"strings"
	"testing"
)

// git's messages are matched by their English wording; a translated git must
// not be able to change them.
func TestGitEnvPinsTheLocale(t *testing.T) {
	t.Setenv("LANGUAGE", "de")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	var lang, lcall []string
	for _, kv := range gitEnv([]string{"GIT_X=1"}) {
		switch {
		case strings.HasPrefix(kv, "LANGUAGE="):
			lang = append(lang, kv)
		case strings.HasPrefix(kv, "LC_ALL="):
			lcall = append(lcall, kv)
		}
	}
	if len(lang) != 0 {
		t.Fatalf("LANGUAGE must be dropped, got %v", lang)
	}
	if len(lcall) == 0 || lcall[len(lcall)-1] != "LC_ALL=C" {
		t.Fatalf("the last LC_ALL must be C, got %v", lcall)
	}
}
