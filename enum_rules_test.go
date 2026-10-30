package protocheck

// Enum value names, defined() and unique() (RFC-001 §5.4, protowire#427,
// #11): rules written the way daysong writes them, evaluated through the
// carrier path, and checked against protocheck's bracket rules on the
// same inputs.

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const enumRulesSchema = `syntax = "proto3";
package t;

import "protowire/schema/v1/annotations.proto";

function mode_seen(m: int32);

enum Mode {
  MODE_UNSPECIFIED = 0;
  MODE_MATCH = 1;
  MODE_REGULATE = 2;
}

enum StyleTag {
  STYLE_TAG_UNSPECIFIED = 0;
  STYLE_TAG_WARM = 1;
  STYLE_TAG_DARK = 2;
}

type KnownTag = StyleTag
  @validate(this.defined() && this != STYLE_TAG_UNSPECIFIED, code = "tag.known");

message Moment {
  optional Mode mood = 1 @validate(defined(this), code = "moment.mood");
}

message Descriptor {
  Mode mode = 1 @validate(this in [MODE_MATCH, Mode.MODE_REGULATE], code = "mode.known");
  Mode mode2 = 2 @validate(this.defined() && this != t.MODE_UNSPECIFIED, code = "mode.defined");
  repeated KnownTag tags = 3 @validate(this.unique(), code = "tags.unique");
  repeated string labels = 4 @validate(unique(this), code = "labels.unique");
  map<string, KnownTag> tag_by_name = 5;
  Mode seen = 6 @validate(mode_seen(this), code = "mode.seen");
}
`

func enumRulesValidator(t *testing.T, opts ...Option) (*Validator, protoreflect.FileDescriptor) {
	t.Helper()
	fd := compileInline(t, "t.proto", enumRulesSchema)
	if len(opts) == 0 {
		opts = []Option{WithFunction("t.mode_seen", Function(func([]any) (bool, *Violation) { return true, nil }))}
	}
	v, err := New(append(opts, WithDescriptors(fd))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v, fd
}

// codes runs one validation and returns the violation codes, path:code.
func codes(v *Validator, msg protoreflect.Message) []string {
	var out []string
	for _, viol := range v.validate(runInstant, &carrierRun{index: v.carrier}, nil, msg) {
		out = append(out, viol.Path.String()+":"+viol.Cause.Code)
	}
	return out
}

func TestEnumNamesDefinedUnique(t *testing.T) {
	v, fd := enumRulesValidator(t)
	md := fd.Messages().ByName("Descriptor")
	field := func(name string) protoreflect.FieldDescriptor { return md.Fields().ByName(protoreflect.Name(name)) }
	valid := func() *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		m.Set(field("mode"), protoreflect.ValueOfEnum(1))
		m.Set(field("mode2"), protoreflect.ValueOfEnum(2))
		return m
	}
	setList := func(m *dynamicpb.Message, name string, vals ...protoreflect.Value) {
		l := m.Mutable(field(name)).List()
		for _, x := range vals {
			l.Append(x)
		}
	}
	enum := func(n int32) protoreflect.Value { return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)) }
	str := protoreflect.ValueOfString

	if got := codes(v, valid()); len(got) != 0 {
		t.Fatalf("valid message: %v", got)
	}
	cases := []struct {
		name  string
		build func(*dynamicpb.Message)
		want  string
	}{
		{"mode by name, 2 is MODE_REGULATE", func(m *dynamicpb.Message) { m.Set(field("mode"), enum(2)) }, ""},
		// Zero on a proto3 enum is unset (§6.1), so its rule is not evaluated;
		// daysong pairs these rules with @required for that.
		{"mode zero is unset", func(m *dynamicpb.Message) { m.Set(field("mode"), enum(0)) }, ""},
		{"mode undeclared", func(m *dynamicpb.Message) { m.Set(field("mode"), enum(7)) }, "mode:mode.known"},
		{"defined refuses an undeclared number", func(m *dynamicpb.Message) { m.Set(field("mode2"), enum(7)) }, "mode2:mode.defined"},
		{"distinct known tags", func(m *dynamicpb.Message) { setList(m, "tags", enum(1), enum(2)) }, ""},
		{"duplicate tag", func(m *dynamicpb.Message) { setList(m, "tags", enum(1), enum(1)) }, "tags:tags.unique"},
		{"undeclared tag, through the alias", func(m *dynamicpb.Message) { setList(m, "tags", enum(1), enum(9)) }, "tags[1]:tag.known"},
		{"UNSPECIFIED tag", func(m *dynamicpb.Message) { setList(m, "tags", enum(0)) }, "tags[0]:tag.known"},
		{"distinct labels", func(m *dynamicpb.Message) { setList(m, "labels", str("a"), str("b")) }, ""},
		{"duplicate label", func(m *dynamicpb.Message) { setList(m, "labels", str("a"), str("b"), str("a")) }, "labels:labels.unique"},
		{"undeclared map value, through the alias", func(m *dynamicpb.Message) {
			m.Mutable(field("tag_by_name")).Map().Set(protoreflect.ValueOfString("x").MapKey(), enum(5))
		}, `tag_by_name[x]:tag.known`},
	}
	for _, tc := range cases {
		m := valid()
		tc.build(m)
		got := strings.Join(codes(v, m), ",")
		if got != tc.want {
			t.Errorf("%s: violations %q, want %q", tc.name, got, tc.want)
		}
	}

	// A declared enum value reports by its enum-qualified name, an
	// undeclared one by its number, which has no name.
	for _, tc := range []struct {
		build func(*dynamicpb.Message)
		want  any
	}{
		{func(m *dynamicpb.Message) { m.Set(field("mode2"), enum(7)) }, 7},
		{func(m *dynamicpb.Message) { setList(m, "tags", enum(0)) }, EnumName("t.StyleTag.STYLE_TAG_UNSPECIFIED")},
		{func(m *dynamicpb.Message) {
			m.Mutable(field("tag_by_name")).Map().Set(protoreflect.ValueOfString("x").MapKey(), enum(0))
		}, EnumName("t.StyleTag.STYLE_TAG_UNSPECIFIED")},
	} {
		m := valid()
		tc.build(m)
		got := v.validate(runInstant, &carrierRun{index: v.carrier}, nil, m)
		if len(got) != 1 || got[0].ActualValue != tc.want {
			t.Errorf("ActualValue: got %v, want %v (%T)", got, tc.want, tc.want)
		}
	}
}

// An optional enum (daysong's Moment fields): unset skips the rule, a set
// undeclared number fails it, and the zero value is declared.
func TestDefinedOnOptionalEnum(t *testing.T) {
	v, fd := enumRulesValidator(t)
	md := fd.Messages().ByName("Moment")
	mood := md.Fields().ByName("mood")
	for _, tc := range []struct {
		set  bool
		n    protoreflect.EnumNumber
		want string
	}{
		{false, 0, ""},
		{true, 0, ""},
		{true, 2, ""},
		{true, 3, "mood:moment.mood"},
	} {
		m := dynamicpb.NewMessage(md)
		if tc.set {
			m.Set(mood, protoreflect.ValueOfEnum(tc.n))
		}
		if got := strings.Join(codes(v, m), ","); got != tc.want {
			t.Errorf("set=%v n=%d: %q, want %q", tc.set, tc.n, got, tc.want)
		}
	}
}

// A declared function still receives an enum as the plain int it always
// has: the enum type the binding carries for defined() stays inside the
// language.
func TestDeclaredFunctionGetsEnumAsInt(t *testing.T) {
	var got []any
	v, fd := enumRulesValidator(t, WithFunction("t.mode_seen", Function(func(args []any) (bool, *Violation) {
		got = args
		return true, nil
	})))
	md := fd.Messages().ByName("Descriptor")
	m := dynamicpb.NewMessage(md)
	m.Set(md.Fields().ByName("mode"), protoreflect.ValueOfEnum(1))
	m.Set(md.Fields().ByName("mode2"), protoreflect.ValueOfEnum(1))
	m.Set(md.Fields().ByName("seen"), protoreflect.ValueOfEnum(2))
	v.validate(runInstant, &carrierRun{index: v.carrier}, nil, m)
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("mode_seen args = %#v, want []any{int(2)}", got)
	}
}

// The language edges of the three additions, without a schema.
func TestExprEnumRefsDefinedUnique(t *testing.T) {
	enums := map[string]int32{"MODE_MATCH": 1, "Mode.MODE_REGULATE": 2}
	eval := func(src string, this any) (bool, error) {
		t.Helper()
		e, err := compileExpr(src, nil, enums)
		if err != nil {
			t.Fatalf("compileExpr(%q): %v", src, err)
		}
		ok, _, err := e.eval(this, runInstant, noCall)
		return ok, err
	}
	for src, want := range map[string]bool{
		`this == MODE_MATCH`:                       true,
		`this in [MODE_MATCH, Mode.MODE_REGULATE]`: true,
		`MODE_MATCH < Mode.MODE_REGULATE`:          true,
		`defined(MODE_MATCH)`:                      true,
		`this == 1`:                                true,
	} {
		if ok, err := eval(src, int(1)); err != nil || ok != want {
			t.Errorf("%s = (%v, %v), want (%v, nil)", src, ok, err, want)
		}
	}

	// The engine never resolves a name: one the compiler did not record is
	// a load-time error, and so is `in` where a term belongs.
	for _, src := range []string{`this == MODE_OTHER`, `this == in`} {
		if _, err := compileExpr(src, nil, enums); err == nil {
			t.Errorf("%s: want a compile error", src)
		}
	}
	if _, err := compileExpr(`this == MODE_MATCH`, nil, nil); err == nil || !strings.Contains(err.Error(), "Expression.enum_values") {
		t.Errorf("a name with no enum_values entry: got %v", err)
	}

	// defined() needs an enum value; a plain integer is an evaluation error.
	if _, err := eval(`this.defined()`, int(1)); err == nil || !strings.Contains(err.Error(), "needs an enum value") {
		t.Errorf("defined() on an int: got %v", err)
	}

	for _, tc := range []struct {
		this any
		want bool
		err  string
	}{
		{[]any{}, true, ""},
		{[]any{"a"}, true, ""},
		{[]any{"a", "b", "a"}, false, ""},
		{[]any{int(1), 1.0}, false, ""}, // equal by value
		{[]any{math.NaN(), math.NaN()}, true, ""},
		{[]any{[]byte("a"), []byte("a")}, false, "unique: cannot compare"},
		{"ab", false, "unique is not defined on string"},
	} {
		ok, err := eval(`this.unique()`, tc.this)
		switch {
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("unique(%v): got (%v, %v), want error %q", tc.this, ok, err, tc.err)
		case tc.err == "" && (err != nil || ok != tc.want):
			t.Errorf("unique(%v): got (%v, %v), want (%v, nil)", tc.this, ok, err, tc.want)
		}
	}
}

// defined() and unique() agree with the bracket rules they replace,
// enum.defined_only and repeated.unique, on the same inputs.
func TestDefinedUniqueMatchBrackets(t *testing.T) {
	fd := compileInlineWith(t, "p.proto", `syntax = "proto3";
package p;

import "protocheck/protocheck.proto";
import "protowire/schema/v1/annotations.proto";

enum Color {
  COLOR_UNSPECIFIED = 0;
  COLOR_RED = 1;
  COLOR_GREEN = 2;
  COLOR_BLUE = 5;
}

message Parity {
  Color bracket = 1 [(protocheck.field).enum.defined_only = true];
  Color builtin = 2 @validate(this.defined());
  repeated Color bracket_list = 3 [(protocheck.field).repeated.unique = true];
  repeated Color builtin_list = 4 @validate(this.unique());
  repeated string bracket_strs = 5 [(protocheck.field).repeated.unique = true];
  repeated string builtin_strs = 6 @validate(this.unique());
}
`, map[string]string{"protocheck/protocheck.proto": "proto/protocheck/protocheck.proto"})
	v, err := New(WithDescriptors(fd))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	md := fd.Messages().ByName("Parity")
	f := func(name string) protoreflect.FieldDescriptor { return md.Fields().ByName(protoreflect.Name(name)) }

	refused := func(m *dynamicpb.Message) map[string]bool {
		out := map[string]bool{}
		for _, viol := range v.validate(runInstant, &carrierRun{index: v.carrier}, nil, m) {
			out[string(viol.Path.String())] = true
		}
		return out
	}

	checked := 0
	for n := int32(-2); n <= 7; n++ {
		m := dynamicpb.NewMessage(md)
		m.Set(f("bracket"), protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)))
		m.Set(f("builtin"), protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)))
		r := refused(m)
		if r["bracket"] != r["builtin"] {
			t.Errorf("enum %d: bracket refused=%v, defined() refused=%v", n, r["bracket"], r["builtin"])
		}
		checked++
	}
	lists := [][]int32{{}, {1}, {1, 2}, {1, 1}, {0, 0}, {1, 2, 1}, {5, 9, 9}, {9, 5}}
	strs := [][]string{{}, {"a"}, {"a", "b"}, {"a", "a"}, {"", ""}, {"a", "b", "a"}}
	for _, l := range lists {
		m := dynamicpb.NewMessage(md)
		for _, name := range []string{"bracket_list", "builtin_list"} {
			list := m.Mutable(f(name)).List()
			for _, n := range l {
				list.Append(protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)))
			}
		}
		r := refused(m)
		if r["bracket_list"] != r["builtin_list"] {
			t.Errorf("enum list %v: bracket refused=%v, unique() refused=%v", l, r["bracket_list"], r["builtin_list"])
		}
		checked++
	}
	for _, l := range strs {
		m := dynamicpb.NewMessage(md)
		for _, name := range []string{"bracket_strs", "builtin_strs"} {
			list := m.Mutable(f(name)).List()
			for _, s := range l {
				list.Append(protoreflect.ValueOfString(s))
			}
		}
		r := refused(m)
		if r["bracket_strs"] != r["builtin_strs"] {
			t.Errorf("string list %q: bracket refused=%v, unique() refused=%v", l, r["bracket_strs"], r["builtin_strs"])
		}
		checked++
	}
	// The comparison is only evidence if both sides refuse something.
	m := dynamicpb.NewMessage(md)
	m.Set(f("bracket"), protoreflect.ValueOfEnum(3))
	m.Set(f("builtin"), protoreflect.ValueOfEnum(3))
	if r := refused(m); !r["bracket"] || !r["builtin"] {
		t.Fatalf("enum 3 must be refused by both: %v", r)
	}
	t.Logf("compared %d inputs", checked)
}
