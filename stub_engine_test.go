package protocheck_test

// Core-side coverage of the ExpressionEngine seam: how the walk dispatches
// protocheckpb Check rules to the configured engine, how engine errors
// surface as violations. Real expression semantics are engine business;
// builtin_check_test.go covers the no-engine fallback.

import (
	"errors"
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// stubEngine is a test ExpressionEngine with pluggable behavior.
type stubEngine struct {
	name        string
	evalField   func(val protoreflect.Value, kind protoreflect.Kind, src string) (bool, error)
	evalMessage func(msg protoreflect.Message, src string) (bool, error)
	closed      int
}

func (s *stubEngine) Info() protocheck.EngineInfo {
	name := s.name
	if name == "" {
		name = "protocheck-go/stub"
	}
	return protocheck.EngineInfo{Name: name}
}

func (s *stubEngine) EvalField(val protoreflect.Value, kind protoreflect.Kind, src string) (bool, error) {
	if s.evalField == nil {
		return true, nil
	}
	return s.evalField(val, kind, src)
}

func (s *stubEngine) EvalMessage(msg protoreflect.Message, src string) (bool, error) {
	if s.evalMessage == nil {
		return true, nil
	}
	return s.evalMessage(msg, src)
}

func (s *stubEngine) Close() error {
	s.closed++
	return nil
}

// withStub returns a Validator backed by the given stub.
func withStub(t *testing.T, s *stubEngine, opts ...protocheck.Option) *protocheck.Validator {
	t.Helper()
	v, err := protocheck.New(append([]protocheck.Option{protocheck.WithExpressionEngine(s)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStubEngineFieldDispatch(t *testing.T) {
	// The walk hands the field value and rule source to the engine; a false
	// outcome becomes a violation under the rule's id with the rule's
	// message and the expression recorded in params.
	var gotSrc string
	s := &stubEngine{
		evalField: func(val protoreflect.Value, kind protoreflect.Kind, src string) (bool, error) {
			gotSrc = src
			return val.Float() >= 0, nil
		},
	}
	v := withStub(t, s)

	if err := v.Validate(&testpb.Discount{Percentage: 25}); err != nil {
		t.Errorf("expected pass, got %v", err)
	}
	ve := mustValidationError(t, v.Validate(&testpb.Discount{Percentage: -5}))
	vio := findViolation(ve, "valid_percentage")
	if vio == nil {
		t.Fatalf("expected valid_percentage violation, got %v", ve.Violations)
	}
	if gotSrc == "" {
		t.Error("engine never saw the rule source")
	}
	if vio.Cause.Params["expression"] != gotSrc {
		t.Errorf("params[expression] = %v, want %q", vio.Cause.Params["expression"], gotSrc)
	}
}

func TestStubEngineMessageDispatch(t *testing.T) {
	// Message-scope Check rules dispatch with the message; the stub applies
	// the actual invariant through protoreflect.
	s := &stubEngine{
		evalMessage: func(msg protoreflect.Message, src string) (bool, error) {
			fields := msg.Descriptor().Fields()
			start := msg.Get(fields.ByName("start_epoch")).Int()
			end := msg.Get(fields.ByName("end_epoch")).Int()
			return end > start, nil
		},
	}
	v := withStub(t, s)

	if err := v.Validate(&testpb.DateRange{StartEpoch: 1, EndEpoch: 2}); err != nil {
		t.Errorf("expected pass, got %v", err)
	}
	ve := mustValidationError(t, v.Validate(&testpb.DateRange{StartEpoch: 2, EndEpoch: 1}))
	if findViolation(ve, "valid_range") == nil {
		t.Fatalf("expected valid_range violation, got %v", ve.Violations)
	}
}

func TestStubEngineErrorSurfacesAsViolation(t *testing.T) {
	s := &stubEngine{
		evalField: func(protoreflect.Value, protoreflect.Kind, string) (bool, error) {
			return false, errors.New("expression compile error: boom")
		},
	}
	v := withStub(t, s)
	ve := mustValidationError(t, v.Validate(&testpb.Discount{Percentage: 25}))
	vio := findViolation(ve, "valid_percentage")
	if vio == nil {
		t.Fatalf("expected violation, got %v", ve.Violations)
	}
	if !strings.Contains(vio.Cause.FallbackMessage, "expression compile error: boom") {
		t.Errorf("message = %q, want engine error verbatim", vio.Cause.FallbackMessage)
	}
}

func TestCloseDelegatesToEngine(t *testing.T) {
	s := &stubEngine{}
	v := withStub(t, s)
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if s.closed != 2 {
		t.Errorf("engine Close called %d times, want 2", s.closed)
	}

	// Engine-free Close is a no-op.
	bare := mustValidator(t)
	if err := bare.Close(); err != nil {
		t.Errorf("engine-free Close: %v", err)
	}
}

func TestReportEngineIdentity(t *testing.T) {
	// Engine-free reports identify core; engine-backed reports identify the
	// engine.
	report, err := mustValidator(t).Engine().Validate(&testpb.User{Name: "A", Email: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Engine.Name != "protocheck-go" {
		t.Errorf("engine-free Engine.Name = %q, want protocheck-go", report.Engine.Name)
	}

	report, err = withStub(t, &stubEngine{name: "protocheck-go/stub"}).Engine().Validate(&testpb.User{Name: "A", Email: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Engine.Name != "protocheck-go/stub" {
		t.Errorf("stub-backed Engine.Name = %q, want protocheck-go/stub", report.Engine.Name)
	}
}

// ExpressionEngine.EvalField takes scalar values only, so a field-level
// check on a message-typed field fails with an error naming the engine
// rather than passing unevaluated (#68).
func TestStubEngineMessageFieldFailsLoudly(t *testing.T) {
	var calls int
	s := &stubEngine{
		evalField: func(protoreflect.Value, protoreflect.Kind, string) (bool, error) {
			calls++
			return true, nil
		},
	}
	v := withStub(t, s)

	expectValid(t, v, &testpb.MessageFieldCheck{})
	ve := mustValidationError(t, v.Validate(&testpb.MessageFieldCheck{Inner: &testpb.CheckedInner{}}))
	vio := findViolation(ve, "inner_never")
	if vio == nil {
		t.Fatalf("expected inner_never violation, got %v", ve.Violations)
	}
	if !strings.Contains(vio.Cause.FallbackMessage, "protocheck-go/stub") ||
		!strings.Contains(vio.Cause.FallbackMessage, "message-typed field") {
		t.Errorf("message = %q, want it to name the engine and the message-typed field", vio.Cause.FallbackMessage)
	}
	if calls != 0 {
		t.Errorf("EvalField called %d times, want 0", calls)
	}
}

// With an engine, a map value's check rules go to EvalMessage, as repeated
// message items already do (#73).
func TestStubEngineMapValueDispatch(t *testing.T) {
	var srcs []string
	s := &stubEngine{
		evalMessage: func(_ protoreflect.Message, src string) (bool, error) {
			srcs = append(srcs, src)
			return false, nil
		},
	}
	v := withStub(t, s)

	expectInvalid(t, v, &testpb.MessageCollectionCheck{InnerByName: map[string]*testpb.CheckedInner{"a": {}}}, "inner_never")
	if len(srcs) != 1 || srcs[0] != "false" {
		t.Errorf("EvalMessage sources = %q, want [\"false\"]", srcs)
	}
}
