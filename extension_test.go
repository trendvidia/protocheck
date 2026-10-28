package protocheck

import (
	"testing"

	pb "github.com/trendvidia/protocheck/v2/protocheckpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Regression test.
//
// When descriptors come from protocompile (or any other path that decodes
// options without resolving extensions to their generated Go types), the
// extension value on FieldOptions is stored as *dynamicpb.Message. The
// previous proto.GetExtension-based path panicked inside
// ExtensionInfo.InterfaceOf trying to assert to *protocheckpb.FieldConstraints.
// readExtension must transparently re-decode dynamic values into the concrete
// type.
func TestReadExtension_DynamicValue(t *testing.T) {
	// Build concrete options the normal way, then re-encode and decode them
	// using a resolver that hands out dynamic extension types — exactly the
	// shape protocompile produces.
	concrete := &descriptorpb.FieldOptions{}
	proto.SetExtension(concrete, pb.E_Field, &pb.FieldConstraints{Required: true})

	raw, err := proto.Marshal(concrete)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	opts := &descriptorpb.FieldOptions{}
	if err := (proto.UnmarshalOptions{Resolver: dynamicpb.NewTypes(protoregistry.GlobalFiles)}).Unmarshal(raw, opts); err != nil {
		t.Fatalf("unmarshal with dynamic resolver: %v", err)
	}

	// Sanity: confirm the stored value really is dynamic. If a future protobuf
	// release changes this, the test premise (and the bug) no longer apply.
	stored := opts.ProtoReflect().Get(pb.E_Field.TypeDescriptor()).Message().Interface()
	if _, ok := stored.(*dynamicpb.Message); !ok {
		t.Fatalf("test setup did not produce a dynamic extension value: got %T", stored)
	}

	got := readExtension(opts, pb.E_Field, func() *pb.FieldConstraints { return &pb.FieldConstraints{} })
	if got == nil {
		t.Fatal("readExtension returned nil for a set dynamic extension")
	}
	if !got.GetRequired() {
		t.Errorf("required = false, want true (round-trip from dynamic message failed): %+v", got)
	}
}

func TestReadExtension_ConcreteValue(t *testing.T) {
	opts := &descriptorpb.FieldOptions{}
	proto.SetExtension(opts, pb.E_Field, &pb.FieldConstraints{Required: true})

	got := readExtension(opts, pb.E_Field, func() *pb.FieldConstraints { return &pb.FieldConstraints{} })
	if got == nil {
		t.Fatal("readExtension returned nil for a set concrete extension")
	}
	if !got.GetRequired() {
		t.Errorf("required = false, want true: %+v", got)
	}
}

func TestReadExtension_Unset(t *testing.T) {
	if got := readExtension(&descriptorpb.FieldOptions{}, pb.E_Field, func() *pb.FieldConstraints { return &pb.FieldConstraints{} }); got != nil {
		t.Errorf("readExtension on empty options = %+v, want nil", got)
	}
	if got := readExtension(nil, pb.E_Field, func() *pb.FieldConstraints { return &pb.FieldConstraints{} }); got != nil {
		t.Errorf("readExtension on nil options = %+v, want nil", got)
	}
}
