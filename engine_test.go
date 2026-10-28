package protocheck_test

import (
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
)

// ─── SPI wiring ─────────────────────────────────────────────────────────────

func TestEngineFromValidator(t *testing.T) {
	v := mustValidator(t)
	eng := v.Engine()
	if eng == nil {
		t.Fatal("Engine() returned nil")
	}
	if v.Engine() != eng {
		t.Error("Engine() should return the same instance on repeated calls")
	}
}

func TestEngineRegister(t *testing.T) {
	eng := mustValidator(t).Engine()
	fn := func(args []any) (bool, *protocheck.Violation) { return true, nil }

	if err := eng.Register("myco.commons.is_e164", fn); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := eng.Register("myco.commons.is_e164", fn); err == nil {
		t.Error("expected error on duplicate FQN, got nil")
	}
	if err := eng.Register("", fn); err == nil {
		t.Error("expected error on empty FQN, got nil")
	}
	if err := eng.Register("myco.commons.matches", nil); err == nil {
		t.Error("expected error on nil Function, got nil")
	}
	// A different FQN still registers after the failures above.
	if err := eng.Register("myco.commons.matches", fn); err != nil {
		t.Errorf("Register after errors: %v", err)
	}
}

type staticCatalog map[string]string

func (c staticCatalog) Render(code string, params map[string]any) (string, bool) {
	msg, ok := c[code]
	return msg, ok
}

func TestEngineRegisterCatalog(t *testing.T) {
	eng := mustValidator(t).Engine()
	cat := staticCatalog{"string.min_len": "too short"}

	if err := eng.RegisterCatalog("en-US", cat); err != nil {
		t.Fatalf("RegisterCatalog: %v", err)
	}
	if err := eng.RegisterCatalog("en-US", cat); err == nil {
		t.Error("expected error on duplicate locale, got nil")
	}
	if err := eng.RegisterCatalog("", cat); err == nil {
		t.Error("expected error on empty locale, got nil")
	}
	if err := eng.RegisterCatalog("pt-BR", nil); err == nil {
		t.Error("expected error on nil Catalog, got nil")
	}
	if err := eng.RegisterCatalog("pt-BR", staticCatalog{}); err != nil {
		t.Errorf("RegisterCatalog after errors: %v", err)
	}
}

// ─── Report shape ───────────────────────────────────────────────────────────

func TestEngineValidateValid(t *testing.T) {
	eng := mustValidator(t).Engine()

	report, err := eng.Validate(&testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Violations) != 0 {
		t.Errorf("expected no violations, got %v", report.Violations)
	}
	if report.Truncated {
		t.Error("valid message must not be truncated")
	}
	if report.MessageType != "testpb.User" {
		t.Errorf("MessageType = %q, want %q", report.MessageType, "testpb.User")
	}
	if report.Mode != protocheck.ExecutionModeCollectAll {
		t.Errorf("Mode = %v, want COLLECT_ALL", report.Mode)
	}
	if !strings.HasPrefix(report.Engine.Name, "protocheck-go") {
		t.Errorf("Engine.Name = %q, want protocheck-go prefix", report.Engine.Name)
	}
	if report.WallTimeNanos == 0 {
		t.Error("WallTimeNanos = 0, want measured wall time")
	}
}

func TestEngineValidateInvalid(t *testing.T) {
	eng := mustValidator(t).Engine()

	// User has two required fields; both missing.
	report, err := eng.Validate(&testpb.User{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %v", len(report.Violations), report.Violations)
	}
	if report.Truncated {
		t.Error("collect-all report must not be truncated")
	}
	for _, viol := range report.Violations {
		if viol.Cause.Code != "required" {
			t.Errorf("Code = %q, want %q", viol.Cause.Code, "required")
		}
	}
}

func TestEngineValidateFailFast(t *testing.T) {
	v, err := protocheck.New(protocheck.WithFailFast())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })

	report, err := v.Engine().Validate(&testpb.User{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if report.Mode != protocheck.ExecutionModeFailFast {
		t.Errorf("Mode = %v, want FAIL_FAST", report.Mode)
	}
	if len(report.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d", len(report.Violations))
	}
	if !report.Truncated {
		t.Error("fail-fast report with a violation must be truncated")
	}

	// A valid message under fail-fast is complete, not truncated.
	report, err = v.Engine().Validate(&testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Violations) != 0 || report.Truncated {
		t.Errorf("valid fail-fast report: violations=%v truncated=%v", report.Violations, report.Truncated)
	}
}

func TestExecutionModeString(t *testing.T) {
	cases := map[protocheck.ExecutionMode]string{
		protocheck.ExecutionModeUnspecified: "UNSPECIFIED",
		protocheck.ExecutionModeCollectAll:  "COLLECT_ALL",
		protocheck.ExecutionModeFailFast:    "FAIL_FAST",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("ExecutionMode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}
