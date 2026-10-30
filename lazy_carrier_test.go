package protocheck_test

// #20: carrier rules run whether or not WithDescriptors lists the file that
// holds them. lib.proto is imported by main.proto, so these cases cover a
// rule in the validated message's own file, one in an import, and an alias
// declared in an import and expanded onto a use-site field.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const lazyLibSrc = `syntax = "proto3";
package lazy.lib;

import "protowire/schema/v1/annotations.proto";

function known(value: string);

type Tag = string @validate(this.size() > 1, code = "tag.short");

message Level {
  double energy = 1 @validate(this <= 1.0, code = "level.energy");
  string kind = 2 @validate(known(this), code = "level.kind");
}

@sensitive
message Vault {
  Secret secret = 1;
}

message Secret {
  string token = 1 @validate(this.size() > 3, code = "secret.token");
}
`

const lazyMainSrc = `syntax = "proto3";
package lazy.main;

import "protowire/schema/v1/annotations.proto";
import "lib.proto";

message Song {
  string name = 1 @required;
  lazy.lib.Level level = 2;
  repeated lazy.lib.Tag tags = 3;
  lazy.lib.Vault vault = 4;
}
`

func compileLazy(t *testing.T) (lib, main protoreflect.FileDescriptor) {
	t.Helper()
	fds := compileProtos(t, map[string]string{"lib.proto": lazyLibSrc, "main.proto": lazyMainSrc}, "lib.proto", "main.proto")
	return fds["lib.proto"], fds["main.proto"]
}

// badSong is a Song that breaks every rule: a missing name, an energy over
// 1.0 in an imported message, and a too-short alias-typed list element.
func badSong(t *testing.T, main protoreflect.FileDescriptor) proto.Message {
	t.Helper()
	md := main.Messages().ByName("Song")
	song := dynamicpb.NewMessage(md)

	level := dynamicpb.NewMessage(md.Fields().ByName("level").Message())
	level.Set(level.Descriptor().Fields().ByName("energy"), protoreflect.ValueOfFloat64(9))
	song.Set(md.Fields().ByName("level"), protoreflect.ValueOfMessage(level))

	tags := song.Mutable(md.Fields().ByName("tags")).List()
	tags.Append(protoreflect.ValueOfString("x"))
	return song
}

func violationCodes(viols []*protocheck.EnrichedViolation) string {
	codes := make([]string, len(viols))
	for i, viol := range viols {
		codes[i] = viol.Path.String() + "=" + viol.Cause.Code
	}
	return strings.Join(codes, " ")
}

const lazyWantCodes = "name=protowire.required tags[0]=tag.short level.energy=level.energy"

func TestPlainNewEnforcesCarrierRules(t *testing.T) {
	_, main := compileLazy(t)
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	got := carrierViolations(t, v, badSong(t, main))
	if s := violationCodes(got); s != lazyWantCodes {
		t.Fatalf("violations = %s, want %s", s, lazyWantCodes)
	}
	// The alias lives in the import, so finding it there is what makes the
	// rule per-element, with the alias in the type chain.
	if got[1].RuleKind != protocheck.RuleKindTypeRefinement {
		t.Errorf("tags[0] RuleKind = %v, want TYPE_REFINEMENT", got[1].RuleKind)
	}
	if want := "string lazy.lib.Tag"; strings.Join(got[1].TypeChain, " ") != want {
		t.Errorf("tags[0] TypeChain = %v, want %s", got[1].TypeChain, want)
	}
}

// A listed file's imports are enforced and verified like the file itself.
func TestWithDescriptorsFollowsImports(t *testing.T) {
	_, main := compileLazy(t)

	v, err := protocheck.New(protocheck.WithDescriptors(main))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if s := violationCodes(carrierViolations(t, v, badSong(t, main))); s != lazyWantCodes {
		t.Fatalf("violations = %s, want %s", s, lazyWantCodes)
	}

	_, err = protocheck.New(protocheck.WithDescriptors(main), protocheck.WithStrictValidation())
	if err == nil || !strings.Contains(err.Error(), "lazy.lib.known") {
		t.Fatalf("strict New should name the import's function, got: %v", err)
	}
	v, err = protocheck.New(
		protocheck.WithFunction("lazy.lib.known", protocheck.Function(inRegionImpl)),
		protocheck.WithDescriptors(main),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatalf("strict New with the import's function registered: %v", err)
	}
	v.Close()
}

// Listing only the import still enforces the unlisted file that imports it.
func TestUnlistedRootWithListedImport(t *testing.T) {
	lib, main := compileLazy(t)
	v, err := protocheck.New(protocheck.WithDescriptors(lib))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if s := violationCodes(carrierViolations(t, v, badSong(t, main))); s != lazyWantCodes {
		t.Fatalf("violations = %s, want %s", s, lazyWantCodes)
	}
}

// A function nothing registered fails closed under plain New: the rule fails
// at its call rather than being skipped.
func TestPlainNewFunctionFailsClosed(t *testing.T) {
	lib, _ := compileLazy(t)
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	got := carrierViolations(t, v, newDynamic(t, lib, "Level", map[string]string{"kind": "anything"}))
	if s := violationCodes(got); s != "kind=level.kind" {
		t.Fatalf("violations = %s, want kind=level.kind", s)
	}
}

// Sensitivity is inherited through message-typed fields, and across a file
// boundary here: Secret is sensitive only as reached from Vault.
func TestPlainNewPropagatesSensitivity(t *testing.T) {
	lib, _ := compileLazy(t)
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	vault := lib.Messages().ByName("Vault")
	secret := newDynamic(t, lib, "Secret", map[string]string{"token": "abc"})
	msg := dynamicpb.NewMessage(vault)
	msg.Set(vault.Fields().ByName("secret"), protoreflect.ValueOfMessage(secret.ProtoReflect()))

	got := carrierViolations(t, v, msg)
	if len(got) != 1 || got[0].Cause.Code != "secret.token" {
		t.Fatalf("violations = %s, want one secret.token", violationCodes(got))
	}
	if !got[0].ValueRedacted || got[0].ActualValue != nil {
		t.Errorf("sensitive value not redacted: redacted=%v actual=%v", got[0].ValueRedacted, got[0].ActualValue)
	}
}

// A rule that cannot be loaded refuses the message instead of skipping it,
// and refuses it again on the cached path.
func TestUnloadableRuleIsAnError(t *testing.T) {
	opts := &descriptorpb.FieldOptions{}
	proto.SetExtension(opts, schemav1.E_FieldAnnotations, &schemav1.AnnotationList{
		Entries: []*schemav1.Annotation{{Name: "protowire.schema.v1.validate"}},
	})
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("broken.proto"),
		Package: proto.String("lazy.broken"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Broken"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:    proto.String("name"),
				Number:  proto.Int32(1),
				Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Options: opts,
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := protocheck.New(protocheck.WithDescriptors(fd)); err == nil {
		t.Error("New(WithDescriptors) accepted an unloadable rule")
	}

	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	msg := dynamicpb.NewMessage(fd.Messages().ByName("Broken"))
	for range 2 {
		err := v.Validate(msg)
		var ve *protocheck.ValidationError
		if err == nil || errors.As(err, &ve) || !strings.Contains(err.Error(), "broken.proto") {
			t.Fatalf("Validate = %v, want a load error naming broken.proto", err)
		}
	}
	if _, err := v.Engine().Validate(msg); err == nil {
		t.Error("Engine().Validate accepted an unloadable rule")
	}
}

// The first Validate of a file builds its index; run under -race, this is
// what proves concurrent first calls are safe.
func TestConcurrentFirstValidate(t *testing.T) {
	_, main := compileLazy(t)
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if s := violationCodes(carrierViolations(t, v, badSong(t, main))); s != lazyWantCodes {
				t.Errorf("violations = %s, want %s", s, lazyWantCodes)
			}
		})
	}
	wg.Wait()
}

// A nil message is refused, not passed as valid: the walk's skip for an unset
// field must not reach the root.
func TestNilMessageIsAnError(t *testing.T) {
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	for name, msg := range map[string]proto.Message{
		"nil":       nil,
		"typed nil": (*testpb.User)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			var ve *protocheck.ValidationError
			if err := v.Validate(msg); err == nil || errors.As(err, &ve) {
				t.Errorf("Validate = %v, want a non-ValidationError error", err)
			}
			if _, err := v.Engine().Validate(msg); err == nil {
				t.Error("Engine().Validate accepted a nil message")
			}
		})
	}
}
