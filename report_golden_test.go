package protocheck_test

// Fixture-07 golden report equality (#34, RFC-001 goal 5): validate the
// §5.3 worked-example instance against the executable fixture schema and
// require the emitted wire Report to be semantically equal to the pinned
// golden — violation order per §6.4; engine and wall_time_nanos excluded
// from equality (per the golden's own header).

import (
	"os"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestReportGoldenEquality(t *testing.T) {
	// Compile the executable worked-example schema through the real
	// compiler, exactly as an engine consuming lowered descriptors would.
	read := func(path string) string {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	root := "testdata/schema-extensions/07_report_golden/"
	files := map[string]string{
		"myco/users/user.proto":        read(root + "myco/users/user.proto"),
		"myco/commons/types.proto":     read(root + "myco/commons/types.proto"),
		"myco/commons/validator.proto": read(root + "myco/commons/validator.proto"),
	}
	fds := compileProtos(t, files, "myco/users/user.proto", "myco/commons/types.proto", "myco/commons/validator.proto")
	userFile := fds["myco/users/user.proto"]

	// Conformance stubs (pinned in the fixture README): both declared
	// functions register as always-pass — valid_phone is never invoked
	// (phone is absent) and same_domain passes on the golden instance.
	pass := func(args []any) (bool, *protocheck.Violation) { return true, nil }
	v, err := protocheck.New(
		protocheck.WithFunction("myco.commons.valid_phone", protocheck.Function(pass)),
		protocheck.WithFunction("myco.users.same_domain", protocheck.Function(pass)),
		protocheck.WithDescriptors(fds["myco/commons/types.proto"], fds["myco/commons/validator.proto"], userFile),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	// The invalid instance the golden was computed from.
	userMd := userFile.Messages().ByName(protoreflect.Name("User"))
	instance := dynamicpb.NewMessage(userMd)
	if err := prototext.Unmarshal([]byte(read(root+"instance.textproto")), instance); err != nil {
		t.Fatalf("instance: %v", err)
	}

	report, err := v.Engine().Validate(instance)
	if err != nil {
		t.Fatal(err)
	}
	got := report.Proto()

	want := &schemav1.Report{}
	if err := prototext.Unmarshal([]byte(read("testdata/schema-extensions/07_report_golden.textproto")), want); err != nil {
		t.Fatalf("golden: %v", err)
	}

	// Per the golden header: engine and wall_time_nanos are excluded from
	// cross-port equality.
	for _, r := range []*schemav1.Report{got, want} {
		r.Engine = nil
		r.WallTimeNanos = 0
	}

	if !proto.Equal(got, want) {
		t.Errorf("report differs from golden.\n--- got ---\n%s\n--- want ---\n%s",
			prototext.Format(got), prototext.Format(want))
	}
}
