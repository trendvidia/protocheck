package protocheck

import (
	"bytes"
	"fmt"
	"math"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	pb "github.com/trendvidia/protocheck/v2/protocheckpb"
)

// ─── Generic numeric helpers ────────────────────────────────────────────────

type ordered interface {
	~int32 | ~int64 | ~uint32 | ~uint64 | ~float32 | ~float64
}

func checkConst[T comparable](val, want T, prefix string, path FieldPath) *EnrichedViolation {
	if val != want {
		return newViolationP(path, prefix+".const", params("const", want), "value must equal %v", want)
	}
	return nil
}

func checkLt[T ordered](val, bound T, prefix string, path FieldPath) *EnrichedViolation {
	if val >= bound {
		return newViolationP(path, prefix+".lt", params("lt", bound), "value must be less than %v", bound)
	}
	return nil
}

func checkLte[T ordered](val, bound T, prefix string, path FieldPath) *EnrichedViolation {
	if val > bound {
		return newViolationP(path, prefix+".lte", params("lte", bound), "value must be at most %v", bound)
	}
	return nil
}

func checkGt[T ordered](val, bound T, prefix string, path FieldPath) *EnrichedViolation {
	if val <= bound {
		return newViolationP(path, prefix+".gt", params("gt", bound), "value must be greater than %v", bound)
	}
	return nil
}

func checkGte[T ordered](val, bound T, prefix string, path FieldPath) *EnrichedViolation {
	if val < bound {
		return newViolationP(path, prefix+".gte", params("gte", bound), "value must be at least %v", bound)
	}
	return nil
}

func checkIn[T comparable](val T, allowed []T, prefix string, path FieldPath) *EnrichedViolation {
	if len(allowed) == 0 {
		return nil
	}
	for _, a := range allowed {
		if val == a {
			return nil
		}
	}
	return newViolationP(path, prefix+".in", params("in", allowed), "value must be one of %v", allowed)
}

func checkNotIn[T comparable](val T, disallowed []T, prefix string, path FieldPath) *EnrichedViolation {
	for _, d := range disallowed {
		if val == d {
			return newViolationP(path, prefix+".not_in", params("not_in", disallowed), "value must not be one of %v", disallowed)
		}
	}
	return nil
}

// params builds a single-entry Params map — the common case where a rule has
// exactly one threshold worth carrying for catalog interpolation.
func params(name string, value any) map[string]any {
	return map[string]any{name: value}
}

func validateNumeric[T interface {
	ordered
	comparable
}](val T, constVal *T, lt, lte, gt, gte *T, in, notIn []T, prefix string, path FieldPath) []*EnrichedViolation {
	var vs []*EnrichedViolation
	if constVal != nil {
		if v := checkConst(val, *constVal, prefix, path); v != nil {
			vs = append(vs, v)
		}
	}
	if lt != nil {
		if v := checkLt(val, *lt, prefix, path); v != nil {
			vs = append(vs, v)
		}
	}
	if lte != nil {
		if v := checkLte(val, *lte, prefix, path); v != nil {
			vs = append(vs, v)
		}
	}
	if gt != nil {
		if v := checkGt(val, *gt, prefix, path); v != nil {
			vs = append(vs, v)
		}
	}
	if gte != nil {
		if v := checkGte(val, *gte, prefix, path); v != nil {
			vs = append(vs, v)
		}
	}
	if v := checkIn(val, in, prefix, path); v != nil {
		vs = append(vs, v)
	}
	if v := checkNotIn(val, notIn, prefix, path); v != nil {
		vs = append(vs, v)
	}
	return vs
}

func checkFinite(val float64, prefix string, path FieldPath) *EnrichedViolation {
	if math.IsInf(val, 0) || math.IsNaN(val) {
		return newViolation(path, prefix+".finite", "value must be finite")
	}
	return nil
}

// ─── Type-specific rule validators ──────────────────────────────────────────

func validateFloatRules(val float32, r *pb.FloatRules, path FieldPath) []*EnrichedViolation {
	vs := validateNumeric(val, r.Const, r.Lt, r.Lte, r.Gt, r.Gte, r.In, r.NotIn, "float", path)
	if r.Finite {
		if v := checkFinite(float64(val), "float", path); v != nil {
			vs = append(vs, v)
		}
	}
	return vs
}

func validateDoubleRules(val float64, r *pb.DoubleRules, path FieldPath) []*EnrichedViolation {
	vs := validateNumeric(val, r.Const, r.Lt, r.Lte, r.Gt, r.Gte, r.In, r.NotIn, "double", path)
	if r.Finite {
		if v := checkFinite(val, "double", path); v != nil {
			vs = append(vs, v)
		}
	}
	return vs
}

func validateInt32Rules(val int32, r *pb.Int32Rules, path FieldPath) []*EnrichedViolation {
	return validateNumeric(val, r.Const, r.Lt, r.Lte, r.Gt, r.Gte, r.In, r.NotIn, "int32", path)
}

func validateInt64Rules(val int64, r *pb.Int64Rules, path FieldPath) []*EnrichedViolation {
	return validateNumeric(val, r.Const, r.Lt, r.Lte, r.Gt, r.Gte, r.In, r.NotIn, "int64", path)
}

func validateUint32Rules(val uint32, r *pb.UInt32Rules, path FieldPath) []*EnrichedViolation {
	return validateNumeric(val, r.Const, r.Lt, r.Lte, r.Gt, r.Gte, r.In, r.NotIn, "uint32", path)
}

func validateUint64Rules(val uint64, r *pb.UInt64Rules, path FieldPath) []*EnrichedViolation {
	return validateNumeric(val, r.Const, r.Lt, r.Lte, r.Gt, r.Gte, r.In, r.NotIn, "uint64", path)
}

func validateBoolRules(val bool, r *pb.BoolRules, path FieldPath) []*EnrichedViolation {
	if r.Const != nil && val != *r.Const {
		return []*EnrichedViolation{newViolationP(path, "bool.const", params("const", *r.Const), "value must be %v", *r.Const)}
	}
	return nil
}

// ─── String rules ───────────────────────────────────────────────────────────

var regexCache sync.Map // pattern string -> *regexp.Regexp

func getRegexp(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

func validateStringRules(val string, r *pb.StringRules, path FieldPath) []*EnrichedViolation {
	var vs []*EnrichedViolation
	add := func(id string, p map[string]any, msg string, args ...any) {
		vs = append(vs, newViolationP(path, id, p, msg, args...))
	}

	if r.Const != nil && val != *r.Const {
		add("string.const", params("const", *r.Const), "value must equal %q", *r.Const)
	}

	runeLen := uint64(utf8.RuneCountInString(val))
	if r.Len != nil && runeLen != *r.Len {
		add("string.len", params("len", *r.Len), "value length must be exactly %d characters", *r.Len)
	}
	if r.MinLen != nil && runeLen < *r.MinLen {
		add("string.min_len", params("min_len", *r.MinLen), "value length must be at least %d characters", *r.MinLen)
	}
	if r.MaxLen != nil && runeLen > *r.MaxLen {
		add("string.max_len", params("max_len", *r.MaxLen), "value length must be at most %d characters", *r.MaxLen)
	}

	byteLen := uint64(len(val))
	if r.LenBytes != nil && byteLen != *r.LenBytes {
		add("string.len_bytes", params("len_bytes", *r.LenBytes), "value byte length must be exactly %d", *r.LenBytes)
	}
	if r.MinBytes != nil && byteLen < *r.MinBytes {
		add("string.min_bytes", params("min_bytes", *r.MinBytes), "value byte length must be at least %d", *r.MinBytes)
	}
	if r.MaxBytes != nil && byteLen > *r.MaxBytes {
		add("string.max_bytes", params("max_bytes", *r.MaxBytes), "value byte length must be at most %d", *r.MaxBytes)
	}

	if r.Pattern != nil {
		re, err := getRegexp(*r.Pattern)
		if err != nil {
			add("string.pattern", params("pattern", *r.Pattern), "invalid regex pattern %q: %v", *r.Pattern, err)
		} else if !re.MatchString(val) {
			add("string.pattern", params("pattern", *r.Pattern), "value must match pattern %q", *r.Pattern)
		}
	}

	if r.Prefix != nil && !strings.HasPrefix(val, *r.Prefix) {
		add("string.prefix", params("prefix", *r.Prefix), "value must have prefix %q", *r.Prefix)
	}
	if r.Suffix != nil && !strings.HasSuffix(val, *r.Suffix) {
		add("string.suffix", params("suffix", *r.Suffix), "value must have suffix %q", *r.Suffix)
	}
	if r.Contains != nil && !strings.Contains(val, *r.Contains) {
		add("string.contains", params("contains", *r.Contains), "value must contain %q", *r.Contains)
	}
	if r.NotContains != nil && strings.Contains(val, *r.NotContains) {
		add("string.not_contains", params("not_contains", *r.NotContains), "value must not contain %q", *r.NotContains)
	}

	if v := checkIn(val, r.In, "string", path); v != nil {
		vs = append(vs, v)
	}
	if v := checkNotIn(val, r.NotIn, "string", path); v != nil {
		vs = append(vs, v)
	}

	// Well-known format validators.
	switch wk := r.GetWellKnown().(type) {
	case *pb.StringRules_Email:
		if wk.Email {
			if _, err := mail.ParseAddress(val); err != nil {
				add("string.email", nil, "value must be a valid email address")
			}
		}
	case *pb.StringRules_Hostname:
		if wk.Hostname && !isValidHostname(val) {
			add("string.hostname", nil, "value must be a valid hostname")
		}
	case *pb.StringRules_Ip:
		if wk.Ip && net.ParseIP(val) == nil {
			add("string.ip", nil, "value must be a valid IP address")
		}
	case *pb.StringRules_Ipv4:
		if wk.Ipv4 {
			ip := net.ParseIP(val)
			if ip == nil || ip.To4() == nil {
				add("string.ipv4", nil, "value must be a valid IPv4 address")
			}
		}
	case *pb.StringRules_Ipv6:
		if wk.Ipv6 {
			ip := net.ParseIP(val)
			if ip == nil || ip.To4() != nil {
				add("string.ipv6", nil, "value must be a valid IPv6 address")
			}
		}
	case *pb.StringRules_Uri:
		if wk.Uri {
			u, err := url.ParseRequestURI(val)
			if err != nil || u.Scheme == "" {
				add("string.uri", nil, "value must be a valid absolute URI")
			}
		}
	case *pb.StringRules_UriRef:
		if wk.UriRef {
			if _, err := url.Parse(val); err != nil {
				add("string.uri_ref", nil, "value must be a valid URI reference")
			}
		}
	case *pb.StringRules_Uuid:
		if wk.Uuid && !isValidUUID(val) {
			add("string.uuid", nil, "value must be a valid UUID")
		}
	}

	return vs
}

// ─── Bytes rules ────────────────────────────────────────────────────────────

func validateBytesRules(val []byte, r *pb.BytesRules, path FieldPath) []*EnrichedViolation {
	var vs []*EnrichedViolation
	add := func(id string, p map[string]any, msg string, args ...any) {
		vs = append(vs, newViolationP(path, id, p, msg, args...))
	}

	if r.Const != nil && !bytes.Equal(val, r.Const) {
		add("bytes.const", params("const", r.Const), "value must equal the specified bytes")
	}

	bLen := uint64(len(val))
	if r.Len != nil && bLen != *r.Len {
		add("bytes.len", params("len", *r.Len), "value length must be exactly %d bytes", *r.Len)
	}
	if r.MinLen != nil && bLen < *r.MinLen {
		add("bytes.min_len", params("min_len", *r.MinLen), "value length must be at least %d bytes", *r.MinLen)
	}
	if r.MaxLen != nil && bLen > *r.MaxLen {
		add("bytes.max_len", params("max_len", *r.MaxLen), "value length must be at most %d bytes", *r.MaxLen)
	}
	if r.Prefix != nil && !bytes.HasPrefix(val, r.Prefix) {
		add("bytes.prefix", params("prefix", r.Prefix), "value must have the specified prefix")
	}
	if r.Suffix != nil && !bytes.HasSuffix(val, r.Suffix) {
		add("bytes.suffix", params("suffix", r.Suffix), "value must have the specified suffix")
	}
	if r.Contains != nil && !bytes.Contains(val, r.Contains) {
		add("bytes.contains", params("contains", r.Contains), "value must contain the specified bytes")
	}

	// []byte isn't comparable, so we can't reuse the generic checkIn/checkNotIn;
	// these inline loops use bytes.Equal instead.
	if len(r.In) > 0 {
		matched := false
		for _, a := range r.In {
			if bytes.Equal(val, a) {
				matched = true
				break
			}
		}
		if !matched {
			add("bytes.in", params("in", r.In), "value must be one of the allowed byte sequences")
		}
	}
	for _, d := range r.NotIn {
		if bytes.Equal(val, d) {
			add("bytes.not_in", params("not_in", r.NotIn), "value must not be one of the disallowed byte sequences")
			break
		}
	}

	switch wk := r.GetWellKnown().(type) {
	case *pb.BytesRules_Ip:
		if wk.Ip && len(val) != net.IPv4len && len(val) != net.IPv6len {
			add("bytes.ip", nil, "value must be a valid IP address (4 or 16 bytes)")
		}
	case *pb.BytesRules_Ipv4:
		if wk.Ipv4 && (len(val) != 4 || net.IP(val).To4() == nil) {
			add("bytes.ipv4", nil, "value must be a valid IPv4 address (4 bytes)")
		}
	case *pb.BytesRules_Ipv6:
		if wk.Ipv6 && len(val) != 16 {
			add("bytes.ipv6", nil, "value must be a valid IPv6 address (16 bytes)")
		}
	}

	return vs
}

// ─── Enum rules ─────────────────────────────────────────────────────────────

func validateEnumRules(val int32, r *pb.EnumRules, path FieldPath, definedValues map[int32]bool) []*EnrichedViolation {
	var vs []*EnrichedViolation

	if r.Const != nil && val != *r.Const {
		vs = append(vs, newViolationP(path, "enum.const", params("const", *r.Const), "value must equal %d", *r.Const))
	}
	if r.DefinedOnly && !definedValues[val] {
		vs = append(vs, newViolation(path, "enum.defined_only", "value must be a defined enum value"))
	}
	if v := checkIn(val, r.In, "enum", path); v != nil {
		vs = append(vs, v)
	}
	if v := checkNotIn(val, r.NotIn, "enum", path); v != nil {
		vs = append(vs, v)
	}

	return vs
}

// ─── Format helpers ─────────────────────────────────────────────────────────

var hostnameRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	return hostnameRegex.MatchString(s)
}

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool {
	return uuidRegex.MatchString(s)
}

// ─── Repeated / Map helpers ─────────────────────────────────────────────────

func validateRepeatedRules(listLen int, r *pb.RepeatedRules, path FieldPath) []*EnrichedViolation {
	var vs []*EnrichedViolation
	n := uint64(listLen)

	if r.MinItems != nil && n < *r.MinItems {
		vs = append(vs, newViolationP(path, "repeated.min_items", params("min_items", *r.MinItems), "list must have at least %d items", *r.MinItems))
	}
	if r.MaxItems != nil && n > *r.MaxItems {
		vs = append(vs, newViolationP(path, "repeated.max_items", params("max_items", *r.MaxItems), "list must have at most %d items", *r.MaxItems))
	}
	// Uniqueness is checked in the main validator where we have access to element values.
	return vs
}

func validateMapRules(mapLen int, r *pb.MapRules, path FieldPath) []*EnrichedViolation {
	var vs []*EnrichedViolation
	n := uint64(mapLen)

	if r.MinPairs != nil && n < *r.MinPairs {
		vs = append(vs, newViolationP(path, "map.min_pairs", params("min_pairs", *r.MinPairs), "map must have at least %d entries", *r.MinPairs))
	}
	if r.MaxPairs != nil && n > *r.MaxPairs {
		vs = append(vs, newViolationP(path, "map.max_pairs", params("max_pairs", *r.MaxPairs), "map must have at most %d entries", *r.MaxPairs))
	}
	return vs
}

// uniqueKey returns a string representation of a protoreflect.Value for uniqueness checking.
func uniqueKey(v interface{}) string {
	return fmt.Sprintf("%v", v)
}
