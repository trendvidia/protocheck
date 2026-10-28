package protocheck_test

// Constraints over keyed repeated fields (#13, protowire#116): elements of
// a `repeated <Message>` field carrying (pxf.key) are addressed by their
// key value — the entry name in PXF sources — uniqueness asserts over the
// key dimension, and per-element expressions see the key field in scope.

import (
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const keyedSrc = `syntax = "proto3";
package keyed;

import "pxf/annotations.proto";
import "protocheck/protocheck.proto";

message Endpoint {
  string name = 1 [(protocheck.field).string = {pattern: "^[a-z][a-z0-9-]*$"}];
  string url = 2 [(protocheck.field) = {
    required: true
    string: {min_len: 1}
  }];
}

message Gateway {
  repeated Endpoint endpoints = 1 [
    (pxf.key) = "name",
    (protocheck.field).repeated = {
      unique: true
      items: {
        check: [{
          id: "key_len"
          expression: "len(name) <= 8"
          message: "entry key too long"
        }]
      }
    }
  ];

  // Unkeyed for contrast: elements still validate (by index), and no
  // items rules are needed for the recursion to happen.
  repeated Endpoint fallbacks = 2;
}
`

// keyedEngine implements the fixture's single items.check rule —
// "len(name) <= 8" — through the ExpressionEngine seam, so these tests
// cover the keyed walk (subscripts, uniqueness, per-element dispatch)
// without an engine dependency. Real expression semantics are the engine's
// to test.
func keyedEngine() *stubEngine {
	return &stubEngine{
		evalMessage: func(msg protoreflect.Message, src string) (bool, error) {
			name := msg.Get(msg.Descriptor().Fields().ByName("name")).String()
			return len(name) <= 8, nil
		},
	}
}

func keyedValidator(t *testing.T) *protocheck.Validator {
	t.Helper()
	v, err := protocheck.New(protocheck.WithExpressionEngine(keyedEngine()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

// newDynamicList builds a dynamic message of msgName with the named
// repeated-message field populated from elems (string fields only).
func newDynamicList(t *testing.T, fd protoreflect.FileDescriptor, msgName, listField string, elems []map[string]string) proto.Message {
	t.Helper()
	md := fd.Messages().ByName(protoreflect.Name(msgName))
	if md == nil {
		t.Fatalf("message %s not found", msgName)
	}
	msg := dynamicpb.NewMessage(md)
	lfd := md.Fields().ByName(protoreflect.Name(listField))
	if lfd == nil {
		t.Fatalf("field %s not found on %s", listField, msgName)
	}
	list := msg.Mutable(lfd).List()
	for _, fields := range elems {
		el := dynamicpb.NewMessage(lfd.Message())
		for fname, val := range fields {
			f := lfd.Message().Fields().ByName(protoreflect.Name(fname))
			if f == nil {
				t.Fatalf("field %s not found on element", fname)
			}
			el.Set(f, protoreflect.ValueOfString(val))
		}
		list.Append(protoreflect.ValueOfMessage(el))
	}
	return msg
}

func TestKeyedElementPathsUseKeySubscript(t *testing.T) {
	fd := compileProtos(t, map[string]string{"keyed.proto": keyedSrc}, "keyed.proto")["keyed.proto"]
	v := keyedValidator(t)

	// A well-keyed element with an invalid url: the violation addresses
	// the element by its key, not its index.
	gw := newDynamicList(t, fd, "Gateway", "endpoints", []map[string]string{
		{"name": "api", "url": "https://example.com"},
		{"name": "admin"}, // url missing
	})
	got := carrierViolations(t, v, gw)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	if p := got[0].Path.String(); p != "endpoints[admin].url" {
		t.Errorf("path = %q, want endpoints[admin].url", p)
	}
	if got[0].Path[0].Subscript != protocheck.StringKey("admin") {
		t.Errorf("subscript = %#v, want StringKey(admin)", got[0].Path[0].Subscript)
	}
	if got[0].Cause.Code != "required" {
		t.Errorf("code = %q, want required", got[0].Cause.Code)
	}
}

func TestKeyedElementUnsetKeyFallsBackToIndex(t *testing.T) {
	fd := compileProtos(t, map[string]string{"keyed.proto": keyedSrc}, "keyed.proto")["keyed.proto"]
	v := keyedValidator(t)

	// No key set: the element is addressed by index; the empty name also
	// fails its own pattern rule.
	gw := newDynamicList(t, fd, "Gateway", "endpoints", []map[string]string{
		{"url": "https://example.com"},
	})
	got := carrierViolations(t, v, gw)
	var paths []string
	for _, viol := range got {
		paths = append(paths, viol.Path.String())
	}
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "endpoints[0].name") {
		t.Errorf("expected an index-addressed violation at endpoints[0].name, got %v", paths)
	}
}

func TestKeyedUniqueness(t *testing.T) {
	fd := compileProtos(t, map[string]string{"keyed.proto": keyedSrc}, "keyed.proto")["keyed.proto"]
	v := keyedValidator(t)

	gw := newDynamicList(t, fd, "Gateway", "endpoints", []map[string]string{
		{"name": "api", "url": "https://a.example"},
		{"name": "api", "url": "https://b.example"}, // duplicate key, distinct element
	})
	got := carrierViolations(t, v, gw)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	viol := got[0]
	if viol.Cause.Code != "repeated.unique" {
		t.Errorf("code = %q, want repeated.unique", viol.Cause.Code)
	}
	if p := viol.Path.String(); p != "endpoints[api]" {
		t.Errorf("path = %q, want endpoints[api] (the duplicate entry name)", p)
	}
	if k := viol.Cause.Params["key"]; k != "api" {
		t.Errorf("params[key] = %v, want api", k)
	}

	// Distinct keys with otherwise identical elements are fine: the keyed
	// assertion is over the key dimension only.
	gw = newDynamicList(t, fd, "Gateway", "endpoints", []map[string]string{
		{"name": "a", "url": "https://same.example"},
		{"name": "b", "url": "https://same.example"},
	})
	if got := carrierViolations(t, v, gw); len(got) != 0 {
		t.Errorf("distinct keys must pass, got %v", got)
	}
}

func TestKeyedPerElementExpressionSeesKey(t *testing.T) {
	fd := compileProtos(t, map[string]string{"keyed.proto": keyedSrc}, "keyed.proto")["keyed.proto"]
	v := keyedValidator(t)

	gw := newDynamicList(t, fd, "Gateway", "endpoints", []map[string]string{
		{"name": "waytoolongkey", "url": "https://example.com"},
	})
	got := carrierViolations(t, v, gw)
	// The items.check expression len(name) <= 8 sees the key field; the
	// key also fails nothing else (pattern allows it), so exactly one
	// violation, addressed by the key.
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	if got[0].Cause.Code != "key_len" {
		t.Errorf("code = %q, want key_len", got[0].Cause.Code)
	}
	if p := got[0].Path.String(); p != "endpoints[waytoolongkey]" {
		t.Errorf("path = %q, want endpoints[waytoolongkey]", p)
	}
	if m := got[0].Cause.FallbackMessage; m != "entry key too long" {
		t.Errorf("message = %q", m)
	}
}

func TestUnkeyedListElementsValidateWithoutItemsRules(t *testing.T) {
	fd := compileProtos(t, map[string]string{"keyed.proto": keyedSrc}, "keyed.proto")["keyed.proto"]
	v := keyedValidator(t)

	// fallbacks carries no repeated rules at all — elements still
	// validate against Endpoint's own constraints, addressed by index.
	gw := newDynamicList(t, fd, "Gateway", "fallbacks", []map[string]string{
		{"name": "ok", "url": "https://example.com"},
		{"name": "ok2"}, // url missing
	})
	got := carrierViolations(t, v, gw)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	if p := got[0].Path.String(); p != "fallbacks[1].url" {
		t.Errorf("path = %q, want fallbacks[1].url", p)
	}
}

func TestKeyedFailFast(t *testing.T) {
	fd := compileProtos(t, map[string]string{"keyed.proto": keyedSrc}, "keyed.proto")["keyed.proto"]
	v, err := protocheck.New(protocheck.WithExpressionEngine(keyedEngine()), protocheck.WithFailFast())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })

	gw := newDynamicList(t, fd, "Gateway", "endpoints", []map[string]string{
		{"name": "api"},
		{"name": "api"},
	})
	got := carrierViolations(t, v, gw)
	if len(got) != 1 {
		t.Fatalf("fail-fast: expected exactly 1 violation, got %v", got)
	}
}
