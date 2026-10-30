package protocheck_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocompile/fdp"
	"github.com/trendvidia/protocompile/incremental"
	"github.com/trendvidia/protocompile/incremental/queries"
	"github.com/trendvidia/protocompile/ir"
	"github.com/trendvidia/protocompile/report"
	"github.com/trendvidia/protocompile/source"
	"github.com/trendvidia/protos"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// compileRFC compiles one RFC-001-extended .proto source through the real
// protowire compiler (protocompile) and returns its FileDescriptor, carriers
// 50400/50401 included — the same lowering the conformance fixtures in
// protowire's testdata/schema-extensions/ exercise.
func compileRFC(t *testing.T, src string) protoreflect.FileDescriptor {
	t.Helper()
	annotations, err := fs.ReadFile(protos.Files, "protowire/schema/v1/annotations.proto")
	if err != nil {
		t.Fatal(err)
	}
	opener := source.NewMap(map[string]*source.File{
		"x.proto": source.NewFile("x.proto", src),
		"protowire/schema/v1/annotations.proto": source.NewFile(
			"protowire/schema/v1/annotations.proto", string(annotations)),
	})
	allOpeners := &source.Openers{opener, source.WKTs()}

	exec := incremental.New()
	sess := new(ir.Session)
	results, rep, err := incremental.Run(t.Context(), exec, queries.IR{
		Opener:  allOpeners,
		Session: sess,
		Path:    "x.proto",
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if rep != nil {
		for _, d := range rep.Diagnostics {
			if d.Level() <= report.Error {
				t.Fatalf("compile diagnostic: %s", d.Message())
			}
		}
	}
	if len(results) != 1 || results[0].Value == nil {
		t.Fatalf("compile: expected 1 non-nil result, got %d", len(results))
	}

	fdproto, err := fdp.DescriptorProto(results[0].Value)
	if err != nil {
		t.Fatalf("fdp.DescriptorProto: %v", err)
	}
	// x.proto may import the annotation library; resolve it through the
	// same registry-building path the fixture harness uses.
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fdproto}}
	for _, dep := range fdproto.GetDependency() {
		depResults, _, err := incremental.Run(t.Context(), exec, queries.IR{
			Opener:  allOpeners,
			Session: sess,
			Path:    dep,
		})
		if err != nil || len(depResults) != 1 || depResults[0].Value == nil {
			t.Fatalf("compiling dependency %s: %v", dep, err)
		}
		depProto, err := fdp.DescriptorProto(depResults[0].Value)
		if err != nil {
			t.Fatalf("fdp.DescriptorProto(%s): %v", dep, err)
		}
		set.File = append(set.File, depProto)
	}
	reg, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatalf("protodesc.NewFiles: %v", err)
	}
	fd, err := reg.FindFileByPath("x.proto")
	if err != nil {
		t.Fatalf("FindFileByPath: %v", err)
	}
	return fd
}

// expressionsSrc mirrors conformance fixture 12_expression_args.proto:
// one declared function called from a lowered @validate expression, plus
// engine builtins (matches) that must NOT be required by verification.
const expressionsSrc = `syntax = "proto3";
package fixtures.expressions;

function in_region(value: string, regions: string);

annotation validate(rule: expression, code: string = "");

message Account {
  string country = 1
    @validate(in_region(this, ["US", "CA"]), code = "account.bad_region");
  string display_name = 2
    @validate(matches(this, "^[a-z),]+$"), code = "account.bad_name");
}
`

const inRegionFQN = "fixtures.expressions.in_region"

func inRegionImpl(args []any) (bool, *protocheck.Violation) {
	return true, nil
}

func TestVerifyStrictMissingImplFailsNew(t *testing.T) {
	fd := compileRFC(t, expressionsSrc)

	_, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err == nil {
		t.Fatal("expected New to fail on missing implementation, got nil")
	}
	if !strings.Contains(err.Error(), inRegionFQN) {
		t.Errorf("error should name the missing FQN %q, got: %v", inRegionFQN, err)
	}
	if !strings.Contains(err.Error(), "x.proto:") {
		t.Errorf("error should carry the call-site source location, got: %v", err)
	}
}

func TestVerifyStrictRegisteredImplPasses(t *testing.T) {
	fd := compileRFC(t, expressionsSrc)

	// Only the declared function needs an impl: matches(...) is an engine
	// builtin, absent from Expression.calls, and must not be required.
	v, err := protocheck.New(
		protocheck.WithFunction(inRegionFQN, protocheck.Function(inRegionImpl)),
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.Close()

	// The FQN is genuinely registered: re-registering it is a duplicate.
	if err := v.Engine().Register(inRegionFQN, inRegionImpl); err == nil {
		t.Error("expected duplicate-registration error, got nil")
	}
}

func TestVerifyStrictDeclarationWithoutCallSites(t *testing.T) {
	// A `function` declaration is part of the file's contract even when
	// nothing calls it yet (carrier 50401).
	fd := compileRFC(t, `syntax = "proto3";
package fixtures.decls;

function is_e164(value: string);

message M {
  string phone = 1;
}
`)

	_, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err == nil {
		t.Fatal("expected New to fail on unimplemented declaration, got nil")
	}
	if !strings.Contains(err.Error(), "fixtures.decls.is_e164") {
		t.Errorf("error should name the declared FQN, got: %v", err)
	}
	// Declaration-site locations are populated since protocompile v0.17.1
	// (protocompile#99).
	if !strings.Contains(err.Error(), "x.proto:") {
		t.Errorf("error should carry the declaration source location, got: %v", err)
	}
}

func TestVerifyLenientInstallsPlaceholder(t *testing.T) {
	fd := compileRFC(t, expressionsSrc)

	// Lenient is the default: New succeeds despite the missing impl.
	v, err := protocheck.New(protocheck.WithDescriptors(fd))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.Close()

	// The registry holds an Unimplemented placeholder, which a later
	// Register replaces without a duplicate error...
	if err := v.Engine().Register(inRegionFQN, inRegionImpl); err != nil {
		t.Fatalf("Register over placeholder: %v", err)
	}
	// ...and only once: the second registration hits a real impl.
	if err := v.Engine().Register(inRegionFQN, inRegionImpl); err == nil {
		t.Error("expected duplicate-registration error after upgrade, got nil")
	}
}

func TestUnimplementedFunction(t *testing.T) {
	fn := protocheck.UnimplementedFunction(inRegionFQN)

	ok, viol := fn([]any{"BR"})
	if ok {
		t.Error("unimplemented placeholder must fail")
	}
	if viol == nil {
		t.Fatal("unimplemented placeholder must return a Violation")
	}
	if viol.Code != protocheck.CodeFunctionUnimplemented {
		t.Errorf("Code = %q, want %q", viol.Code, protocheck.CodeFunctionUnimplemented)
	}
	if want := protocheck.MsgFunctionUnimplemented(inRegionFQN); viol.FallbackMessage != want {
		t.Errorf("FallbackMessage = %q, want %q", viol.FallbackMessage, want)
	}
}

func TestWithFunctionSPIDuplicate(t *testing.T) {
	_, err := protocheck.New(
		protocheck.WithFunction(inRegionFQN, protocheck.Function(inRegionImpl)),
		protocheck.WithFunction(inRegionFQN, protocheck.Function(inRegionImpl)),
	)
	if err == nil {
		t.Fatal("expected duplicate WithFunction registration to fail New, got nil")
	}
}

func TestVerifyDescriptorWithoutCarriers(t *testing.T) {
	// A plain proto3 file carries no 50400/50401 payloads; verification is
	// a no-op in both modes.
	fd := compileRFC(t, `syntax = "proto3";
package fixtures.plain;

message M {
  string name = 1;
}
`)

	v, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v.Close()
}
