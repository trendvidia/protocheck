package protocheck_test

// Fixture-15 golden report equality (#53, RFC-001 §6.4 as amended by
// protowire#141/#153): per-element validation on repeated and map
// fields, including the map KEY dimension — key violations carry
// for_key = true with the entry's subscripted path, ordered before the
// same entry's value violation per the synthetic entry's field order.

import (
	"os"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestCollectionsGoldenEquality(t *testing.T) {
	read := func(path string) string {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	root := "testdata/schema-extensions/15_collections_golden/"
	files := map[string]string{
		"15_collections_golden/inventory.proto": read(root + "inventory.proto"),
	}
	fds := compileProtos(t, files, "15_collections_golden/inventory.proto")
	invFile := fds["15_collections_golden/inventory.proto"]

	v, err := protocheck.New(
		protocheck.WithDescriptors(invFile),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	invMd := invFile.Messages().ByName("Inventory")
	instance := dynamicpb.NewMessage(invMd)
	if err := prototext.Unmarshal([]byte(read(root+"instance.textproto")), instance); err != nil {
		t.Fatalf("instance: %v", err)
	}

	report, err := v.Engine().Validate(instance)
	if err != nil {
		t.Fatal(err)
	}
	got := report.Proto()

	want := &schemav1.Report{}
	if err := prototext.Unmarshal([]byte(read("testdata/schema-extensions/15_collections_golden.textproto")), want); err != nil {
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
