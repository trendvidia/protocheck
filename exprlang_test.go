package protocheck

import (
	"context"
	"io/fs"
	"maps"
	"math"
	"math/big"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trendvidia/protocompile/fdp"
	"github.com/trendvidia/protocompile/incremental"
	"github.com/trendvidia/protocompile/incremental/queries"
	"github.com/trendvidia/protocompile/ir"
	"github.com/trendvidia/protocompile/report"
	"github.com/trendvidia/protocompile/source"
	"github.com/trendvidia/protos"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	// Register the well-known types the schemas import.
	_ "google.golang.org/protobuf/types/known/timestamppb"
	_ "google.golang.org/protobuf/types/known/wrapperspb"
)

// noCall is the declared-function dispatcher for expressions that call
// none.
func noCall(fqn string, _ []any) (bool, *Violation) {
	return false, &Violation{FallbackMessage: "unexpected call to " + fqn}
}

// evalExpr compiles src with no declared functions and evaluates it with
// the given `this` and run instant.
func evalExpr(t *testing.T, src string, this any, now time.Time) (bool, error) {
	t.Helper()
	e, err := compileExpr(src, nil, nil)
	if err != nil {
		t.Fatalf("compileExpr(%q): %v", src, err)
	}
	ok, _, err := e.eval(this, now, noCall)
	return ok, err
}

var (
	runInstant = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	later      = runInstant.Add(time.Hour)
	earlier    = runInstant.Add(-time.Hour)
)

// now() yields the instant the run was bound to, and orders against a
// Timestamp-bound `this` (RFC-001 §6.2 rules 2 and 5).
func TestExprNowAndTemporalOrdering(t *testing.T) {
	cases := []struct {
		src  string
		this any
		want bool
	}{
		{`this > now()`, later, true},
		{`this > now()`, earlier, false},
		{`this < now()`, earlier, true},
		{`this >= now()`, runInstant, true},
		{`this <= now()`, runInstant, true},
		{`this == now()`, runInstant, true},
		{`this != now()`, later, true},
		{`this == now()`, later, false},
		{`now() < this`, later, true},
		{`this > now() && this < now()`, later, false},
		{`this in [now()]`, runInstant, true},
		{`this > 30`, 90 * time.Minute, false}, // not comparable — see the error test
	}
	for _, tc := range cases[:len(cases)-1] {
		got, err := evalExpr(t, tc.src, tc.this, runInstant)
		if err != nil {
			t.Errorf("%s with this=%v: unexpected error %v", tc.src, tc.this, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s with this=%v = %v, want %v", tc.src, tc.this, got, tc.want)
		}
	}
}

// Durations order against durations; temporals never against numbers or
// strings — an evaluation error, not a silent false.
func TestExprTemporalKinds(t *testing.T) {
	ok, err := evalExpr(t, `this > 30`, 90*time.Minute, runInstant)
	if err == nil || !strings.Contains(err.Error(), "cannot order time.Duration against int64") {
		t.Fatalf("duration vs number: got (%v, %v), want an ordering error", ok, err)
	}
	if _, err := evalExpr(t, `this == "x"`, runInstant, runInstant); err == nil || !strings.Contains(err.Error(), "cannot compare time.Time with string") {
		t.Fatalf("timestamp vs string: want a comparison error, got %v", err)
	}
	if _, err := evalExpr(t, `this > now()`, 90*time.Minute, runInstant); err == nil || !strings.Contains(err.Error(), "cannot order time.Duration against time.Time") {
		t.Fatalf("duration vs timestamp: want an ordering error, got %v", err)
	}
	if _, err := evalExpr(t, `this.size() > 0`, runInstant, runInstant); err == nil {
		t.Fatal("size() on a timestamp: want an evaluation error")
	}
	// Duration against duration, through equality and ordering; the
	// literal side has no duration form, so both sides are bound.
	e, err := compileExpr(`this == this && !(this < this)`, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.eval(90*time.Minute, runInstant, noCall); err != nil || !ok {
		t.Fatalf("duration self-comparison: got (%v, %v)", ok, err)
	}
}

// now() is receiverless and takes no arguments; a declared function named
// now still resolves ahead of the builtin.
func TestExprNowShape(t *testing.T) {
	for _, src := range []string{`now(1)`, `now(this)`, `this.now()`} {
		if _, err := compileExpr(src, nil, nil); err == nil {
			t.Errorf("%s: want a parse error", src)
		}
	}
	e, err := compileExpr(`now()`, map[string]string{"now": "t.now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	ok, _, err := e.eval(nil, runInstant, func(fqn string, args []any) (bool, *Violation) {
		called = fqn == "t.now" && len(args) == 0
		return true, nil
	})
	if err != nil || !ok || !called {
		t.Fatalf("a declared `now` must dispatch to the registry: ok=%v called=%v err=%v", ok, called, err)
	}
}

// compileInline compiles one schema (which may import the annotation
// library and the well-known types) and returns its FileDescriptor.
func compileInline(t *testing.T, path, src string) protoreflect.FileDescriptor {
	t.Helper()
	return compileInlineWith(t, path, src, nil)
}

// compileInlineWith is compileInline with further library files, keyed by
// import path and read from the given repository-relative source paths
// (protocheck/protocheck.proto for bracket rules). The annotation library
// comes from protos.Files, the version go.mod pins.
func compileInlineWith(t *testing.T, path, src string, libs map[string]string) protoreflect.FileDescriptor {
	t.Helper()
	const annotations = "protowire/schema/v1/annotations.proto"
	libs = maps.Clone(libs)
	if libs == nil {
		libs = map[string]string{}
	}
	libs[annotations] = ""
	byPath := map[string]*source.File{path: source.NewFile(path, src)}
	var queriesIR []incremental.Query[*ir.File]
	sess := new(ir.Session)
	var openers *source.Openers
	importPaths := slices.Sorted(maps.Keys(libs))
	for _, imp := range importPaths {
		var lib []byte
		var err error
		if imp == annotations {
			lib, err = fs.ReadFile(protos.Files, imp)
		} else {
			lib, err = os.ReadFile(libs[imp])
		}
		if err != nil {
			t.Fatal(err)
		}
		byPath[imp] = source.NewFile(imp, string(lib))
	}
	openers = &source.Openers{source.NewMap(byPath), source.WKTs()}
	for _, imp := range append(importPaths, path) {
		queriesIR = append(queriesIR, queries.IR{Opener: openers, Session: sess, Path: imp})
	}
	exec := incremental.New()
	results, rep, err := incremental.Run(context.Background(), exec, queriesIR...)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rep.Diagnostics {
		if d.Level() <= report.Error {
			t.Fatalf("compile: %s", d.Message())
		}
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, dep := range []string{"google/protobuf/descriptor.proto", "google/protobuf/timestamp.proto", "google/protobuf/wrappers.proto"} {
		known, err := protoregistry.GlobalFiles.FindFileByPath(dep)
		if err != nil {
			t.Fatal(err)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(known))
	}
	for _, r := range results {
		fdproto, err := fdp.DescriptorProto(r.Value)
		if err != nil {
			t.Fatal(err)
		}
		set.File = append(set.File, fdproto)
	}
	reg, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := reg.FindFileByPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func setTimestamp(t *testing.T, msg protoreflect.Message, field string, at time.Time) {
	t.Helper()
	fd := msg.Descriptor().Fields().ByName(protoreflect.Name(field))
	if fd == nil {
		t.Fatalf("no field %s", field)
	}
	sub := msg.Mutable(fd).Message()
	sub.Set(sub.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(at.Unix()))
	sub.Set(sub.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(int32(at.Nanosecond())))
}

// One run, one instant: every now() in a Validate call sees the same
// value (RFC-001 §6.2 rule 5). Two Timestamp fields rule `this == now()`;
// an instance holding the injected instant in both passes, and shifting
// the run's instant by a second fails both — so both rules read the
// run's binding, not a clock of their own.
func TestRunInstantIsSharedAcrossRules(t *testing.T) {
	fd := compileInline(t, "t.proto", `syntax = "proto3";
package t;

import "google/protobuf/timestamp.proto";
import "protowire/schema/v1/annotations.proto";

message Window {
  google.protobuf.Timestamp opens = 1 @validate(this == now());
  google.protobuf.Timestamp closes = 2 @validate(this == now());
}
`)
	v, err := New(WithDescriptors(fd))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	msg := dynamicpb.NewMessage(fd.Messages().ByName("Window"))
	setTimestamp(t, msg, "opens", runInstant)
	setTimestamp(t, msg, "closes", runInstant)

	if got := v.validate(runInstant, &carrierRun{index: v.carrier}, nil, msg); len(got) != 0 {
		t.Fatalf("both rules must see the run's instant; got %v", got)
	}
	got := v.validate(runInstant.Add(time.Second), &carrierRun{index: v.carrier}, nil, msg)
	if len(got) != 2 {
		t.Fatalf("shifted instant must fail both rules; got %d: %v", len(got), got)
	}
	if av, ok := got[0].ActualValue.(time.Time); !ok || !av.Equal(runInstant) {
		t.Errorf("ActualValue = %v (%T), want the bound time.Time", got[0].ActualValue, got[0].ActualValue)
	}
}

// A NaN is unordered (RFC-001 §5.4, IEEE 754, #9): every ordering with a
// NaN on either side is false, == is false and != is true, and none is an
// evaluation error. The left and right cases differ because compareValues
// sees the operands in source order.
func TestExprNaNComparisons(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		src  string
		want bool
	}{
		{`this < 1.0`, false},
		{`this <= 1.0`, false},
		{`this > 1.0`, false},
		{`this >= 1.0`, false},
		{`this == 1.0`, false},
		{`this != 1.0`, true},
		{`1.0 < this`, false},
		{`1.0 <= this`, false},
		{`1.0 > this`, false},
		{`1.0 >= this`, false},
		{`1.0 == this`, false},
		{`1.0 != this`, true},
		{`this < 1`, false},  // an integer on the other side
		{`1 >= this`, false}, // and on the left
		{`this < this`, false},
		{`this <= this`, false},
		{`this == this`, false},
		{`this != this`, true},
		{`this in [1.0, 2.0]`, false},
		// The range shapes from #9 refuse NaN; a negated ordering accepts it,
		// which is what is_nan() is for.
		{`this >= 0.0 && this <= 1.0`, false},
		{`this >= 40.0 && this <= 220.0`, false},
		{`!(this < 0.0)`, true},
		{`!this.is_nan() && !(this < 0.0)`, false},
	}
	for _, tc := range cases {
		ok, err := evalExpr(t, tc.src, nan, runInstant)
		if err != nil || ok != tc.want {
			t.Errorf("NaN: %s = (%v, %v), want (%v, nil)", tc.src, ok, err, tc.want)
		}
	}

	// Infinities stay ordinary ordered values.
	inf := math.Inf(1)
	for src, want := range map[string]bool{
		`this > 1.0`:                 true,
		`this >= this`:               true,
		`this >= 0.0 && this <= 1.0`: false,
		`this.is_nan()`:              false,
	} {
		if ok, err := evalExpr(t, src, inf, runInstant); err != nil || ok != want {
			t.Errorf("+Inf: %s = (%v, %v), want (%v, nil)", src, ok, err, want)
		}
	}
}

// is_nan() in both forms: true only for a floating-point NaN, false for
// every other number, integers included, and an error on anything else.
func TestExprIsNaN(t *testing.T) {
	for _, tc := range []struct {
		this any
		want bool
	}{
		{math.NaN(), true},
		{1.5, false},
		{math.Inf(-1), false},
		{int64(0), false},
		{int(7), false},
		{uint64(7), false},
	} {
		for _, src := range []string{`this.is_nan()`, `is_nan(this)`} {
			if ok, err := evalExpr(t, src, tc.this, runInstant); err != nil || ok != tc.want {
				t.Errorf("%s with this = %v (%T): got (%v, %v), want (%v, nil)", src, tc.this, tc.this, ok, err, tc.want)
			}
		}
	}
	if _, err := evalExpr(t, `this.is_nan()`, "x", runInstant); err == nil || !strings.Contains(err.Error(), "is_nan is not defined on string") {
		t.Errorf("is_nan on a string: want an evaluation error, got %v", err)
	}
	if _, err := evalExpr(t, `this.is_nan(1)`, 1.5, runInstant); err == nil || !strings.Contains(err.Error(), "is_nan expects no arguments") {
		t.Errorf("is_nan with an argument: want an arity error, got %v", err)
	}
	if _, err := compileExpr(`is_nan()`, nil, nil); err == nil {
		t.Error("is_nan() in function form with no receiver: want a compile error")
	}
}

// The rule from #9 on a real float field, through the carrier path: NaN
// is refused with the rule's own code, as 1.5 and +Inf already were.
func TestNaNRefusedByRangeRule(t *testing.T) {
	fd := compileInline(t, "t.proto", `syntax = "proto3";
package t;

import "protowire/schema/v1/annotations.proto";

message Descriptor {
  float energy = 1
    @validate(this >= 0.0 && this <= 1.0, code = "descriptor.energy.range");
}
`)
	v, err := New(WithDescriptors(fd))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	md := fd.Messages().ByName("Descriptor")
	energy := md.Fields().ByName("energy")
	for _, tc := range []struct {
		val     float32
		refused bool
	}{
		{0.5, false},
		{1.5, true},
		{float32(math.Inf(1)), true},
		{float32(math.NaN()), true},
	} {
		msg := dynamicpb.NewMessage(md)
		msg.Set(energy, protoreflect.ValueOfFloat32(tc.val))
		got := v.validate(runInstant, &carrierRun{index: v.carrier}, nil, msg)
		if refused := len(got) > 0; refused != tc.refused {
			t.Errorf("energy = %v: refused = %v, want %v (%v)", tc.val, refused, tc.refused, got)
			continue
		}
		if tc.refused && got[0].Cause.Code != "descriptor.energy.range" {
			t.Errorf("energy = %v: code %q, want the rule's own", tc.val, got[0].Cause.Code)
		}
	}
}

// Numbers compare by exact value whatever their kind (§5.4, #10). Every
// kind used to widen to float64, which rounds integers beyond 2^53, so
// 2^53 and 2^53+1 compared equal. Each case is checked in both operand
// orders.
func TestCompareNumbersExact(t *testing.T) {
	const p53 = 1 << 53
	cases := []struct {
		a, b any
		want int // -1, 0, 1; 2 = unordered
	}{
		{int64(p53), int64(p53 + 1), -1},
		{int(p53 + 1), int64(p53), 1},
		{int64(math.MaxInt64), int64(math.MaxInt64 - 1), 1},
		{uint64(math.MaxUint64), uint64(math.MaxUint64 - 1), 1},
		{int64(-1), uint64(0), -1},
		{int64(-1), uint64(math.MaxUint64), -1},
		{int64(math.MaxInt64), uint64(1 << 63), -1},
		{int64(math.MaxInt64), uint64(math.MaxInt64), 0},
		{float64(p53), int64(p53 + 1), -1},
		{float64(p53), uint64(p53 + 1), -1},
		{float64(p53), int64(p53), 0},
		{0.5, int64(0), 1},
		{-0.5, int64(0), -1},
		{-0.5, uint64(0), -1},
		{-0.0, int64(0), 0},
		{float64(1 << 63), int64(math.MaxInt64), 1},
		{float64(-(1 << 63)), int64(math.MinInt64), 0},
		{float64(1 << 64), uint64(math.MaxUint64), 1},
		{math.Nextafter(1<<64, 0), uint64(math.MaxUint64), -1},
		{math.Inf(1), int64(math.MaxInt64), 1},
		{math.Inf(-1), int64(math.MinInt64), -1},
		{math.Inf(1), uint64(math.MaxUint64), 1},
		{math.NaN(), int64(0), 2},
		{math.NaN(), uint64(0), 2},
		{1.5, 2.5, -1},
	}
	for _, tc := range cases {
		for _, pair := range [][2]any{{tc.a, tc.b}, {tc.b, tc.a}} {
			want := tc.want
			if pair[0] != tc.a && want != 2 {
				want = -want
			}
			c, ordered := compareNumbers(pair[0], pair[1])
			got := c
			if !ordered {
				got = 2
			}
			if got != want {
				t.Errorf("compareNumbers(%v (%T), %v (%T)) = %d, want %d", pair[0], pair[0], pair[1], pair[1], got, want)
			}
		}
	}
}

// The #10 repro through the language: a bound integer against a literal
// one above 2^53, for each operator.
func TestExprLargeIntegers(t *testing.T) {
	for _, this := range []any{int64(1 << 53), int(1 << 53), uint64(1 << 53)} {
		for src, want := range map[string]bool{
			`this < 9007199254740993`:    true,
			`this == 9007199254740993`:   false,
			`this != 9007199254740993`:   true,
			`this > 9007199254740991`:    true,
			`this in [9007199254740993]`: false,
			`this == 9007199254740992`:   true,
			`this >= 9007199254740992.0`: true,
			`this < 9223372036854775807`: true,
		} {
			if ok, err := evalExpr(t, src, this, runInstant); err != nil || ok != want {
				t.Errorf("this = %v (%T): %s = (%v, %v), want (%v, nil)", this, this, src, ok, err, want)
			}
		}
	}
	if ok, err := evalExpr(t, `this > 9223372036854775807`, uint64(math.MaxUint64), runInstant); err != nil || !ok {
		t.Errorf("uint64 max > int64 max: got (%v, %v)", ok, err)
	}
}

// compareNumbers agrees with exact big.Float arithmetic on every pair of
// values near each kind's boundaries, plus random ones.
func TestCompareNumbersMatchesBig(t *testing.T) {
	vals := []any{int64(math.MinInt64), int64(math.MaxInt64), uint64(math.MaxUint64), math.Inf(1), math.Inf(-1), math.NaN()}
	for _, e := range []float64{0, 1 << 53, 1 << 63, 1 << 64} {
		vals = append(vals, e, -e, math.Nextafter(e, 0), math.Nextafter(e, math.Inf(1)), e+0.5, -e-0.5)
		for d := -3.0; d <= 3; d++ {
			if v := e + d; e < 1<<63 && v >= -(1<<63) {
				vals = append(vals, int64(e)+int64(d), -int64(e)+int64(d))
			}
			if e < 1<<64 && e+d >= 0 {
				vals = append(vals, uint64(e)+uint64(int64(d)))
			}
		}
	}
	r := rand.New(rand.NewPCG(10, 10))
	for range 200 {
		vals = append(vals, int64(r.Uint64()), r.Uint64(), math.Float64frombits(r.Uint64()))
	}
	exact := func(v any) (*big.Float, bool) {
		f := new(big.Float).SetPrec(0)
		switch n := v.(type) {
		case int64:
			return f.SetInt64(n), true
		case uint64:
			return f.SetUint64(n), true
		case float64:
			if math.IsNaN(n) {
				return nil, false
			}
			return f.SetFloat64(n), true
		}
		panic(v)
	}
	pairs := 0
	for _, a := range vals {
		for _, b := range vals {
			ea, okA := exact(a)
			eb, okB := exact(b)
			c, ordered := compareNumbers(a, b)
			if !okA || !okB {
				if ordered {
					t.Errorf("compareNumbers(%v, %v): ordered with a NaN", a, b)
				}
				continue
			}
			if want := ea.Cmp(eb); !ordered || c != want {
				t.Errorf("compareNumbers(%v (%T), %v (%T)) = (%d, %v), want (%d, true)", a, a, b, b, c, ordered, want)
			}
			pairs++
		}
	}
	if pairs < 100000 {
		t.Fatalf("compared only %d ordered pairs", pairs)
	}
	t.Logf("compared %d ordered pairs against big.Float", pairs)
}
