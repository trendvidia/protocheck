package protocheck_test

// @sensitive redaction (#33, RFC-001 §6.7): violations on sensitive fields
// withhold ActualValue and set ValueRedacted — via field-level annotation,
// alias expansion, or message classification inherited transitively
// through message-typed fields.

import (
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const sensitiveSrc = `syntax = "proto3";
package sens;

import "protowire/schema/v1/annotations.proto";

function matches(value: string, pattern: string);

type Token = string
  @sensitive
  @validate(matches(this, "^tk_"), code = "token.format");

message Account {
  string password = 1
    @sensitive
    @validate(this.size() > 7, code = "password.short");

  Token api_token = 2;

  string nickname = 3
    @validate(this.size() > 2, code = "nickname.short");
}

@sensitive
message Vault {
  string secret = 1 @validate(this.size() > 7, code = "secret.short");
  Inner inner = 2;
}

message Inner {
  string pin = 1 @validate(this.size() > 3, code = "pin.short");
}
`

func sensValidator(t *testing.T, fd protoreflect.FileDescriptor) *protocheck.Validator {
	t.Helper()
	v, err := protocheck.New(
		protocheck.WithFunction("sens.matches", protocheck.Function(fxMatches)),
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func requireRedacted(t *testing.T, viol *protocheck.EnrichedViolation) {
	t.Helper()
	if viol.ActualValue != nil {
		t.Errorf("%s: ActualValue = %v, want withheld", viol.Path, viol.ActualValue)
	}
	if !viol.ValueRedacted {
		t.Errorf("%s: ValueRedacted = false, want true", viol.Path)
	}
}

func TestSensitiveFieldRedactsValue(t *testing.T) {
	fd := compileProtos(t, map[string]string{"s.proto": sensitiveSrc}, "s.proto")["s.proto"]
	v := sensValidator(t, fd)

	got := carrierViolations(t, v, newDynamic(t, fd, "Account", map[string]string{
		"password": "hunter2", "api_token": "tk_ok1234", "nickname": "x",
	}))
	byPath := map[string]*protocheck.EnrichedViolation{}
	for _, viol := range got {
		byPath[viol.Path.String()] = viol
	}
	if len(got) != 2 {
		t.Fatalf("expected password + nickname violations, got %v", got)
	}

	// Field-level @sensitive: value withheld.
	requireRedacted(t, byPath["password"])
	// The violation itself is otherwise intact.
	if byPath["password"].Cause.Code != "password.short" {
		t.Errorf("code = %q", byPath["password"].Cause.Code)
	}

	// Non-sensitive sibling keeps its captured value.
	nick := byPath["nickname"]
	if nick.ActualValue != "x" || nick.ValueRedacted {
		t.Errorf("nickname: ActualValue=%v ValueRedacted=%v, want captured value", nick.ActualValue, nick.ValueRedacted)
	}
}

func TestSensitiveViaAliasExpansion(t *testing.T) {
	fd := compileProtos(t, map[string]string{"s.proto": sensitiveSrc}, "s.proto")["s.proto"]
	v := sensValidator(t, fd)

	// Token alias carries @sensitive; its expansion marks api_token.
	got := carrierViolations(t, v, newDynamic(t, fd, "Account", map[string]string{
		"password": "long enough", "api_token": "wrong", "nickname": "abc",
	}))
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	if got[0].Cause.Code != "token.format" {
		t.Errorf("code = %q, want token.format", got[0].Cause.Code)
	}
	requireRedacted(t, got[0])
}

func TestSensitiveMessageTransitive(t *testing.T) {
	fd := compileProtos(t, map[string]string{"s.proto": sensitiveSrc}, "s.proto")["s.proto"]
	v := sensValidator(t, fd)

	md := fd.Messages().ByName("Vault")
	vault := dynamicpb.NewMessage(md)
	vault.Set(md.Fields().ByName("secret"), protoreflect.ValueOfString("short"))
	inner := dynamicpb.NewMessage(fd.Messages().ByName("Inner"))
	inner.Set(fd.Messages().ByName("Inner").Fields().ByName("pin"), protoreflect.ValueOfString("12"))
	vault.Set(md.Fields().ByName("inner"), protoreflect.ValueOfMessage(inner))

	got := carrierViolations(t, v, vault)
	if len(got) != 2 {
		t.Fatalf("expected secret + inner.pin violations, got %v", got)
	}
	for _, viol := range got {
		// Message-level @sensitive covers secret directly and Inner.pin
		// transitively through the message-typed field.
		requireRedacted(t, viol)
	}

	// Inner standalone is NOT sensitive-classified per source — but the
	// type-level closure marks it once reachable from Vault; document the
	// over-approximation by asserting today's behavior explicitly.
	standalone := dynamicpb.NewMessage(fd.Messages().ByName("Inner"))
	standalone.Set(fd.Messages().ByName("Inner").Fields().ByName("pin"), protoreflect.ValueOfString("12"))
	got = carrierViolations(t, v, standalone)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	requireRedacted(t, got[0])
}

func TestSensitiveDoesNotMarkAbsence(t *testing.T) {
	fd := compileProtos(t, map[string]string{"s.proto": `syntax = "proto3";
package sens2;

import "protowire/schema/v1/annotations.proto";

message Login {
  string password = 1 @sensitive @required;
}
`}, "s.proto")["s.proto"]
	v, err := protocheck.New(protocheck.WithDescriptors(fd), protocheck.WithStrictValidation())
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	// Absence is the violation: no value was captured, so nothing was
	// redacted — ValueRedacted stays false (three-state presence).
	got := carrierViolations(t, v, newDynamic(t, fd, "Login", nil))
	if len(got) != 1 || got[0].Cause.Code != "protowire.required" {
		t.Fatalf("expected required violation, got %v", got)
	}
	if got[0].ValueRedacted {
		t.Error("required violation must not set ValueRedacted — absence, not redaction")
	}
	if got[0].ActualValue != nil {
		t.Errorf("ActualValue = %v, want nil", got[0].ActualValue)
	}
}
