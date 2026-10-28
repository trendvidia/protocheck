package protocheck_test

// With no expression engine configured, protocheckpb Check expressions are
// evaluated in the RFC-001 §5.4 language @validate rules use.

import (
	"strings"
	"testing"
	"time"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// tempoOrdered is the cross-field rule TempoRange calls: the §5.4 language
// has no field selection, so it is a registered function taking the message.
func tempoOrdered(args []any) (bool, *protocheck.Violation) {
	t := args[0].(*testpb.TempoRange)
	if t.GetMinBpm() <= t.GetMaxBpm() {
		return true, nil
	}
	return false, &protocheck.Violation{Code: "acme.tempo", FallbackMessage: "min_bpm must not exceed max_bpm"}
}

func TestBuiltinCheckFieldRule(t *testing.T) {
	v := mustValidator(t)

	expectValid(t, v, &testpb.Discount{Percentage: 50})
	ve := mustValidationError(t, v.Validate(&testpb.Discount{Percentage: -5}))
	vio := findViolation(ve, "valid_percentage")
	if vio == nil {
		t.Fatalf("expected valid_percentage violation, got %v", ve.Violations)
	}
	if got, want := vio.Cause.FallbackMessage, "percentage must be between 0 and 100"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}

	expectValid(t, v, &testpb.Handle{Name: "@bob"})
	expectInvalid(t, v, &testpb.Handle{Name: "bob"}, "handle_shape")
	expectInvalid(t, v, &testpb.Handle{Name: "@" + strings.Repeat("b", 16)}, "handle_shape")
}

func TestBuiltinCheckRegisteredFunction(t *testing.T) {
	v, err := protocheck.New(protocheck.WithFunction("acme.v1.tempo_ordered", protocheck.Function(tempoOrdered)))
	if err != nil {
		t.Fatal(err)
	}

	expectValid(t, v, &testpb.TempoRange{MinBpm: 90, MaxBpm: 120})
	ve := mustValidationError(t, v.Validate(&testpb.TempoRange{MinBpm: 120, MaxBpm: 90}))
	if len(ve.Violations) != 1 {
		t.Fatalf("violations = %v, want only tempo_ordered", ve.Violations)
	}
	vio := findViolation(ve, "tempo_ordered")
	if vio == nil {
		t.Fatalf("expected tempo_ordered violation, got %v", ve.Violations)
	}
	// The rule sets no message, so the function's own message is used.
	if got, want := vio.Cause.FallbackMessage, "min_bpm must not exceed max_bpm"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// A callee resolves by FQN or by a suffix exactly one registered function
// has; an unregistered or ambiguous name fails the rule rather than passing.
func TestBuiltinCheckUnresolvedFunction(t *testing.T) {
	for name, opts := range map[string][]protocheck.Option{
		"unregistered": nil,
		"ambiguous": {
			protocheck.WithFunction("acme.v1.tempo_ordered", protocheck.Function(tempoOrdered)),
			protocheck.WithFunction("other.tempo_ordered", protocheck.Function(tempoOrdered)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := protocheck.New(opts...)
			if err != nil {
				t.Fatal(err)
			}
			ve := mustValidationError(t, v.Validate(&testpb.TempoRange{MinBpm: 90, MaxBpm: 120}))
			vio := findViolation(ve, "tempo_ordered")
			if vio == nil {
				t.Fatalf("expected tempo_ordered violation, got %v", ve.Violations)
			}
			if !strings.Contains(vio.Cause.FallbackMessage, `unknown function "tempo_ordered"`) {
				t.Errorf("message = %q, want an unknown-function error", vio.Cause.FallbackMessage)
			}
		})
	}
}

// Expressions written for an engine's own language (here bare field
// names) fail as parse errors with no engine, never pass silently.
func TestBuiltinCheckForeignSyntaxFails(t *testing.T) {
	v := mustValidator(t)
	ve := mustValidationError(t, v.Validate(&testpb.DateRange{StartEpoch: 1, EndEpoch: 2}))
	vio := findViolation(ve, "valid_range")
	if vio == nil {
		t.Fatalf("expected valid_range violation, got %v", ve.Violations)
	}
	if !strings.Contains(vio.Cause.FallbackMessage, "parsing") {
		t.Errorf("message = %q, want a parse error", vio.Cause.FallbackMessage)
	}

	ve = mustValidationError(t, v.Validate(&testpb.BadFieldCompile{S: "x"}))
	if vio := findViolation(ve, "compile_fail"); vio == nil || !strings.Contains(vio.Cause.FallbackMessage, "parsing") {
		t.Errorf("compile_fail violation = %v, want a parse error", vio)
	}
}

// A field-level check on a message-typed field is evaluated with `this`
// bound per RFC-001 §6.2: the plain message as itself, a Timestamp as
// time.Time (#68). Unset fields are not evaluated (§6.1).
func TestBuiltinCheckMessageField(t *testing.T) {
	v := mustValidator(t)

	expectValid(t, v, &testpb.MessageFieldCheck{})
	expectInvalid(t, v, &testpb.MessageFieldCheck{Inner: &testpb.CheckedInner{}}, "inner_never")

	expectValid(t, v, &testpb.MessageFieldCheck{Due: timestamppb.New(time.Now().Add(-time.Hour))})
	expectInvalid(t, v, &testpb.MessageFieldCheck{Due: timestamppb.New(time.Now().Add(time.Hour))}, "due_not_future")
}

// Repeated items and map values that are messages bind `this` per RFC-001
// §6.2 too, and a map value's check rules run (#73).
func TestBuiltinCheckMessageCollections(t *testing.T) {
	v := mustValidator(t)
	past := timestamppb.New(time.Now().Add(-time.Hour))
	future := timestamppb.New(time.Now().Add(time.Hour))

	expectValid(t, v, &testpb.MessageCollectionCheck{})
	expectValid(t, v, &testpb.MessageCollectionCheck{Stamps: []*timestamppb.Timestamp{past}})
	expectInvalid(t, v, &testpb.MessageCollectionCheck{Stamps: []*timestamppb.Timestamp{past, future}}, "stamp_not_future")

	expectValid(t, v, &testpb.MessageCollectionCheck{DueByName: map[string]*timestamppb.Timestamp{"a": past}})
	expectInvalid(t, v, &testpb.MessageCollectionCheck{DueByName: map[string]*timestamppb.Timestamp{"a": future}}, "due_not_future")

	expectInvalid(t, v, &testpb.MessageCollectionCheck{InnerByName: map[string]*testpb.CheckedInner{"a": {}}}, "inner_never")
}
