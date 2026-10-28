package protocheck_test

import (
	"errors"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"google.golang.org/protobuf/proto"
)

func mustValidator(t *testing.T) *protocheck.Validator {
	t.Helper()
	v, err := protocheck.New()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func expectValid(t *testing.T, v *protocheck.Validator, msg proto.Message) {
	t.Helper()
	if err := v.Validate(msg); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func expectInvalid(t *testing.T, v *protocheck.Validator, msg proto.Message, wantIDs ...string) {
	t.Helper()
	err := v.Validate(msg)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if len(wantIDs) == 0 {
		return
	}
	gotIDs := make(map[string]bool)
	for _, v := range ve.Violations {
		gotIDs[v.Cause.Code] = true
	}
	for _, id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("expected violation with constraint ID %q, got violations: %v", id, ve.Violations)
		}
	}
}

func expectViolationCount(t *testing.T, v *protocheck.Validator, msg proto.Message, count int) {
	t.Helper()
	err := v.Validate(msg)
	if count == 0 {
		if err != nil {
			t.Errorf("expected 0 violations, got: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected %d violations, got nil error", count)
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Violations) != count {
		t.Errorf("expected %d violations, got %d: %v", count, len(ve.Violations), ve.Violations)
	}
}

// ─── Required fields ────────────────────────────────────────────────────────

func TestRequired(t *testing.T) {
	v := mustValidator(t)

	// Missing required fields.
	expectInvalid(t, v, &testpb.User{}, "required")

	// Valid user.
	expectValid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
	})
}

// ─── String rules ───────────────────────────────────────────────────────────

func TestStringMinMaxLen(t *testing.T) {
	v := mustValidator(t)

	// Name too long (>100).
	expectInvalid(t, v, &testpb.User{
		Name:  string(make([]byte, 101)),
		Email: "a@b.com",
	}, "string.max_len")

	// Empty name (required + min_len).
	expectInvalid(t, v, &testpb.User{
		Name:  "",
		Email: "a@b.com",
	}, "required")
}

func TestStringPattern(t *testing.T) {
	v := mustValidator(t)

	// Invalid phone pattern.
	expectInvalid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Phone: "not-a-phone!@#",
	}, "string.pattern")

	// Valid phone.
	expectValid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Phone: "+1-555-123-4567",
	})
}

func TestStringEmail(t *testing.T) {
	v := mustValidator(t)

	// Invalid email.
	expectInvalid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "not-an-email",
	}, "string.email")

	// Valid email.
	expectValid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
	})
}

func TestStringPattern_ZipCode(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Address{
		Street: "123 Main St",
		City:   "Springfield",
		// Invalid zip code.
		ZipCode: "abc",
	}, "string.pattern")

	expectValid(t, v, &testpb.Address{
		Street:  "123 Main St",
		City:    "Springfield",
		ZipCode: "12345",
	})

	expectValid(t, v, &testpb.Address{
		Street:  "123 Main St",
		City:    "Springfield",
		ZipCode: "12345-6789",
	})
}

// ─── Numeric rules ──────────────────────────────────────────────────────────

func TestNumericGteLte(t *testing.T) {
	v := mustValidator(t)

	// age > 150
	expectInvalid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Age:   200,
	}, "uint32.lte")

	// age = 0 should be fine (gte: 0).
	expectValid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Age:   0,
	})
}

func TestDoubleGt(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Product{
		Name:  "Widget",
		Price: 0.0,
	}, "double.gt")

	expectInvalid(t, v, &testpb.Product{
		Name:  "Widget",
		Price: -5.0,
	}, "double.gt")

	expectValid(t, v, &testpb.Product{
		Name:  "Widget",
		Price: 9.99,
	})
}

func TestInt32Gte(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Product{
		Name:     "Widget",
		Price:    9.99,
		Quantity: -1,
	}, "int32.gte")

	expectValid(t, v, &testpb.Product{
		Name:     "Widget",
		Price:    9.99,
		Quantity: 0,
	})
}

// ─── Enum rules ─────────────────────────────────────────────────────────────

func TestEnumDefinedOnly(t *testing.T) {
	v := mustValidator(t)

	// Undefined enum value.
	expectInvalid(t, v, &testpb.Product{
		Name:   "Widget",
		Price:  9.99,
		Status: 99,
	}, "enum.defined_only")

	// Defined value.
	expectValid(t, v, &testpb.Product{
		Name:   "Widget",
		Price:  9.99,
		Status: testpb.Status_STATUS_ACTIVE,
	})
}

// ─── Repeated rules ─────────────────────────────────────────────────────────

func TestRepeatedMaxItems(t *testing.T) {
	v := mustValidator(t)

	tags := make([]string, 11)
	for i := range tags {
		tags[i] = string(rune('a' + i))
	}

	expectInvalid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Tags:  tags,
	}, "repeated.max_items")
}

func TestRepeatedUnique(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Tags:  []string{"go", "rust", "go"},
	}, "repeated.unique")

	expectValid(t, v, &testpb.User{
		Name:  "Alice",
		Email: "alice@example.com",
		Tags:  []string{"go", "rust", "python"},
	})
}

func TestRepeatedMinItems(t *testing.T) {
	v := mustValidator(t)

	// Order with no items.
	expectInvalid(t, v, &testpb.Order{
		Id: "550e8400-e29b-41d4-a716-446655440000",
		ShippingAddress: &testpb.Address{
			Street: "123 Main St",
			City:   "Springfield",
		},
		Items: nil,
	}, "repeated.min_items")
}

// ─── Map rules ──────────────────────────────────────────────────────────────

func TestMapMinMaxPairs(t *testing.T) {
	v := mustValidator(t)

	// Empty map (min_pairs: 1).
	expectInvalid(t, v, &testpb.Config{
		Labels: map[string]string{},
	}, "map.min_pairs")

	// Valid map.
	expectValid(t, v, &testpb.Config{
		Labels: map[string]string{"env": "prod"},
	})
}

// ─── Nested message validation ──────────────────────────────────────────────

func TestNestedValidation(t *testing.T) {
	v := mustValidator(t)

	// Invalid nested address (missing required fields).
	expectInvalid(t, v, &testpb.Order{
		Id: "550e8400-e29b-41d4-a716-446655440000",
		ShippingAddress: &testpb.Address{
			Street: "", // required
			City:   "", // required
		},
		Items: []*testpb.LineItem{{
			ProductId: "widget-1",
			Quantity:  1,
			UnitPrice: 9.99,
		}},
	}, "required")

	// Valid order.
	expectValid(t, v, &testpb.Order{
		Id: "550e8400-e29b-41d4-a716-446655440000",
		ShippingAddress: &testpb.Address{
			Street: "123 Main St",
			City:   "Springfield",
		},
		Items: []*testpb.LineItem{{
			ProductId: "widget-1",
			Quantity:  1,
			UnitPrice: 9.99,
		}},
	})
}

// ─── Oneof constraints ──────────────────────────────────────────────────────

func TestOneofRequired(t *testing.T) {
	v := mustValidator(t)

	// Missing oneof.
	expectInvalid(t, v, &testpb.Notification{
		Recipient: "Alice",
	}, "required")

	// Valid with email.
	expectValid(t, v, &testpb.Notification{
		Recipient: "Alice",
		Channel:   &testpb.Notification_EmailAddress{EmailAddress: "alice@example.com"},
	})

	// Valid with phone.
	expectValid(t, v, &testpb.Notification{
		Recipient: "Alice",
		Channel:   &testpb.Notification_PhoneNumber{PhoneNumber: "1234567890"},
	})

	// Valid with webhook.
	expectValid(t, v, &testpb.Notification{
		Recipient: "Alice",
		Channel:   &testpb.Notification_WebhookUrl{WebhookUrl: "https://example.com/hook"},
	})
}

func TestOneofFieldValidation(t *testing.T) {
	v := mustValidator(t)

	// Invalid email in oneof.
	expectInvalid(t, v, &testpb.Notification{
		Recipient: "Alice",
		Channel:   &testpb.Notification_EmailAddress{EmailAddress: "not-an-email"},
	}, "string.email")

	// Phone too short.
	expectInvalid(t, v, &testpb.Notification{
		Recipient: "Alice",
		Channel:   &testpb.Notification_PhoneNumber{PhoneNumber: "123"},
	}, "string.min_len")
}

// ─── Custom expressions ─────────────────────────────────────────────────────

// Expression evaluation semantics live with the engines. Core covers the
// Check-rule dispatch through the stub engine in stub_engine_test.go.

// ─── Ignore behavior ────────────────────────────────────────────────────────

func TestIgnoreIfUnpopulated(t *testing.T) {
	v := mustValidator(t)

	// Empty nickname should pass (IGNORE_IF_UNPOPULATED).
	expectValid(t, v, &testpb.OptionalFields{})

	// Short nickname should fail when populated.
	expectInvalid(t, v, &testpb.OptionalFields{
		Nickname: "ab",
	}, "string.min_len")

	// Valid nickname.
	expectValid(t, v, &testpb.OptionalFields{
		Nickname: "alice",
	})
}

func TestIgnoreAlways(t *testing.T) {
	v := mustValidator(t)

	// always_ignored field should never be validated, even with a value.
	expectValid(t, v, &testpb.OptionalFields{
		AlwaysIgnored: "short",
	})
}

// ─── Disabled message ───────────────────────────────────────────────────────

func TestDisabledMessage(t *testing.T) {
	v := mustValidator(t)

	// Should pass even though name is empty and has strict rules.
	expectValid(t, v, &testpb.SkippedMessage{})
	expectValid(t, v, &testpb.SkippedMessage{Name: ""})
}

// ─── Format validators ─────────────────────────────────────────────────────

func TestFormatValidators(t *testing.T) {
	v := mustValidator(t)

	tests := []struct {
		name  string
		msg   *testpb.Formats
		valid bool
		id    string
	}{
		{
			name:  "valid email",
			msg:   &testpb.Formats{Email: "user@example.com"},
			valid: true,
		},
		{
			name:  "invalid email",
			msg:   &testpb.Formats{Email: "not-email"},
			valid: false,
			id:    "string.email",
		},
		{
			name:  "valid hostname",
			msg:   &testpb.Formats{Hostname: "example.com"},
			valid: true,
		},
		{
			name:  "invalid hostname",
			msg:   &testpb.Formats{Hostname: "not a hostname!"},
			valid: false,
			id:    "string.hostname",
		},
		{
			name:  "valid ip",
			msg:   &testpb.Formats{IpAddress: "192.168.1.1"},
			valid: true,
		},
		{
			name:  "invalid ip",
			msg:   &testpb.Formats{IpAddress: "999.999.999.999"},
			valid: false,
			id:    "string.ip",
		},
		{
			name:  "valid ipv4",
			msg:   &testpb.Formats{Ipv4Address: "10.0.0.1"},
			valid: true,
		},
		{
			name:  "ipv6 in ipv4 field",
			msg:   &testpb.Formats{Ipv4Address: "::1"},
			valid: false,
			id:    "string.ipv4",
		},
		{
			name:  "valid ipv6",
			msg:   &testpb.Formats{Ipv6Address: "::1"},
			valid: true,
		},
		{
			name:  "ipv4 in ipv6 field",
			msg:   &testpb.Formats{Ipv6Address: "192.168.1.1"},
			valid: false,
			id:    "string.ipv6",
		},
		{
			name:  "valid uri",
			msg:   &testpb.Formats{UriValue: "https://example.com/path"},
			valid: true,
		},
		{
			name:  "invalid uri",
			msg:   &testpb.Formats{UriValue: "not a uri"},
			valid: false,
			id:    "string.uri",
		},
		{
			name:  "valid uuid",
			msg:   &testpb.Formats{UuidValue: "550e8400-e29b-41d4-a716-446655440000"},
			valid: true,
		},
		{
			name:  "invalid uuid",
			msg:   &testpb.Formats{UuidValue: "not-a-uuid"},
			valid: false,
			id:    "string.uuid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.valid {
				expectValid(t, v, tc.msg)
			} else {
				expectInvalid(t, v, tc.msg, tc.id)
			}
		})
	}
}

// ─── In / NotIn ─────────────────────────────────────────────────────────────

func TestStringIn(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Membership{
		CountryCode: "XX",
	}, "string.in")

	expectValid(t, v, &testpb.Membership{
		CountryCode: "US",
	})
}

func TestInt32In(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Membership{
		Priority: 99,
	}, "int32.in")

	expectValid(t, v, &testpb.Membership{
		Priority: 3,
	})
}

func TestStringNotIn(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Membership{
		BlockedWord: "spam",
	}, "string.not_in")

	expectValid(t, v, &testpb.Membership{
		BlockedWord: "hello",
	})
}

// ─── Violation count ────────────────────────────────────────────────────────

func TestMultipleViolations(t *testing.T) {
	v := mustValidator(t)

	// User with multiple issues: name required, email required.
	err := v.Validate(&testpb.User{})
	if err == nil {
		t.Fatal("expected error")
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("expected ValidationError")
	}
	if len(ve.Violations) < 2 {
		t.Errorf("expected at least 2 violations, got %d", len(ve.Violations))
	}
}

// ─── UUID validation on Order ID ────────────────────────────────────────────

func TestOrderUUID(t *testing.T) {
	v := mustValidator(t)

	expectInvalid(t, v, &testpb.Order{
		Id: "not-a-uuid",
		ShippingAddress: &testpb.Address{
			Street: "123 Main St",
			City:   "Springfield",
		},
		Items: []*testpb.LineItem{{
			ProductId: "w-1",
			Quantity:  1,
			UnitPrice: 9.99,
		}},
	}, "string.uuid")
}

// ─── Nested message expressions ─────────────────────────────────────────────

// ─── Fail-fast mode ─────────────────────────────────────────────────────────

func TestFailFast(t *testing.T) {
	// An empty User violates required on both name and email: collect-all
	// reports both, fail-fast exactly one.
	collectAll := mustValidator(t)
	ff, err := protocheck.New(protocheck.WithFailFast())
	if err != nil {
		t.Fatal(err)
	}

	err = collectAll.Validate(&testpb.User{})
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) || len(ve.Violations) < 2 {
		t.Fatalf("collect-all: expected >=2 violations, got %v", err)
	}

	expectViolationCount(t, ff, &testpb.User{}, 1)

	// Valid messages are unaffected.
	expectValid(t, ff, &testpb.User{Name: "Alice", Email: "alice@example.com"})

	// Nested walk short-circuits too: the empty nested Address violates
	// required on both street and city under collect-all.
	badOrder := &testpb.Order{
		Id:              "550e8400-e29b-41d4-a716-446655440000",
		ShippingAddress: &testpb.Address{},
		Items: []*testpb.LineItem{{
			ProductId: "widget-1",
			Quantity:  1,
			UnitPrice: 9.99,
		}},
	}
	err = collectAll.Validate(badOrder)
	if !errors.As(err, &ve) || len(ve.Violations) < 2 {
		t.Fatalf("collect-all nested: expected >=2 violations, got %v", err)
	}
	expectViolationCount(t, ff, badOrder, 1)
}

func BenchmarkValidateStandardRules(b *testing.B) {
	v, err := protocheck.New()
	if err != nil {
		b.Fatal(err)
	}
	msg := &testpb.User{Name: "Alice", Email: "alice@example.com"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := v.Validate(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// ─── ValidationError.Error() ────────────────────────────────────────────────

func TestValidationErrorString(t *testing.T) {
	v := mustValidator(t)

	err := v.Validate(&testpb.User{})
	if err == nil {
		t.Fatal("expected error")
	}

	s := err.Error()
	if s == "" {
		t.Error("error string should not be empty")
	}
	t.Log(s)
}
