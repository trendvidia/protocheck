package protocheck_test

import (
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2/internal/testpb"
)

func TestStringRules(t *testing.T) {
	v := mustValidator(t)

	tests := []struct {
		name  string
		msg   *testpb.StringShapes
		valid bool
		id    string
	}{
		// const = "ABC"
		{name: "const match", msg: &testpb.StringShapes{Code: "ABC"}, valid: true},
		{name: "const mismatch", msg: &testpb.StringShapes{Code: "XYZ"}, valid: false, id: "string.const"},
		{name: "const empty ignored", msg: &testpb.StringShapes{Code: ""}, valid: true},

		// len = 5 (runes)
		{name: "len exact ascii", msg: &testpb.StringShapes{Slug: "abcde"}, valid: true},
		{name: "len short", msg: &testpb.StringShapes{Slug: "abcd"}, valid: false, id: "string.len"},
		{name: "len long", msg: &testpb.StringShapes{Slug: "abcdef"}, valid: false, id: "string.len"},
		// "café!" is 5 runes but 6 bytes — len counts runes so this passes.
		{name: "len counts runes not bytes", msg: &testpb.StringShapes{Slug: "café!"}, valid: true},

		// len_bytes = 5
		{name: "len_bytes exact", msg: &testpb.StringShapes{LegacySlug: "abcde"}, valid: true},
		{name: "len_bytes short", msg: &testpb.StringShapes{LegacySlug: "abcd"}, valid: false, id: "string.len_bytes"},
		// "café" is 4 runes but 5 bytes — len_bytes passes.
		{name: "len_bytes counts bytes not runes", msg: &testpb.StringShapes{LegacySlug: "café"}, valid: true},

		// min_bytes = 4
		{name: "min_bytes floor", msg: &testpb.StringShapes{ShortText: "abcd"}, valid: true},
		{name: "min_bytes below", msg: &testpb.StringShapes{ShortText: "abc"}, valid: false, id: "string.min_bytes"},
		// "café" is 5 bytes — passes a 4-byte floor.
		{name: "min_bytes multibyte", msg: &testpb.StringShapes{ShortText: "café"}, valid: true},

		// max_bytes = 6
		{name: "max_bytes ceil", msg: &testpb.StringShapes{TruncatedText: "abcdef"}, valid: true},
		{name: "max_bytes above", msg: &testpb.StringShapes{TruncatedText: "abcdefg"}, valid: false, id: "string.max_bytes"},

		// prefix = "pre-"
		{name: "prefix match", msg: &testpb.StringShapes{Prefixed: "pre-x"}, valid: true},
		{name: "prefix mismatch", msg: &testpb.StringShapes{Prefixed: "x-pre-"}, valid: false, id: "string.prefix"},

		// suffix = "-end"
		{name: "suffix match", msg: &testpb.StringShapes{Suffixed: "x-end"}, valid: true},
		{name: "suffix mismatch", msg: &testpb.StringShapes{Suffixed: "-end-x"}, valid: false, id: "string.suffix"},

		// contains = "foo"
		{name: "contains match", msg: &testpb.StringShapes{MustContain: "xfoox"}, valid: true},
		{name: "contains mismatch", msg: &testpb.StringShapes{MustContain: "xxx"}, valid: false, id: "string.contains"},

		// not_contains = "spam"
		{name: "not_contains absent", msg: &testpb.StringShapes{MustNotContain: "all good"}, valid: true},
		{name: "not_contains present", msg: &testpb.StringShapes{MustNotContain: "very spammy"}, valid: false, id: "string.not_contains"},

		// uri_ref — relative and absolute references both pass; only
		// truly malformed escapes are rejected.
		{name: "uri_ref relative", msg: &testpb.StringShapes{RelativeUri: "/foo/bar?q=1"}, valid: true},
		{name: "uri_ref absolute", msg: &testpb.StringShapes{RelativeUri: "https://example.com/x"}, valid: true},
		{name: "uri_ref bad escape", msg: &testpb.StringShapes{RelativeUri: "%ZZ"}, valid: false, id: "string.uri_ref"},
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

// Sanity for the rune/byte distinction we rely on in the table above.
func TestStringRules_RuneVsByte(t *testing.T) {
	if r, b := len([]rune("café")), len("café"); r == b {
		t.Skipf("test setup invariant violated: expected rune count != byte count for 'café'; got %d == %d", r, b)
	}
	if !strings.HasPrefix("café", "ca") {
		t.Fatal("strings.HasPrefix sanity check")
	}
}
