package protowire_test

import (
	"errors"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"github.com/trendvidia/protocheck/v2/protowire"
	"github.com/trendvidia/protowire-go/check"
	"github.com/trendvidia/protowire-go/encoding/pxf"
)

func mustValidator(t *testing.T) *protowire.Validator {
	t.Helper()
	v, err := protowire.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func ruleIDs(rep *check.Report) map[string]check.Violation {
	byID := make(map[string]check.Violation, len(rep.Violations))
	for _, v := range rep.Violations {
		byID[v.RuleID] = v
	}
	return byID
}

func TestValidateMapsViolations(t *testing.T) {
	v := mustValidator(t)

	// name empty (violates min_len), email absent (violates required).
	rep, err := v.Validate(&testpb.User{Name: ""})
	if err != nil {
		t.Fatalf("violations must be report content, not an engine error: %v", err)
	}
	if rep.OK() {
		t.Fatal("expected violations, got clean report")
	}
	byID := ruleIDs(rep)
	if _, ok := byID["rfc001.required"]; !ok {
		if _, ok := byID["rfc001.protowire.required"]; !ok {
			t.Errorf("expected a required violation, got: %+v", rep.Violations)
		}
	}
	for id, viol := range byID {
		if len(id) < len("rfc001.") || id[:len("rfc001.")] != "rfc001." {
			t.Errorf("rule ID %q not namespaced under rfc001.", id)
		}
		if viol.Message == "" {
			t.Errorf("violation %q has empty message", id)
		}
	}
}

func TestValidateViolationPath(t *testing.T) {
	v := mustValidator(t)

	rep, err := v.Validate(&testpb.User{Name: "Al", Email: "not-an-email"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatal("expected an email-format violation")
	}
	found := false
	for _, viol := range rep.Violations {
		if viol.Path == "email" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a violation with path %q, got: %+v", "email", rep.Violations)
	}
}

func TestValidateCleanPass(t *testing.T) {
	v := mustValidator(t)

	rep, err := v.Validate(&testpb.User{Name: "Al", Email: "al@example.com", Age: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("expected clean report, got: %+v", rep.Violations)
	}
}

func TestValidateNonProtoValue(t *testing.T) {
	v := mustValidator(t)

	rep, err := v.Validate(struct{ Name string }{"x"})
	if rep != nil {
		t.Errorf("expected nil report, got: %+v", rep)
	}
	if err == nil {
		t.Fatal("expected engine error for non-proto value")
	}
	var ce *check.Error
	if errors.As(err, &ce) {
		t.Error("engine errors must not be check.Error")
	}
}

func TestWrapSharesEngine(t *testing.T) {
	engine, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	rep, err := protowire.Wrap(engine).Validate(&testpb.User{Name: "Al", Email: "al@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("expected clean report, got: %+v", rep.Violations)
	}
}

// TestPXFDecodeWithProtocheck is the end-to-end shape from
// protowire-go#49: a PXF decode that fails with *check.Error when the
// decoded message violates RFC-001 constraints.
func TestPXFDecodeWithProtocheck(t *testing.T) {
	v := mustValidator(t)
	opts := pxf.UnmarshalOptions{Validator: v}

	// email absent → required violation fails the decode.
	var user testpb.User
	err := opts.Unmarshal([]byte(`name = "Al"`), &user)
	var ce *check.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *check.Error, got %T: %v", err, err)
	}
	if len(ce.Report.Violations) == 0 {
		t.Fatal("expected violations in the report")
	}

	// Valid document decodes cleanly.
	var ok testpb.User
	if err := opts.Unmarshal([]byte(`name = "Al"`+"\n"+`email = "al@example.com"`), &ok); err != nil {
		t.Fatalf("expected clean decode, got: %v", err)
	}
}
