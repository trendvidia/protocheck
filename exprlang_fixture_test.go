package protocheck_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trendvidia/protocheck/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	// Register the well-known types the schemas import.
	_ "google.golang.org/protobuf/types/known/timestamppb"
	_ "google.golang.org/protobuf/types/known/wrapperspb"
)

// ─── Fixture 24: the normative expression language ─────────────────────────

func fxInRegion(args []any) (bool, *protocheck.Violation) {
	regions, _ := args[1].(string)
	if regions == "NA" {
		return true, nil
	}
	return false, &protocheck.Violation{FallbackMessage: "unknown region set"}
}

func fxTrue([]any) (bool, *protocheck.Violation) { return true, nil }

// newProfile builds a fixtures.exprlang.Profile that satisfies every rule
// of fixture 24, with `expires` at the given instant.
func newProfile(t *testing.T, fd protoreflect.FileDescriptor, expires time.Time, age int32) proto.Message {
	t.Helper()
	md := fd.Messages().ByName("Profile")
	if md == nil {
		t.Fatal("Profile not found")
	}
	msg := dynamicpb.NewMessage(md)
	field := func(name string) protoreflect.FieldDescriptor {
		f := md.Fields().ByName(protoreflect.Name(name))
		if f == nil {
			t.Fatalf("no field %s", name)
		}
		return f
	}
	msg.Set(field("email"), protoreflect.ValueOfString("alice@acme.com"))
	msg.Set(field("handle"), protoreflect.ValueOfString("u_alice"))
	msg.Set(field("country"), protoreflect.ValueOfString("US"))
	msg.Set(field("age"), protoreflect.ValueOfInt32(age))
	msg.Set(field("score"), protoreflect.ValueOfFloat64(0.5))
	msg.Set(field("verified"), protoreflect.ValueOfBool(true))
	msg.Set(field("token"), protoreflect.ValueOfBytes([]byte(strings.Repeat("k", 32))))
	tags := msg.Mutable(field("tags")).List()
	tags.Append(protoreflect.ValueOfString("golang"))
	tags.Append(protoreflect.ValueOfString("protobuf"))
	msg.Mutable(field("limits")).Map().Set(protoreflect.ValueOfString("rps").MapKey(), protoreflect.ValueOfInt32(100))
	exp := msg.Mutable(field("expires")).Message()
	exp.Set(exp.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(expires.Unix()))
	exp.Set(exp.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(int32(expires.Nanosecond())))
	return msg
}

// Every rule of protowire's conformance fixture 24 evaluates: every
// builtin in both forms, now() against a Timestamp field, every operator
// and literal kind, declared-function calls, and an alias rule applied per
// element of a repeated field.
func TestFixture24ExpressionLanguage(t *testing.T) {
	fd := compileFixtures(t, "24_expression_language.proto")["24_expression_language.proto"]
	v, err := protocheck.New(
		protocheck.WithFunction("fixtures.exprlang.in_region", protocheck.Function(fxInRegion)),
		protocheck.WithFunction("fixtures.exprlang.same_domain", protocheck.Function(fxTrue)),
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if got := carrierViolations(t, v, newProfile(t, fd, time.Now().Add(time.Hour), 30)); len(got) != 0 {
		t.Fatalf("a conforming Profile produced violations: %v", got)
	}

	// expires in the past: `this > now()` fails, and only it.
	got := carrierViolations(t, v, newProfile(t, fd, time.Now().Add(-time.Hour), 30))
	if len(got) != 1 {
		t.Fatalf("expected exactly the expires violation, got %d: %v", len(got), got)
	}
	if p := fmt.Sprint(got[0].Path); !strings.Contains(p, "expires") {
		t.Errorf("violation path = %s, want expires", p)
	}
	if _, ok := got[0].ActualValue.(time.Time); !ok {
		t.Errorf("ActualValue = %T, want time.Time (§6.2 rule 2 binding)", got[0].ActualValue)
	}

	// age == 42: a numeric rule beside the temporal one, to show the two
	// paths do not interfere.
	got = carrierViolations(t, v, newProfile(t, fd, time.Now().Add(time.Hour), 42))
	if len(got) != 1 || !strings.Contains(fmt.Sprint(got[0].Path), "age") {
		t.Fatalf("expected exactly the age violation, got %v", got)
	}
}

// ─── Wrappers bind unwrapped (RFC-001 §6.2 rule 1) ─────────────────────────

func TestCarrierWrapperFieldsBindUnwrapped(t *testing.T) {
	const path = "wrap.proto"
	fd := compileProtos(t, map[string]string{path: `syntax = "proto3";
package t;

import "google/protobuf/wrappers.proto";
import "protowire/schema/v1/annotations.proto";

message W {
  google.protobuf.StringValue name = 1 @validate(this.starts_with("a"));
  google.protobuf.Int64Value count = 2 @validate(this > 10);
  google.protobuf.BoolValue on = 3 @validate(this == true);
}
`}, path)[path]
	v, err := protocheck.New(protocheck.WithDescriptors(fd))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	build := func(name string, count int64, on bool) proto.Message {
		md := fd.Messages().ByName("W")
		msg := dynamicpb.NewMessage(md)
		set := func(field string, val protoreflect.Value) {
			f := md.Fields().ByName(protoreflect.Name(field))
			sub := msg.Mutable(f).Message()
			sub.Set(sub.Descriptor().Fields().ByName("value"), val)
		}
		set("name", protoreflect.ValueOfString(name))
		set("count", protoreflect.ValueOfInt64(count))
		set("on", protoreflect.ValueOfBool(on))
		return msg
	}
	if got := carrierViolations(t, v, build("alice", 11, true)); len(got) != 0 {
		t.Fatalf("unwrapped values satisfy the rules; got %v", got)
	}
	got := carrierViolations(t, v, build("bob", 5, false))
	if len(got) != 3 {
		t.Fatalf("expected three violations, got %d: %v", len(got), got)
	}
	if got[0].ActualValue != "bob" {
		t.Errorf("ActualValue = %v (%T), want the unwrapped string", got[0].ActualValue, got[0].ActualValue)
	}
}
