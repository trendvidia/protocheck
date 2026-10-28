package protocheck

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trendvidia/protocompile/fdp"
	"github.com/trendvidia/protocompile/incremental"
	"github.com/trendvidia/protocompile/incremental/queries"
	"github.com/trendvidia/protocompile/ir"
	"github.com/trendvidia/protocompile/report"
	"github.com/trendvidia/protocompile/source"
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
	e, err := compileExpr(src, nil)
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
	e, err := compileExpr(`this == this && !(this < this)`, nil)
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
		if _, err := compileExpr(src, nil); err == nil {
			t.Errorf("%s: want a parse error", src)
		}
	}
	e, err := compileExpr(`now()`, map[string]string{"now": "t.now"})
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
	const annotations = "protowire/schema/v1/annotations.proto"
	lib, err := os.ReadFile("testdata/" + annotations)
	if err != nil {
		t.Fatal(err)
	}
	files := source.NewMap(map[string]*source.File{
		annotations: source.NewFile(annotations, string(lib)),
		path:        source.NewFile(path, src),
	})
	openers := &source.Openers{files, source.WKTs()}
	exec := incremental.New()
	sess := new(ir.Session)
	results, rep, err := incremental.Run(context.Background(), exec,
		queries.IR{Opener: openers, Session: sess, Path: annotations},
		queries.IR{Opener: openers, Session: sess, Path: path},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rep.Diagnostics {
		if d.Level() <= report.Error {
			t.Fatalf("compile: %s", d.Message())
		}
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, dep := range []string{"google/protobuf/timestamp.proto", "google/protobuf/wrappers.proto"} {
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

	if got := v.validate(runInstant, nil, msg); len(got) != 0 {
		t.Fatalf("both rules must see the run's instant; got %v", got)
	}
	got := v.validate(runInstant.Add(time.Second), nil, msg)
	if len(got) != 2 {
		t.Fatalf("shifted instant must fail both rules; got %d: %v", len(got), got)
	}
	if av, ok := got[0].ActualValue.(time.Time); !ok || !av.Equal(runInstant) {
		t.Errorf("ActualValue = %v (%T), want the bound time.Time", got[0].ActualValue, got[0].ActualValue)
	}
}
