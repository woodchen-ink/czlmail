package imapc

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

// UIDSet is a list of UIDs (or sequence numbers). Ranges from the server are
// expanded; "*" is not allowed in parsed sets.
type UIDSet []uint32

// ParseUIDSet parses "1:3,7,9:10".
func ParseUIDSet(s string) (UIDSet, error) {
	var out UIDSet
	for part := range strings.SplitSeq(s, ",") {
		lo, hi, isRange := strings.Cut(part, ":")
		a, err := strconv.ParseUint(lo, 10, 32)
		if err != nil {
			return nil, errors.New("imapc: bad uid set " + s)
		}
		b := a
		if isRange {
			if b, err = strconv.ParseUint(hi, 10, 32); err != nil {
				return nil, errors.New("imapc: bad uid set " + s)
			}
		}
		if a > b {
			a, b = b, a
		}
		if b-a > 10_000_000 {
			return nil, errors.New("imapc: uid range too large")
		}
		for u := a; u <= b; u++ {
			out = append(out, uint32(u))
		}
	}
	return out, nil
}

// Compact renders the set with ranges, sorted: "1:3,7".
func (s UIDSet) Compact() string {
	if len(s) == 0 {
		return ""
	}
	u := slices.Clone(s)
	slices.Sort(u)
	u = slices.Compact(u)
	var sb strings.Builder
	start, prev := u[0], u[0]
	flush := func() {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatUint(uint64(start), 10))
		if prev != start {
			sb.WriteByte(':')
			sb.WriteString(strconv.FormatUint(uint64(prev), 10))
		}
	}
	for _, x := range u[1:] {
		if x == prev+1 {
			prev = x
			continue
		}
		flush()
		start, prev = x, x
	}
	flush()
	return sb.String()
}

// Range is "lo:hi" or "lo:*" when hi is 0.
func Range(lo, hi uint32) string {
	if hi == 0 {
		return strconv.FormatUint(uint64(lo), 10) + ":*"
	}
	return strconv.FormatUint(uint64(lo), 10) + ":" + strconv.FormatUint(uint64(hi), 10)
}
