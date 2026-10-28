package protocheck_test

// Per-element carrier rules on collections (#31, RFC-001 §6.4):
// alias-expanded rules apply per element with subscripted paths;
// directly-written @validate applies to the collection as a whole.

import (
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const collectionSrc = `syntax = "proto3";
package coll;

import "protowire/schema/v1/annotations.proto";

function matches(value: string, pattern: string);

type Email = string @validate(matches(this, "@"), code = "email.invalid");

message Book {
  repeated Email emails = 1;

  repeated Email checked = 2
    @validate(this.size() > 0, code = "coll.empty");

  map<string, string> tags = 3
    @validate(this.size() > 0, code = "tags.empty");

  map<string, Email> contacts = 4;
}
`

func collBook(t *testing.T, fd protoreflect.FileDescriptor, emails, checked []string, tags map[string]string) proto.Message {
	t.Helper()
	md := fd.Messages().ByName("Book")
	msg := dynamicpb.NewMessage(md)
	fill := func(field string, vals []string) {
		if vals == nil {
			return
		}
		f := md.Fields().ByName(protoreflect.Name(field))
		list := msg.Mutable(f).List()
		for _, val := range vals {
			list.Append(protoreflect.ValueOfString(val))
		}
	}
	fill("emails", emails)
	fill("checked", checked)
	if tags != nil {
		f := md.Fields().ByName("tags")
		m := msg.Mutable(f).Map()
		for k, val := range tags {
			m.Set(protoreflect.MapKey(protoreflect.ValueOfString(k)), protoreflect.ValueOfString(val))
		}
	}
	return msg
}

func collValidator(t *testing.T, fd protoreflect.FileDescriptor) *protocheck.Validator {
	t.Helper()
	v, err := protocheck.New(
		protocheck.WithFunction("coll.matches", protocheck.Function(fxContainsAt)),
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func TestCollectionAliasRulesPerElement(t *testing.T) {
	fd := compileProtos(t, map[string]string{"coll.proto": collectionSrc}, "coll.proto")["coll.proto"]
	v := collValidator(t, fd)

	// tags/checked populated so only emails violates.
	msg := collBook(t, fd, []string{"good@x", "bad", "also@ok"}, []string{"a@b"}, map[string]string{"k": "v"})
	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	viol := got[0]
	if p := viol.Path.String(); p != "emails[1]" {
		t.Errorf("path = %q, want emails[1]", p)
	}
	if viol.Cause.Code != "email.invalid" {
		t.Errorf("code = %q, want email.invalid", viol.Cause.Code)
	}
	if viol.RuleKind != protocheck.RuleKindTypeRefinement {
		t.Errorf("RuleKind = %v, want TYPE_REFINEMENT", viol.RuleKind)
	}
	wantChain := []string{"string", "coll.Email"}
	if len(viol.TypeChain) != 2 || viol.TypeChain[0] != wantChain[0] || viol.TypeChain[1] != wantChain[1] {
		t.Errorf("TypeChain = %v, want %v", viol.TypeChain, wantChain)
	}
	if viol.ActualValue != "bad" {
		t.Errorf("ActualValue = %v, want the element value", viol.ActualValue)
	}
}

func TestCollectionLevelRuleOnEmptyList(t *testing.T) {
	fd := compileProtos(t, map[string]string{"coll.proto": collectionSrc}, "coll.proto")["coll.proto"]
	v := collValidator(t, fd)

	// checked is empty: its collection-level size rule fires even though
	// the field is unpopulated; the per-element alias rule does not.
	msg := collBook(t, fd, nil, nil, map[string]string{"k": "v"})
	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	viol := got[0]
	if p := viol.Path.String(); p != "checked" {
		t.Errorf("path = %q, want checked (no subscript for collection rules)", p)
	}
	if viol.Cause.Code != "coll.empty" {
		t.Errorf("code = %q, want coll.empty", viol.Cause.Code)
	}
	if viol.RuleKind != protocheck.RuleKindValidate {
		t.Errorf("RuleKind = %v, want VALIDATE", viol.RuleKind)
	}
	if viol.ActualValue != nil {
		t.Errorf("collection rule must not capture a value, got %v", viol.ActualValue)
	}
}

func TestCollectionMixedElementAndCollectionRules(t *testing.T) {
	fd := compileProtos(t, map[string]string{"coll.proto": collectionSrc}, "coll.proto")["coll.proto"]
	v := collValidator(t, fd)

	// checked non-empty (collection rule passes) with one bad element
	// (alias rule fails per element).
	msg := collBook(t, fd, nil, []string{"good@x", "nope"}, map[string]string{"k": "v"})
	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	if p := got[0].Path.String(); p != "checked[1]" {
		t.Errorf("path = %q, want checked[1]", p)
	}
	if got[0].Cause.Code != "email.invalid" {
		t.Errorf("code = %q, want email.invalid", got[0].Cause.Code)
	}
}

func TestMapAliasRulesPerElement(t *testing.T) {
	fd := compileProtos(t, map[string]string{"coll.proto": collectionSrc}, "coll.proto")["coll.proto"]
	v := collValidator(t, fd)

	// Map whose value type is an alias (protocompile ≥ v0.19.0 lowers the
	// alias rules onto the map field): the rule dispatches per entry value
	// with a key-subscripted path.
	msg := collBook(t, fd, nil, []string{"a@b"}, map[string]string{"k": "v"})
	md := fd.Messages().ByName("Book")
	f := md.Fields().ByName("contacts")
	m := msg.ProtoReflect().Mutable(f).Map()
	m.Set(protoreflect.MapKey(protoreflect.ValueOfString("ann")), protoreflect.ValueOfString("a@b"))
	m.Set(protoreflect.MapKey(protoreflect.ValueOfString("bob")), protoreflect.ValueOfString("bad"))

	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	viol := got[0]
	if p := viol.Path.String(); p != "contacts[bob]" {
		t.Errorf("path = %q, want contacts[bob]", p)
	}
	if viol.Cause.Code != "email.invalid" {
		t.Errorf("code = %q, want email.invalid", viol.Cause.Code)
	}
	if viol.RuleKind != protocheck.RuleKindTypeRefinement {
		t.Errorf("RuleKind = %v, want TYPE_REFINEMENT", viol.RuleKind)
	}
	wantChain := []string{"string", "coll.Email"}
	if len(viol.TypeChain) != 2 || viol.TypeChain[0] != wantChain[0] || viol.TypeChain[1] != wantChain[1] {
		t.Errorf("TypeChain = %v, want %v", viol.TypeChain, wantChain)
	}
	if viol.ActualValue != "bad" {
		t.Errorf("ActualValue = %v, want the entry value", viol.ActualValue)
	}
}

func TestMapCollectionRule(t *testing.T) {
	fd := compileProtos(t, map[string]string{"coll.proto": collectionSrc}, "coll.proto")["coll.proto"]
	v := collValidator(t, fd)

	// Empty map: its size rule fires.
	msg := collBook(t, fd, nil, []string{"a@b"}, nil)
	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %v", got)
	}
	if got[0].Cause.Code != "tags.empty" || got[0].Path.String() != "tags" {
		t.Errorf("violation = %v, want tags.empty at tags", got[0])
	}
}

const mapKeySrc = `syntax = "proto3";
package mk;

import "protowire/schema/v1/annotations.proto";

type Code = string @validate(this.size() == 2, code = "code.length");
type Score = int64 @validate(this >= 0, code = "score.negative");

message Board {
  map<Code, Score> scores = 1;
}
`

func mapKeyBoard(t *testing.T, fd protoreflect.FileDescriptor, scores map[string]int64) proto.Message {
	t.Helper()
	md := fd.Messages().ByName("Board")
	msg := dynamicpb.NewMessage(md)
	m := msg.Mutable(md.Fields().ByName("scores")).Map()
	for k, val := range scores {
		m.Set(protoreflect.MapKey(protoreflect.ValueOfString(k)), protoreflect.ValueOfInt64(val))
	}
	return msg
}

func TestMapKeyRulesPerEntry(t *testing.T) {
	// Both dimensions per entry (#53, §6.4): keys against the key type's
	// chain with ForKey set, values against the value type's chain —
	// key before value within one entry, entries in sorted key order.
	fd := compileProtos(t, map[string]string{"mk.proto": mapKeySrc}, "mk.proto")["mk.proto"]
	v, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })

	// "ok" passes both dims; "bad" fails both; "x" fails the key only.
	msg := mapKeyBoard(t, fd, map[string]int64{"ok": 1, "bad": -1, "x": 2})
	got := carrierViolations(t, v, msg)
	if len(got) != 3 {
		t.Fatalf("expected 3 violations, got %v", got)
	}

	want := []struct {
		path   string
		code   string
		forKey bool
		actual any
	}{
		{"scores[bad]", "code.length", true, "bad"},
		{"scores[bad]", "score.negative", false, -1},
		{"scores[x]", "code.length", true, "x"},
	}
	for i, w := range want {
		viol := got[i]
		if p := viol.Path.String(); p != w.path {
			t.Errorf("[%d] path = %q, want %q", i, p, w.path)
		}
		if viol.Cause.Code != w.code {
			t.Errorf("[%d] code = %q, want %q", i, viol.Cause.Code, w.code)
		}
		if viol.ForKey != w.forKey {
			t.Errorf("[%d] ForKey = %v, want %v", i, viol.ForKey, w.forKey)
		}
		if viol.ActualValue != w.actual {
			t.Errorf("[%d] ActualValue = %v, want %v", i, viol.ActualValue, w.actual)
		}
		if viol.RuleKind != protocheck.RuleKindTypeRefinement {
			t.Errorf("[%d] RuleKind = %v, want TYPE_REFINEMENT", i, viol.RuleKind)
		}
	}

	wantKeyChain := []string{"string", "mk.Code"}
	if kc := got[0].TypeChain; len(kc) != 2 || kc[0] != wantKeyChain[0] || kc[1] != wantKeyChain[1] {
		t.Errorf("key TypeChain = %v, want %v", kc, wantKeyChain)
	}
	wantValChain := []string{"int64", "mk.Score"}
	if vc := got[1].TypeChain; len(vc) != 2 || vc[0] != wantValChain[0] || vc[1] != wantValChain[1] {
		t.Errorf("value TypeChain = %v, want %v", vc, wantValChain)
	}
}

func TestMapKeyFailFast(t *testing.T) {
	fd := compileProtos(t, map[string]string{"mk.proto": mapKeySrc}, "mk.proto")["mk.proto"]
	v, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })

	msg := mapKeyBoard(t, fd, map[string]int64{"ok": 1, "bad": -1, "x": 2})
	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("fail-fast: expected exactly 1 violation, got %v", got)
	}
	if !got[0].ForKey || got[0].Path.String() != "scores[bad]" {
		t.Errorf("fail-fast must stop on the first entry's key violation, got %+v", got[0])
	}
}

func TestCollectionFailFast(t *testing.T) {
	fd := compileProtos(t, map[string]string{"coll.proto": collectionSrc}, "coll.proto")["coll.proto"]
	v, err := protocheck.New(
		protocheck.WithFunction("coll.matches", protocheck.Function(fxContainsAt)),
		protocheck.WithDescriptors(fd),
		protocheck.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })

	// Multiple bad elements and an empty map: exactly one violation.
	msg := collBook(t, fd, []string{"bad1", "bad2"}, nil, nil)
	got := carrierViolations(t, v, msg)
	if len(got) != 1 {
		t.Fatalf("fail-fast: expected exactly 1 violation, got %v", got)
	}
}
