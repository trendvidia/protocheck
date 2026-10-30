package protocheck_test

import (
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protowire-go/encoding/pxf"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	_ "google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Declaring _null by hand gives these messages the mask RFC-001 §8.6 lowering
// would add, so a PXF null survives into the validated message.
const nullSrc = `syntax = "proto3";
package nulls;

import "google/protobuf/field_mask.proto";
import "protowire/schema/v1/annotations.proto";

message Inner {
  optional string code = 1 @required;
}

message Doc {
  optional string name = 1 @required @validate(this.size() > 3, code = "name.short");
  optional string role = 2 @default("viewer") @validate(this == "admin", code = "role.bad");
  Inner inner = 3;
  repeated Inner items = 4;

  @required
  oneof pick {
    string left = 5;
    string right = 6;
  }

  google.protobuf.FieldMask _null = 536870911;
}
`

// decodeNulls decodes a real PXF document, so the mask holds what the decoder
// writes rather than what a test assumes.
func decodeNulls(t *testing.T, doc string) (proto.Message, protoreflect.MessageDescriptor) {
	t.Helper()
	fd := compileProtos(t, map[string]string{"nulls.proto": nullSrc}, "nulls.proto")["nulls.proto"]
	md := fd.Messages().ByName("Doc")
	msg := dynamicpb.NewMessage(md)
	// UnmarshalFull, because plain Unmarshal records no nulls. SkipPostDecode,
	// because the decoder's own required check would otherwise refuse the
	// absent cases before protocheck saw them.
	if _, err := (pxf.UnmarshalOptions{SkipPostDecode: true}).UnmarshalFull([]byte(doc), msg); err != nil {
		t.Fatalf("pxf: %v", err)
	}
	return msg, md
}

func TestNullCountsAsPresent(t *testing.T) {
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	for _, c := range []struct{ name, doc, want string }{
		{"null field satisfies @required", `role = "admin"
name = null
left = "x"
inner { code = "c" }
`, ""},
		{"absent field does not", `role = "admin"
left = "x"
inner { code = "c" }
`, "name=protowire.required"},
		{"rules skip a null field", `role = "admin"
name = null
left = "x"
inner { code = "c" }
`, ""},
		{"null blocks @default", `name = "long enough"
role = null
left = "x"
inner { code = "c" }
`, ""},
		{"absent field takes @default", `name = "long enough"
left = "x"
inner { code = "c" }
`, "role=role.bad"},
		{"null oneof member counts", `role = "admin"
name = "long enough"
right = null
inner { code = "c" }
`, ""},
		{"absent oneof does not", `role = "admin"
name = "long enough"
inner { code = "c" }
`, "pick=protowire.required"},
		{"null nested field", `role = "admin"
name = "long enough"
left = "x"
inner { code = null }
`, ""},
		{"absent nested field", `role = "admin"
name = "long enough"
left = "x"
inner { }
`, "inner.code=protowire.required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			msg, _ := decodeNulls(t, c.doc)
			if got := violationCodes(carrierViolations(t, v, msg)); got != c.want {
				t.Errorf("violations = %q, want %q", got, c.want)
			}
		})
	}
}
