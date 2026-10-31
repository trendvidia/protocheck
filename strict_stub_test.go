package protocheck_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const stubSrc = `syntax = "proto3";
package fixtures.expressions;

import "protowire/schema/v1/annotations.proto";

function in_region(value: string);
function is_iban(value: string);

message Account {
  string country = 1 @validate(in_region(this), code = "account.bad_region");
  string iban = 2 @validate(is_iban(this));
}
`

const isIbanFQN = "fixtures.expressions.is_iban"

// stubFunctions has the shape of RFC-001 §9.3 generated code, guards included.
type stubFunctions interface {
	InRegion(value string) (bool, *protocheck.Violation)
	IsIban(value string) (bool, *protocheck.Violation)
}

type unimplementedStubFunctions struct{}

func (unimplementedStubFunctions) InRegion(string) (bool, *protocheck.Violation) {
	return protocheck.UnimplementedFunction(inRegionFQN)(nil)
}

func (unimplementedStubFunctions) IsIban(string) (bool, *protocheck.Violation) {
	return protocheck.UnimplementedFunction(isIbanFQN)(nil)
}

func registerStubFunctions(eng protocheck.Engine, impl stubFunctions) error {
	guarded := func(call func(string) (bool, *protocheck.Violation)) protocheck.Function {
		return func(args []any) (bool, *protocheck.Violation) {
			a0, ok := args[0].(string)
			if !ok {
				return false, &protocheck.Violation{Code: protocheck.CodeFunctionInvalidArgument}
			}
			return call(a0)
		}
	}
	if err := eng.Register(inRegionFQN, guarded(impl.InRegion)); err != nil {
		return err
	}
	return eng.Register(isIbanFQN, guarded(impl.IsIban))
}

type inRegionOnly struct{ unimplementedStubFunctions }

func (inRegionOnly) InRegion(string) (bool, *protocheck.Violation) { return true, nil }

type inRegionByPointer struct{ unimplementedStubFunctions }

func (*inRegionByPointer) InRegion(string) (bool, *protocheck.Violation) { return true, nil }

type bothImplemented struct{ inRegionOnly }

func (bothImplemented) IsIban(string) (bool, *protocheck.Violation) { return true, nil }

type refusesZero struct{ inRegionOnly }

func (refusesZero) IsIban(string) (bool, *protocheck.Violation) {
	return false, &protocheck.Violation{Code: "iban.empty"}
}

type panicsOnZero struct{ inRegionOnly }

func (panicsOnZero) IsIban(string) (bool, *protocheck.Violation) { panic("empty") }

// rewritesCode hides a stub behind its own code.
type rewritesCode struct{ inRegionOnly }

func (rewritesCode) IsIban(v string) (bool, *protocheck.Violation) {
	_, viol := unimplementedStubFunctions{}.IsIban(v)
	return false, &protocheck.Violation{Code: "iban.todo", FallbackMessage: viol.FallbackMessage}
}

func newStrict(t *testing.T, fd protoreflect.FileDescriptor, impl stubFunctions) (*protocheck.Validator, error) {
	t.Helper()
	return protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithRegistration(func(e protocheck.Engine) error { return registerStubFunctions(e, impl) }),
		protocheck.WithStrictValidation(),
	)
}

func TestStrictRefusesStubs(t *testing.T) {
	fd := compileRFC(t, stubSrc)
	for _, tc := range []struct {
		name string
		impl stubFunctions
		want []string
	}{
		{"the default itself", unimplementedStubFunctions{}, []string{inRegionFQN, isIbanFQN}},
		{"embedded, nothing overridden", struct{ unimplementedStubFunctions }{}, []string{inRegionFQN, isIbanFQN}},
		{"one overridden", inRegionOnly{}, []string{isIbanFQN}},
		{"one overridden on a pointer", &inRegionByPointer{}, []string{isIbanFQN}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newStrict(t, fd, tc.impl)
			if err == nil {
				t.Fatal("New passed")
			}
			for _, fqn := range []string{inRegionFQN, isIbanFQN} {
				if strings.Contains(err.Error(), fqn) != slices.Contains(tc.want, fqn) {
					t.Errorf("New = %v, want it to name %v", err, tc.want)
				}
			}
		})
	}
}

// The zero value is the engine's choice of argument, so a real implementation
// refusing it says nothing.
func TestStrictAcceptsImplementations(t *testing.T) {
	fd := compileRFC(t, stubSrc)
	for name, impl := range map[string]stubFunctions{
		"implemented":    bothImplemented{},
		"refuses zero":   refusesZero{},
		"panics on zero": panicsOnZero{},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := newStrict(t, fd, impl)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			v.Close()
		})
	}
}

// Pins what strict does not promise: only the reserved code identifies a stub,
// so one behind a wrapper that rewrites it passes New, and fails closed at
// first call.
func TestStrictPassesAStubThatRewritesItsCode(t *testing.T) {
	fd := compileRFC(t, stubSrc)
	v, err := newStrict(t, fd, rewritesCode{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.Close()

	msg := newDynamic(t, fd, "Account", map[string]string{"country": "US", "iban": "x"})
	if s := violationCodes(carrierViolations(t, v, msg)); s != "iban=iban.todo" {
		t.Fatalf("violations = %s", s)
	}
}

func TestLenientCallsNoFunctionDuringNew(t *testing.T) {
	calls := 0
	v, err := protocheck.New(
		protocheck.WithDescriptors(compileRFC(t, regionSrc)),
		protocheck.WithFunction(inRegionFQN, protocheck.Function(func([]any) (bool, *protocheck.Violation) {
			calls++
			return true, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if calls != 0 {
		t.Fatalf("New called the function %d times", calls)
	}
}

const strictArgsLib = `syntax = "proto3";
package lib;

import "protowire/schema/v1/annotations.proto";

type Label = string;
type Count = int32;

enum Color {
  COLOR_UNSPECIFIED = 0;
  COLOR_RED = 1;
}

message Inner { string s = 1; }
`

// strictArgTypes names every declared parameter shape of the RFC-001 §9.1 table.
var strictArgTypes = []string{
	"string", "bool", "bytes", "int32", "sint32", "sfixed32", "int64", "sint64",
	"sfixed64", "uint32", "fixed32", "uint64", "fixed64", "float", "double",
	"Local", "lib.Color", "Nested", "lib.Inner", "google.protobuf.Any",
	"google.protobuf.Timestamp", "google.protobuf.Duration",
	"google.protobuf.StringValue", "google.protobuf.BytesValue",
	"google.protobuf.BoolValue", "google.protobuf.Int32Value",
	"google.protobuf.Int64Value", "google.protobuf.UInt32Value",
	"google.protobuf.UInt64Value", "google.protobuf.FloatValue",
	"google.protobuf.DoubleValue", "Smaller", "Name", "lib.Count", "lib.Label",
}

func strictArgsSchema() string {
	var b strings.Builder
	b.WriteString(`syntax = "proto3";
package t;

import "google/protobuf/any.proto";
import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";
import "google/protobuf/wrappers.proto";
import "lib.proto";
import "protowire/schema/v1/annotations.proto";

type Small = int32;
type Smaller = Small;
type Name = string;

enum Local {
  LOCAL_UNSPECIFIED = 0;
  LOCAL_ONE = 1;
}

message Nested { string s = 1; }

`)
	for i, typ := range strictArgTypes {
		fmt.Fprintf(&b, "function f%d(v: %s);\n", i, typ)
	}
	b.WriteString("\nmessage M {\n")
	for i, typ := range strictArgTypes {
		fmt.Fprintf(&b, "  %s a%d = %d @validate(f%d(this));\n", typ, i, i+1, i)
	}
	b.WriteString("}\n")
	return b.String()
}

// setEveryField gives each field of m a non-zero value, so each rule runs.
func setEveryField(m protoreflect.Message) {
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		switch fd.Kind() {
		case protoreflect.MessageKind:
			m.Mutable(fd)
		case protoreflect.EnumKind:
			m.Set(fd, protoreflect.ValueOfEnum(1))
		case protoreflect.StringKind:
			m.Set(fd, protoreflect.ValueOfString("x"))
		case protoreflect.BytesKind:
			m.Set(fd, protoreflect.ValueOfBytes([]byte("x")))
		case protoreflect.BoolKind:
			m.Set(fd, protoreflect.ValueOfBool(true))
		case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
			m.Set(fd, protoreflect.ValueOfInt32(1))
		case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
			m.Set(fd, protoreflect.ValueOfInt64(1))
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
			m.Set(fd, protoreflect.ValueOfUint32(1))
		case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			m.Set(fd, protoreflect.ValueOfUint64(1))
		case protoreflect.FloatKind:
			m.Set(fd, protoreflect.ValueOfFloat32(1))
		case protoreflect.DoubleKind:
			m.Set(fd, protoreflect.ValueOfFloat64(1))
		}
	}
}

// The check only sees a stub if the arguments pass the stub's guards, and those
// assert the type a rule's call delivers. So the call strict makes must deliver
// the same Go type as a real one, for every declared shape. A shape that
// diverges would hide a stub behind protowire.function.invalid_argument.
func TestStrictCallsWithTheTypesARuleDelivers(t *testing.T) {
	fds := compileProtos(t, map[string]string{"lib.proto": strictArgsLib, "t.proto": strictArgsSchema()}, "lib.proto", "t.proto")
	fd := fds["t.proto"]

	seen := make([][]any, len(strictArgTypes))
	opts := []protocheck.Option{protocheck.WithDescriptors(fd), protocheck.WithStrictValidation()}
	for i := range strictArgTypes {
		opts = append(opts, protocheck.WithFunction(fmt.Sprintf("t.f%d", i), protocheck.Function(func(args []any) (bool, *protocheck.Violation) {
			seen[i] = append(seen[i], args[0])
			return true, nil
		})))
	}
	v, err := protocheck.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.Close()

	m := dynamicpb.NewMessage(fd.Messages().ByName("M"))
	setEveryField(m)
	if err := v.Validate(m); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for i, typ := range strictArgTypes {
		if len(seen[i]) != 2 {
			t.Errorf("%s: called %d times, want once by strict and once by the rule", typ, len(seen[i]))
			continue
		}
		atStartup, inRule := seen[i][0], seen[i][1]
		// A message is its generated type or a dynamic one (§9.1), whichever
		// the caller has, and this test validates a dynamic one.
		_, startupMsg := atStartup.(proto.Message)
		_, ruleMsg := inRule.(proto.Message)
		if startupMsg && ruleMsg {
			continue
		}
		if a, b := fmt.Sprintf("%T", atStartup), fmt.Sprintf("%T", inRule); a != b {
			t.Errorf("%s: strict called with %s, a rule with %s", typ, a, b)
		}
	}
}
