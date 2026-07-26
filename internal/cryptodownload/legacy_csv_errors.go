package cryptodownload

import "fmt"

type LegacyCSVCorruptionReason string

const (
	LegacyCSVReasonUnparseable     LegacyCSVCorruptionReason = "unparseable"
	LegacyCSVReasonEmpty           LegacyCSVCorruptionReason = "empty"
	LegacyCSVReasonRepeatedHeader  LegacyCSVCorruptionReason = "repeated_header"
	LegacyCSVReasonAddressMismatch LegacyCSVCorruptionReason = "address_mismatch"
	LegacyCSVReasonInvalidTime     LegacyCSVCorruptionReason = "invalid_time"
	LegacyCSVReasonNonMonotonic    LegacyCSVCorruptionReason = "non_monotonic"
)

type LegacyCSVCorruptionError struct {
	Path    string
	Segment int
	Reason  LegacyCSVCorruptionReason
	Cause   error
}

func (e *LegacyCSVCorruptionError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("legacy CSV segment %d is %s: %s: %v", e.Segment, e.Reason, e.Path, e.Cause)
	}
	return fmt.Sprintf("legacy CSV segment %d is %s: %s", e.Segment, e.Reason, e.Path)
}

func (e *LegacyCSVCorruptionError) Unwrap() error { return e.Cause }

type LegacyCSVGapError struct {
	Kind           string
	MissingSegment int
	FoundSegment   int
}

func (e *LegacyCSVGapError) Error() string {
	return fmt.Sprintf("legacy CSV %s segment gap: missing %d before %d", e.Kind, e.MissingSegment, e.FoundSegment)
}

type LegacyCSVRangeError struct {
	Start int64
	End   int64
}

func (e *LegacyCSVRangeError) Error() string {
	return fmt.Sprintf("legacy CSV migration requires full-history range, got start=%d end=%d", e.Start, e.End)
}
