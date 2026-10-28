package protocheck_test

import (
	"net"
	"testing"

	"github.com/trendvidia/protocheck/v2/internal/testpb"
)

func TestBytesRules(t *testing.T) {
	v := mustValidator(t)

	tests := []struct {
		name  string
		msg   *testpb.BytesShapes
		valid bool
		id    string
	}{
		// const
		{name: "const match", msg: &testpb.BytesShapes{Signature: []byte{0x01, 0x02, 0x03}}, valid: true},
		{name: "const mismatch", msg: &testpb.BytesShapes{Signature: []byte{0x01, 0x02}}, valid: false, id: "bytes.const"},

		// len (exact)
		{name: "len exact", msg: &testpb.BytesShapes{Token: []byte{1, 2, 3, 4}}, valid: true},
		{name: "len short", msg: &testpb.BytesShapes{Token: []byte{1, 2, 3}}, valid: false, id: "bytes.len"},
		{name: "len long", msg: &testpb.BytesShapes{Token: []byte{1, 2, 3, 4, 5}}, valid: false, id: "bytes.len"},

		// min_len / max_len
		{name: "min_len floor", msg: &testpb.BytesShapes{Payload: []byte{1, 2}}, valid: true},
		{name: "min_len below", msg: &testpb.BytesShapes{Payload: []byte{1}}, valid: false, id: "bytes.min_len"},
		{name: "max_len ceil", msg: &testpb.BytesShapes{Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}}, valid: true},
		{name: "max_len above", msg: &testpb.BytesShapes{Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}}, valid: false, id: "bytes.max_len"},

		// prefix
		{name: "prefix match", msg: &testpb.BytesShapes{Header: []byte{0xCA, 0xFE, 0x00}}, valid: true},
		{name: "prefix mismatch", msg: &testpb.BytesShapes{Header: []byte{0x00, 0xCA, 0xFE}}, valid: false, id: "bytes.prefix"},

		// suffix
		{name: "suffix match", msg: &testpb.BytesShapes{Footer: []byte{0x00, 0xBE, 0xEF}}, valid: true},
		{name: "suffix mismatch", msg: &testpb.BytesShapes{Footer: []byte{0xBE, 0xEF, 0x00}}, valid: false, id: "bytes.suffix"},

		// contains
		{name: "contains match", msg: &testpb.BytesShapes{Magic: []byte{0x00, 0xDE, 0xAD, 0x00}}, valid: true},
		{name: "contains mismatch", msg: &testpb.BytesShapes{Magic: []byte{0x00, 0x00, 0x00}}, valid: false, id: "bytes.contains"},

		// ip (4-byte IPv4 or 16-byte IPv6)
		{name: "ip v4", msg: &testpb.BytesShapes{Addr: net.IPv4(192, 168, 0, 1).To4()}, valid: true},
		{name: "ip v6", msg: &testpb.BytesShapes{Addr: net.ParseIP("2001:db8::1").To16()}, valid: true},
		{name: "ip wrong length", msg: &testpb.BytesShapes{Addr: []byte{1, 2, 3}}, valid: false, id: "bytes.ip"},

		// ipv4 strictly 4 bytes
		{name: "ipv4 ok", msg: &testpb.BytesShapes{Addr4: net.IPv4(10, 0, 0, 1).To4()}, valid: true},
		{name: "ipv4 wrong length", msg: &testpb.BytesShapes{Addr4: []byte{10, 0, 0}}, valid: false, id: "bytes.ipv4"},

		// ipv6 strictly 16 bytes
		{name: "ipv6 ok", msg: &testpb.BytesShapes{Addr6: net.ParseIP("2001:db8::1").To16()}, valid: true},
		{name: "ipv6 wrong length", msg: &testpb.BytesShapes{Addr6: []byte{1, 2, 3, 4}}, valid: false, id: "bytes.ipv6"},

		// in / not_in (use bytes.Equal under the hood since []byte isn't comparable)
		{name: "in member", msg: &testpb.BytesShapes{Allowlisted: []byte{0x01, 0x02}}, valid: true},
		{name: "in other member", msg: &testpb.BytesShapes{Allowlisted: []byte{0x03, 0x04}}, valid: true},
		{name: "in non-member", msg: &testpb.BytesShapes{Allowlisted: []byte{0x09, 0x09}}, valid: false, id: "bytes.in"},

		{name: "not_in clear", msg: &testpb.BytesShapes{Blocklisted: []byte{0x00, 0x00}}, valid: true},
		{name: "not_in hit first", msg: &testpb.BytesShapes{Blocklisted: []byte{0xDE, 0xAD}}, valid: false, id: "bytes.not_in"},
		{name: "not_in hit second", msg: &testpb.BytesShapes{Blocklisted: []byte{0xBE, 0xEF}}, valid: false, id: "bytes.not_in"},
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
