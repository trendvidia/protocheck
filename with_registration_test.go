package protocheck_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
)

const regionSrc = `syntax = "proto3";
package fixtures.expressions;

import "protowire/schema/v1/annotations.proto";

function in_region(value: string);

message Account {
  string country = 1 @validate(in_region(this), code = "account.bad_region");
}
`

// regionFunctions has the shape of RFC-001 §9.3 generated code: an interface,
// an Unimplemented default to embed, and a helper binding methods to FQNs.
type regionFunctions interface {
	InRegion(args []any) (bool, *protocheck.Violation)
}

type unimplementedRegionFunctions struct{}

func (unimplementedRegionFunctions) InRegion([]any) (bool, *protocheck.Violation) {
	return protocheck.UnimplementedFunction(inRegionFQN)(nil)
}

func registerRegionFunctions(eng protocheck.Engine, impl regionFunctions) error {
	return eng.Register(inRegionFQN, impl.InRegion)
}

type allowRegions struct{ calls *int }

func (r allowRegions) InRegion([]any) (bool, *protocheck.Violation) {
	*r.calls++
	return true, nil
}

func TestWithRegistrationSatisfiesStrict(t *testing.T) {
	fd := compileRFC(t, regionSrc)
	calls := 0
	v, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithRegistration(func(e protocheck.Engine) error {
			return registerRegionFunctions(e, allowRegions{&calls})
		}),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.Close()

	msg := newDynamic(t, fd, "Account", map[string]string{"country": "US"})
	if got := carrierViolations(t, v, msg); len(got) != 0 {
		t.Fatalf("violations: %v", got)
	}
	if calls != 1 {
		t.Fatalf("registered implementation ran %d times, want 1", calls)
	}
}

func TestWithRegistrationMissingFunctionStillFailsNew(t *testing.T) {
	fd := compileRFC(t, regionSrc)
	ran := false
	_, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithRegistration(func(protocheck.Engine) error {
			ran = true
			return nil
		}),
		protocheck.WithStrictValidation(),
	)
	if !ran {
		t.Fatal("the registration never ran")
	}
	if err == nil || !strings.Contains(err.Error(), inRegionFQN) {
		t.Fatalf("New = %v, want it to name %s", err, inRegionFQN)
	}
}

// Pins what strict does not promise: it checks that a name is registered, and
// an embedded Unimplemented method is registered. The miss surfaces at first
// call, closed.
func TestStrictPassesAnEmbeddedUnimplementedMethod(t *testing.T) {
	fd := compileRFC(t, regionSrc)
	v, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithRegistration(func(e protocheck.Engine) error {
			return registerRegionFunctions(e, struct{ unimplementedRegionFunctions }{})
		}),
		protocheck.WithStrictValidation(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.Close()

	got := carrierViolations(t, v, newDynamic(t, fd, "Account", map[string]string{"country": "US"}))
	if s := violationCodes(got); s != "country=account.bad_region" {
		t.Fatalf("violations = %s", s)
	}
}

func TestWithRegistrationErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := protocheck.New(protocheck.WithRegistration(func(protocheck.Engine) error { return boom })); !errors.Is(err, boom) {
		t.Errorf("failing registration: New = %v", err)
	}
	if _, err := protocheck.New(protocheck.WithRegistration(nil)); err == nil {
		t.Error("nil registration accepted")
	}
	// A registration meets WithFunction in one registry, so a clash is an error.
	_, err := protocheck.New(
		protocheck.WithFunction(inRegionFQN, protocheck.Function(inRegionImpl)),
		protocheck.WithRegistration(func(e protocheck.Engine) error {
			return e.Register(inRegionFQN, inRegionImpl)
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate: New = %v", err)
	}
}

// Every registration runs, so a project with functions in several files passes
// one per generated helper.
func TestWithRegistrationRunsEach(t *testing.T) {
	var order []string
	_, err := protocheck.New(
		protocheck.WithRegistration(func(protocheck.Engine) error { order = append(order, "a"); return nil }),
		protocheck.WithRegistration(func(protocheck.Engine) error { order = append(order, "b"); return nil }),
	)
	if err != nil || strings.Join(order, "") != "ab" {
		t.Fatalf("order %v, err %v", order, err)
	}
}
