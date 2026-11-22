package protocheck_test

import (
	"maps"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// What a function's returned Violation contributes to a @validate rule's
// report (RFC-001 §7): its Params always, its Code unless the rule sets
// `code`, its message unless the rule sets `message` (#47).
const fnViolationSrc = `syntax = "proto3";
package fnviolation;

import "protowire/schema/v1/annotations.proto";

function refuse(msg: Bare);
function refuse_coded(msg: Coded);
function refuse_both(msg: Both);

@validate(refuse(this))
message Bare {}

@validate(refuse_coded(this), code = "rule.code")
message Coded {}

@validate(refuse_both(this), code = "rule.code", message = "rule message")
message Both {}
`

func TestValidateKeepsFunctionViolation(t *testing.T) {
	fd := compileProtos(t, map[string]string{"fnviolation.proto": fnViolationSrc}, "fnviolation.proto")["fnviolation.proto"]
	refuse := protocheck.Function(func([]any) (bool, *protocheck.Violation) {
		return false, &protocheck.Violation{
			Code:            "fn.code",
			Params:          map[string]any{"bpm": 0.0},
			FallbackMessage: "fn message",
		}
	})
	v, err := protocheck.New(
		protocheck.WithFunction("fnviolation.refuse", refuse),
		protocheck.WithFunction("fnviolation.refuse_coded", refuse),
		protocheck.WithFunction("fnviolation.refuse_both", refuse),
		protocheck.WithDescriptors(fd),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	wantParams := map[string]any{"bpm": 0.0}
	for _, tc := range []struct {
		message, code, msg string
	}{
		{"Bare", "fn.code", "fn message"},
		{"Coded", "rule.code", "fn message"},
		{"Both", "rule.code", "rule message"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			md := fd.Messages().ByName(protoreflect.Name(tc.message))
			got := carrierViolations(t, v, dynamicpb.NewMessage(md))
			if len(got) != 1 {
				t.Fatalf("violations = %v, want one", got)
			}
			cause := got[0].Cause
			if cause.Code != tc.code {
				t.Errorf("code = %q, want %q", cause.Code, tc.code)
			}
			if cause.FallbackMessage != tc.msg {
				t.Errorf("message = %q, want %q", cause.FallbackMessage, tc.msg)
			}
			if !maps.Equal(cause.Params, wantParams) {
				t.Errorf("params = %v, want %v", cause.Params, wantParams)
			}
		})
	}
}

// A bracket check rule keeps a function's Violation the same way: its Params
// always, with "expression" added; its Code unless the rule has an id; its
// message unless the rule sets one (#47).
const fnCheckSrc = `syntax = "proto3";
package fncheck;

import "protocheck/protocheck.proto";

message NoID {
  option (protocheck.message) = { check: [{ expression: "refuse(this)" }] };
}

message WithID {
  option (protocheck.message) = { check: [{ id: "rule.code", expression: "refuse(this)" }] };
}

message WithIDAndMessage {
  option (protocheck.message) = {
    check: [{ id: "rule.code", message: "rule message", expression: "refuse(this)" }]
  };
}
`

func TestCheckKeepsFunctionViolation(t *testing.T) {
	fd := compileProtos(t, map[string]string{"fncheck.proto": fnCheckSrc}, "fncheck.proto")["fncheck.proto"]
	v, err := protocheck.New(protocheck.WithFunction("fncheck.refuse",
		protocheck.Function(func([]any) (bool, *protocheck.Violation) {
			return false, &protocheck.Violation{
				Code:            "fn.code",
				Params:          map[string]any{"bpm": 0.0, "expression": "from the function"},
				FallbackMessage: "fn message",
			}
		})))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	wantParams := map[string]any{"bpm": 0.0, "expression": "refuse(this)"}
	for _, tc := range []struct {
		message, code, msg string
	}{
		{"NoID", "fn.code", "fn message"},
		{"WithID", "rule.code", "fn message"},
		{"WithIDAndMessage", "rule.code", "rule message"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			md := fd.Messages().ByName(protoreflect.Name(tc.message))
			got := carrierViolations(t, v, dynamicpb.NewMessage(md))
			if len(got) != 1 {
				t.Fatalf("violations = %v, want one", got)
			}
			cause := got[0].Cause
			if cause.Code != tc.code {
				t.Errorf("code = %q, want %q", cause.Code, tc.code)
			}
			if cause.FallbackMessage != tc.msg {
				t.Errorf("message = %q, want %q", cause.FallbackMessage, tc.msg)
			}
			if !maps.Equal(cause.Params, wantParams) {
				t.Errorf("params = %v, want %v", cause.Params, wantParams)
			}
		})
	}
}
