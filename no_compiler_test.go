package protocheck_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoCompilerLinked pins that a program importing protocheck does not link
// the proto compiler: generated stubs import protocheck, so every program that
// uses a message type would pay for it (#42). descpath is the one protocompile
// package protocheck needs, and it has no dependencies. Tests may use the rest.
func TestNoCompilerLinked(t *testing.T) {
	const allowed = "github.com/trendvidia/protocompile/fdp/descpath"
	for _, pkg := range []string{
		"github.com/trendvidia/protocheck/v2",
		"github.com/trendvidia/protocheck/v2/protocheckpb",
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		deps := strings.Fields(string(out))
		if !containsDep(deps, pkg) {
			t.Fatalf("go list -deps %s did not list %s itself: %v", pkg, pkg, deps)
		}
		for _, dep := range deps {
			if strings.HasPrefix(dep, "github.com/trendvidia/protocompile") && dep != allowed {
				t.Errorf("%s links %s", pkg, dep)
			}
		}
	}
}

func containsDep(deps []string, pkg string) bool {
	for _, dep := range deps {
		if dep == pkg {
			return true
		}
	}
	return false
}
