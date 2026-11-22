package protocheck_test

import (
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
)

// WithZeroValueCheck refuses a @validate rule that refuses the zero value of
// a field that skips its rules at zero (#46).
const zeroDeadSrc = `syntax = "proto3";
package zerodead;

import "protowire/schema/v1/annotations.proto";

enum Mode {
  MODE_UNSPECIFIED = 0;
  MODE_MATCH = 1;
}

type Bpm = float @validate(this >= 40.0);

message Tempo {
  float bpm = 1 @validate(this >= 40.0 && this <= 220.0);
  Mode mode = 2 @validate(this != MODE_UNSPECIFIED);
  Bpm aliased = 3;
}
`

// zeroLiveSrc has only rules the check must leave alone.
const zeroLiveSrc = `syntax = "proto3";
package zerolive;

import "protowire/schema/v1/annotations.proto";

function unregistered(value: string);
function panics(value: string);
function refuse_inner(msg: Inner);

message Inner {
  string name = 1;
}

message Shapes {
  // Presence: zero is populated, so the rule sees it.
  optional float with_presence = 1 @validate(this >= 40.0);
  // @required refuses the unpopulated zero itself.
  float required = 2 @required @validate(this >= 40.0);
  // The rule runs against the @default.
  float defaulted = 3 @default(90.0) @validate(this >= 40.0);
  // Zero passes the rule.
  int32 accepts_zero = 4 @validate(this >= 0 && this <= 600);
  string accepts_empty = 5 @validate(this.size() <= 100);
  // Collections run their rules whatever their length.
  repeated float list = 6 @validate(this.size() > 0);
  // A message field has presence, so even a rule that refuses everything
  // is not flagged.
  Inner inner = 7 @validate(refuse_inner(this));
  // No implementation, or a panic at zero, says nothing about zero.
  string no_impl = 8 @validate(unregistered(this));
  string panicky = 9 @validate(panics(this));
}
`

func TestZeroValueCheckRefusesDeadRule(t *testing.T) {
	fd := compileProtos(t, map[string]string{"zerodead.proto": zeroDeadSrc}, "zerodead.proto")["zerodead.proto"]

	// Off by default.
	v, err := protocheck.New(protocheck.WithDescriptors(fd))
	if err != nil {
		t.Fatalf("New without the check: %v", err)
	}
	v.Close()

	_, err = protocheck.New(protocheck.WithDescriptors(fd), protocheck.WithZeroValueCheck())
	if err == nil {
		t.Fatal("New with the check accepted rules that zero can never reach")
	}
	for _, want := range []string{
		"3 @validate rule(s)",
		"zerodead.Tempo.bpm @validate(this >= 40.0 && this <= 220.0)",
		"zerodead.Tempo.mode @validate(this != MODE_UNSPECIFIED)",
		"zerodead.Tempo.aliased @validate(this >= 40.0)",
		"make the field optional and @required",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestZeroValueCheckAcceptsLiveRules(t *testing.T) {
	fd := compileProtos(t, map[string]string{"zerolive.proto": zeroLiveSrc}, "zerolive.proto")["zerolive.proto"]
	calledAtZero := false
	v, err := protocheck.New(
		protocheck.WithDescriptors(fd),
		protocheck.WithFunction("zerolive.panics", protocheck.Function(func(args []any) (bool, *protocheck.Violation) {
			if args[0] == "" {
				calledAtZero = true
				panic("empty")
			}
			return true, nil
		})),
		protocheck.WithFunction("zerolive.refuse_inner", protocheck.Function(func([]any) (bool, *protocheck.Violation) {
			return false, nil
		})),
		protocheck.WithZeroValueCheck(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v.Close()
	// The panicking rule was evaluated at zero, not skipped.
	if !calledAtZero {
		t.Error("the check never called the function with zero")
	}
}

// The trade-off WithZeroValueCheck's doc names: the RFC-001 conformance
// fixtures write "optional, but valid if given" rules on fields without
// presence, and the check refuses them.
func TestZeroValueCheckRefusesIfSetRules(t *testing.T) {
	fd := compileFixtures(t, "01_basic.proto")["01_basic.proto"]
	_, err := protocheck.New(protocheck.WithDescriptors(fd), protocheck.WithZeroValueCheck(),
		protocheck.WithFunction("fixtures.basic.is_email", protocheck.Function(fxContainsAt)))
	if err == nil || !strings.Contains(err.Error(), "fixtures.basic.User.email @validate(is_email(this))") {
		t.Fatalf("err = %v, want the fixture's email rule refused", err)
	}
}
