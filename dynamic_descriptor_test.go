package protocheck_test

import (
	"errors"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// rebuildDynamic rewires the given file descriptor so that its option
// extensions are stored as *dynamicpb.Message — the shape protocompile
// produces when compiling .proto files without the generated Go types for
// the extensions registered concretely. The round-trip through Marshal +
// Unmarshal with dynamicpb.NewTypes as the resolver is what flips
// concrete-typed extension values to dynamic ones; protodesc.NewFile then
// preserves them on the resulting descriptors.
func rebuildDynamic(t *testing.T, src protoreflect.FileDescriptor) protoreflect.FileDescriptor {
	t.Helper()
	raw, err := proto.Marshal(protodesc.ToFileDescriptorProto(src))
	if err != nil {
		t.Fatalf("marshal FileDescriptorProto: %v", err)
	}
	fdp := &descriptorpb.FileDescriptorProto{}
	if err := (proto.UnmarshalOptions{Resolver: dynamicpb.NewTypes(protoregistry.GlobalFiles)}).Unmarshal(raw, fdp); err != nil {
		t.Fatalf("unmarshal with dynamic resolver: %v", err)
	}
	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	return file
}

// dynUser returns a *dynamicpb.Message for testpb.User backed by descriptors
// whose option extensions are dynamic, mirroring what an LSP/build tool sees
// when it compiles workspace .protos with protocompile.
func dynUser(t *testing.T) (protoreflect.Message, protoreflect.MessageDescriptor) {
	t.Helper()
	file := rebuildDynamic(t, testpb.File_test_proto)
	md := file.Messages().ByName("User")
	if md == nil {
		t.Fatal("User descriptor missing from rebuilt file")
	}

	// Confirm the rebuilt FieldOptions really do carry a dynamic extension
	// value. If protobuf changes how unmarshalled extensions are stored, this
	// test's premise — and the regression we're guarding — no longer apply.
	xt, err := protoregistry.GlobalTypes.FindExtensionByName("protocheck.field")
	if err != nil {
		t.Fatalf("find protocheck.field: %v", err)
	}
	stored := md.Fields().ByName("email").Options().ProtoReflect().Get(xt.TypeDescriptor()).Message().Interface()
	if _, ok := stored.(*dynamicpb.Message); !ok {
		t.Fatalf("test setup did not produce a dynamic extension value: got %T", stored)
	}
	return dynamicpb.NewMessage(md), md
}

// Regression for https://github.com/trendvidia/protocheck/issues/2: Validate
// must not panic when descriptors carry *dynamicpb.Message extension values.
// Before the fix, this scenario panicked inside ExtensionInfo.InterfaceOf.
func TestValidate_DynamicDescriptors_InvalidMessage(t *testing.T) {
	msg, md := dynUser(t)
	msg.Set(md.Fields().ByName("name"), protoreflect.ValueOfString("Alice"))
	msg.Set(md.Fields().ByName("email"), protoreflect.ValueOfString("not-an-email"))

	v, err := protocheck.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = v.Validate(msg.Interface())
	if err == nil {
		t.Fatal("expected validation error for malformed email, got nil")
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !hasConstraintID(ve, "string.email") {
		t.Errorf("expected string.email violation, got: %v", ve.Violations)
	}
}

func TestValidate_DynamicDescriptors_ValidMessage(t *testing.T) {
	msg, md := dynUser(t)
	msg.Set(md.Fields().ByName("name"), protoreflect.ValueOfString("Alice"))
	msg.Set(md.Fields().ByName("email"), protoreflect.ValueOfString("alice@example.com"))

	v, err := protocheck.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Validate(msg.Interface()); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_DynamicDescriptors_RequiredMissing(t *testing.T) {
	msg, _ := dynUser(t)

	v, err := protocheck.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = v.Validate(msg.Interface())
	if err == nil {
		t.Fatal("expected required-field violations, got nil")
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !hasConstraintID(ve, "required") {
		t.Errorf("expected required violation, got: %v", ve.Violations)
	}
}

func hasConstraintID(ve *protocheck.ValidationError, id string) bool {
	for _, v := range ve.Violations {
		if v.Cause.Code == id {
			return true
		}
	}
	return false
}
