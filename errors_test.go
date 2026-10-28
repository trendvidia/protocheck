package protocheck_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
)

// Engine-produced expression errors (compile/eval) are the engine's to
// test; stub_engine_test.go covers how core surfaces engine errors, and
// builtin_check_test.go the no-engine fallback.

// findViolation returns the first violation with the given code.
func findViolation(ve *protocheck.ValidationError, id string) *protocheck.EnrichedViolation {
	for _, v := range ve.Violations {
		if v.Cause.Code == id {
			return v
		}
	}
	return nil
}

func mustValidationError(t *testing.T, err error) *protocheck.ValidationError {
	t.Helper()
	if err == nil {
		t.Fatal("expected ValidationError, got nil")
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return ve
}

// ─── WithFunction error paths ──────────────────────────────────────────────

// WithFunction accepts only §9.2 SPI functions; engine-native reflective
// Go functions moved to the expression engines, and the error says so.
func TestWithFunction_RejectsNonSPIFunction(t *testing.T) {
	for _, fn := range []any{42, func(s string) bool { return true }} {
		_, err := protocheck.New(protocheck.WithFunction("X", fn))
		if err == nil {
			t.Fatalf("expected error for %T, got nil", fn)
		}
		if !strings.Contains(err.Error(), `WithFunction("X")`) {
			t.Errorf("err = %q, want WithFunction(\"X\") prefix wrap", err.Error())
		}
		if !strings.Contains(err.Error(), "expression engine") {
			t.Errorf("err = %q, want pointer to the expression engine", err.Error())
		}
	}
}

// New() must propagate the first option's error: a later valid option must
// not silently swallow a prior failure.
func TestNew_OptionErrorShortCircuits(t *testing.T) {
	_, err := protocheck.New(
		protocheck.WithFunction("bad", 123), // fails: not a Function
		protocheck.WithFunction("ok", protocheck.Function(func(args []any) (bool, *protocheck.Violation) {
			return true, nil
		})),
	)
	if err == nil {
		t.Fatal("expected error from first option")
	}
	if !strings.Contains(err.Error(), `WithFunction("bad")`) {
		t.Errorf("err = %q, want first option's error", err.Error())
	}
}
