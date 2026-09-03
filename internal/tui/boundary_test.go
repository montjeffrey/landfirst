package tui

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestDoesNotDirectlyImportModelOrGit is the import-boundary test P1-1's own
// acceptance criterion calls for ("an automated import-boundary test"),
// generalized to this package's actual name - the plan says internal/ui/**,
// but this codebase never did that rename, so the test targets internal/tui
// as it actually exists.
//
// Uses `go list -json`'s own Imports field - this package's *direct* imports
// only. `go list -deps` would give the full transitive closure instead, which
// would false-positive on the legitimate internal/tui -> internal/engine ->
// internal/model chain: internal/engine's own package doc explains why it
// stays a thin adapter over internal/model rather than a physical
// relocation of it. What must never happen is internal/tui reaching past
// internal/engine/internal/protocol straight into model or git itself.
func TestDoesNotDirectlyImportModelOrGit(t *testing.T) {
	// The full import path, not ".", so this resolves correctly regardless of
	// the working directory the test binary happens to run from (a
	// go-test-compiled binary run directly, as this repo's own build
	// instructions do to route around a Windows Smart App Control quirk,
	// does not cd into the package directory the way `go test` itself does).
	out, err := exec.Command("go", "list", "-json", "github.com/montjeffrey/landfirst/internal/tui").Output()
	if err != nil {
		t.Fatalf("go list -json internal/tui: %v", err)
	}
	var pkg struct {
		Imports []string
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("decoding go list output: %v\n%s", err, out)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("go list reported no imports at all - something is wrong with this test, not the package")
	}
	for _, imp := range pkg.Imports {
		switch imp {
		case "github.com/montjeffrey/landfirst/internal/model", "github.com/montjeffrey/landfirst/internal/git":
			t.Fatalf("internal/tui must not directly import %q - go through internal/engine and internal/protocol instead", imp)
		}
	}
}
