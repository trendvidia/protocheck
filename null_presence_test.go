package protocheck_test

import (
	"strings"
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
  optional string nick = 7 @validate(this.size() > 3, code = "nick.short");
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
func decodeNulls(t *testing.T, doc string, extraNulls ...string) (proto.Message, protoreflect.MessageDescriptor) {
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
	// The decoder refuses null on a required field (RFC-001 §6.1), so a
	// test of that case sets the mask itself, as another producer might.
	if len(extraNulls) > 0 {
		maskFd := md.Fields().ByName("_null")
		mask := msg.Mutable(maskFd).Message()
		paths := mask.Mutable(mask.Descriptor().Fields().ByName("paths")).List()
		for _, p := range extraNulls {
			paths.Append(protoreflect.ValueOfString(p))
		}
	}
	return msg, md
}

// Null is "no value" (RFC-001 §6.1). A field that can hold it runs no rule and
// takes no @default. A required field holds a value, so null fails @required
// as absence does, and a @required oneof needs a member holding a value.
func TestNullPresence(t *testing.T) {
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	const base = `role = "admin"
name = "long enough"
left = "x"
inner { code = "c" }
`
	for _, c := range []struct {
		name, doc string
		nulls     []string // mask paths the decoder would refuse
		want      string
	}{
		{"valid", base, nil, ""},
		{"rules skip a null field", base + "nick = null\n", nil, ""},
		{"rules run on a set field", base + `nick = "ab"` + "\n", nil, "nick=nick.short"},
		{"null blocks @default", `name = "long enough"
role = null
left = "x"
inner { code = "c" }
`, nil, ""},
		{"absent field takes @default", `name = "long enough"
left = "x"
inner { code = "c" }
`, nil, "role=role.bad"},
		{"absent required field", `role = "admin"
left = "x"
inner { code = "c" }
`, nil, "name=protowire.required"},
		{"null required field", `role = "admin"
left = "x"
inner { code = "c" }
`, []string{"name"}, "name=protowire.required"},
		{"null oneof member does not count", `role = "admin"
name = "long enough"
right = null
inner { code = "c" }
`, nil, "pick=protowire.required"},
		{"absent oneof", `role = "admin"
name = "long enough"
inner { code = "c" }
`, nil, "pick=protowire.required"},
		{"absent nested required field", `role = "admin"
name = "long enough"
left = "x"
inner { }
`, nil, "inner.code=protowire.required"},
		{"null nested required field", `role = "admin"
name = "long enough"
left = "x"
inner { }
`, []string{"inner.code"}, "inner.code=protowire.required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			msg, _ := decodeNulls(t, c.doc, c.nulls...)
			if got := violationCodes(carrierViolations(t, v, msg)); got != c.want {
				t.Errorf("violations = %q, want %q", got, c.want)
			}
		})
	}
}

// The decoder refuses null on a required field, so protocheck sees one only
// from a producer that writes the mask itself.
func TestDecoderRefusesNullRequired(t *testing.T) {
	fd := compileProtos(t, map[string]string{"nulls.proto": nullSrc}, "nulls.proto")["nulls.proto"]
	msg := dynamicpb.NewMessage(fd.Messages().ByName("Doc"))
	_, err := (pxf.UnmarshalOptions{SkipPostDecode: true}).UnmarshalFull([]byte("name = null\n"), msg)
	if err == nil || !strings.Contains(err.Error(), "null is not allowed on required field") {
		t.Fatalf("err = %v, want the decoder to refuse null on a required field", err)
	}
}
