package protocheck_test

// Catalog + i18n (#11 / RFC-001 §7): MapCatalog interpolation and the
// end-to-end locale rendering path over conformance fixture 05, in two
// locales with distinct fallback behavior.

import (
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
)

func TestMapCatalogRender(t *testing.T) {
	cat := protocheck.NewMapCatalog(map[string]string{
		"string.min_len": "must be at least {min_len} characters",
		"range":          "between {min} and {max}",
		"plain":          "no placeholders here",
		"unknown.param":  "value {nope} stays",
		"stray":          "brace { without close",
	})

	cases := []struct {
		code   string
		params map[string]any
		want   string
		ok     bool
	}{
		{"string.min_len", map[string]any{"min_len": uint64(3)}, "must be at least 3 characters", true},
		{"range", map[string]any{"min": 1, "max": 9}, "between 1 and 9", true},
		{"plain", map[string]any{"x": 1}, "no placeholders here", true},
		{"unknown.param", map[string]any{"x": 1}, "value {nope} stays", true},
		{"stray", map[string]any{"x": 1}, "brace { without close", true},
		{"missing.code", nil, "", false},
	}
	for _, c := range cases {
		got, ok := cat.Render(c.code, c.params)
		if ok != c.ok || got != c.want {
			t.Errorf("Render(%q) = (%q, %v), want (%q, %v)", c.code, got, ok, c.want, c.ok)
		}
	}
}

func TestCatalogLocalesOverFixture(t *testing.T) {
	fd := compileFixtures(t, "05_error_overrides.proto")["05_error_overrides.proto"]

	en := protocheck.NewMapCatalog(map[string]string{
		"users.email.format":      "email address is invalid (expected {pattern})",
		"users.country.invalid":   "country not supported",
		"fixtures.errors.is_e164": "phone number must be E.164",
	})
	fr := protocheck.NewMapCatalog(map[string]string{
		"users.email.format":    "adresse e-mail invalide (attendu {pattern})",
		"users.country.invalid": "pays non pris en charge",
		// no is_e164 entry: falls back per-violation
	})

	v, err := protocheck.New(
		protocheck.WithFunction("fixtures.errors.matches", protocheck.Function(fxMatches)),
		protocheck.WithFunction("fixtures.errors.is_e164", protocheck.Function(fxE164)),
		protocheck.WithDescriptors(fd),
		protocheck.WithStrictValidation(),
		protocheck.WithCatalog("en", en),
		protocheck.WithCatalog("fr", fr),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	report, err := v.Engine().Validate(newDynamic(t, fd, "User", map[string]string{
		"email": "nope", "phone": "nope", "country": "DE",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 3 {
		t.Fatalf("expected 3 violations, got %v", report.Violations)
	}

	find := func(lines []string, substr string) string {
		for _, l := range lines {
			if strings.Contains(l, substr) {
				return l
			}
		}
		t.Fatalf("no line containing %q in %v", substr, lines)
		return ""
	}

	enLines := report.Format("en")
	// Params interpolate into the catalog template (pattern comes from the
	// function-returned violation).
	if l := find(enLines, "email"); !strings.Contains(l, "email address is invalid (expected ^[^@]+@[^@]+$)") {
		t.Errorf("en email line = %q", l)
	}
	if l := find(enLines, "phone"); !strings.Contains(l, "phone number must be E.164") {
		t.Errorf("en phone line = %q", l)
	}

	frLines := report.Format("fr")
	if l := find(frLines, "email"); !strings.Contains(l, "adresse e-mail invalide (attendu ^[^@]+@[^@]+$)") {
		t.Errorf("fr email line = %q", l)
	}
	if l := find(frLines, "country"); !strings.Contains(l, "pays non pris en charge") {
		t.Errorf("fr country line = %q", l)
	}
	// Code missing from the fr catalog: per-violation fallback message.
	if l := find(frLines, "phone"); !strings.Contains(l, "not an E.164 number") {
		t.Errorf("fr phone line should fall back, got %q", l)
	}

	// Locale with no catalog at all: everything falls back.
	deLines := report.Format("de")
	if l := find(deLines, "email"); !strings.Contains(l, "must be a valid email address") {
		t.Errorf("de email line should fall back to the use-site message, got %q", l)
	}
}

func TestWithCatalogErrors(t *testing.T) {
	cat := protocheck.NewMapCatalog(nil)

	if _, err := protocheck.New(protocheck.WithCatalog("", cat)); err == nil {
		t.Error("expected error for empty locale")
	}
	if _, err := protocheck.New(protocheck.WithCatalog("en", nil)); err == nil {
		t.Error("expected error for nil catalog")
	}
	if _, err := protocheck.New(
		protocheck.WithCatalog("en", cat),
		protocheck.WithCatalog("en", cat),
	); err == nil {
		t.Error("expected error for duplicate locale")
	}

	// A construction-time catalog occupies the locale for RegisterCatalog.
	v, err := protocheck.New(protocheck.WithCatalog("en", cat))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.Engine().RegisterCatalog("en", cat); err == nil {
		t.Error("expected duplicate-locale error via Engine.RegisterCatalog")
	}
	if err := v.Engine().RegisterCatalog("fr", cat); err != nil {
		t.Errorf("RegisterCatalog(fr): %v", err)
	}
}
