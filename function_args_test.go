package protocheck

// Function arguments arrive in their declared type (RFC-001 §9.1,
// protobuf-go#11): from a field and from a literal alike, through aliases and
// wrappers, and refused when they do not fit.

import (
	"math"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const functionArgsSchema = `syntax = "proto3";
package t;

import "google/protobuf/wrappers.proto";
import "protowire/schema/v1/annotations.proto";

type Small = int32;
type Smaller = Small;

function f_int32(v: int32);
function f_int64(v: int64);
function f_uint32(v: uint32);
function f_uint64(v: uint64);
function f_float(v: float);
function f_double(v: double);
function f_alias(v: Smaller);
function f_wrapper(v: google.protobuf.Int32Value);
function f_string(v: string);
function l_int32(v: int32);
function l_int64(v: int64);
function l_double(v: double);
function l_overflow(v: int32);

message M {
  int32 a = 1 @validate(f_int32(this));
  int64 b = 2 @validate(f_int64(this));
  uint32 c = 3 @validate(f_uint32(this));
  uint64 d = 4 @validate(f_uint64(this));
  float e = 5 @validate(f_float(this));
  double f = 6 @validate(f_double(this));
  int32 g = 7 @validate(f_alias(this));
  google.protobuf.Int32Value h = 8 @validate(f_wrapper(this));
  string i = 9 @validate(f_string(this));
  string j = 10 @validate(l_int32(5));
  string k = 11 @validate(l_int64(5));
  string l = 12 @validate(l_double(5));
  string m = 13 @validate(l_overflow(5000000000));
}
`

func TestFunctionArgsArriveDeclared(t *testing.T) {
	fd := compileInline(t, "t.proto", functionArgsSchema)
	got := map[string]any{}
	fn := func(name string) Option {
		return WithFunction("t."+name, Function(func(args []any) (bool, *Violation) {
			got[name] = args[0]
			return true, nil
		}))
	}
	var opts []Option
	for _, n := range []string{"f_int32", "f_int64", "f_uint32", "f_uint64", "f_float", "f_double", "f_alias", "f_wrapper", "f_string", "l_int32", "l_int64", "l_double", "l_overflow"} {
		opts = append(opts, fn(n))
	}
	v, err := New(append(opts, WithDescriptors(fd))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })

	md := fd.Messages().ByName("M")
	m := dynamicpb.NewMessage(md)
	field := func(name protoreflect.Name) protoreflect.FieldDescriptor { return md.Fields().ByName(name) }
	m.Set(field("a"), protoreflect.ValueOfInt32(7))
	m.Set(field("b"), protoreflect.ValueOfInt64(7))
	m.Set(field("c"), protoreflect.ValueOfUint32(7))
	m.Set(field("d"), protoreflect.ValueOfUint64(7))
	m.Set(field("e"), protoreflect.ValueOfFloat32(1.5))
	m.Set(field("f"), protoreflect.ValueOfFloat64(1.5))
	m.Set(field("g"), protoreflect.ValueOfInt32(7))
	h := dynamicpb.NewMessage(wrapperspb.File_google_protobuf_wrappers_proto.Messages().ByName("Int32Value"))
	h.Set(h.Descriptor().Fields().ByName("value"), protoreflect.ValueOfInt32(7))
	m.Set(field("h"), protoreflect.ValueOfMessage(h))
	for _, name := range []protoreflect.Name{"i", "j", "k", "l", "m"} {
		m.Set(field(name), protoreflect.ValueOfString("x"))
	}

	viols := codes(v, m)
	for name, want := range map[string]any{
		"f_int32": int32(7), "f_int64": int64(7), "f_uint32": uint32(7), "f_uint64": uint64(7),
		"f_float": float32(1.5), "f_double": float64(1.5), "f_alias": int32(7), "f_wrapper": int32(7),
		"f_string": "x", "l_int32": int32(5), "l_int64": int64(5), "l_double": float64(5),
	} {
		if got[name] != want {
			t.Errorf("%s got %#v, want %#v", name, got[name], want)
		}
	}
	// The one refusal: a literal too big for its parameter never reaches the
	// implementation.
	if _, called := got["l_overflow"]; called {
		t.Error("l_overflow reached its implementation")
	}
	if len(viols) != 1 || viols[0] != "m:"+CodeFunctionInvalidArgument {
		t.Errorf("violations = %v, want only m:%s", viols, CodeFunctionInvalidArgument)
	}
}

func TestConvertArg(t *testing.T) {
	for _, tt := range []struct {
		declared string
		in, want any
		ok       bool
	}{
		{"int32", int(math.MaxInt32), int32(math.MaxInt32), true},
		{"int32", int64(math.MaxInt32) + 1, nil, false},
		{"int32", int64(math.MinInt32) - 1, nil, false},
		{"int32", 1.5, nil, false},
		{"int32", "7", nil, false},
		{"int64", uint64(math.MaxInt64), int64(math.MaxInt64), true},
		{"int64", uint64(math.MaxInt64) + 1, nil, false},
		{"uint32", int(-1), nil, false},
		{"uint32", uint64(math.MaxUint32) + 1, nil, false},
		{"uint64", int64(7), uint64(7), true},
		{"uint64", int64(-1), nil, false},
		{"float", float64(math.MaxFloat64), nil, false},
		{"float", math.Inf(1), float32(math.Inf(1)), true},
		{"double", int64(5), float64(5), true},
	} {
		got, ok := convertArg(tt.declared, tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("convertArg(%s, %#v) = %#v, %v; want %#v, %v", tt.declared, tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
