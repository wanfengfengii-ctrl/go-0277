package domain

import "strings"

// NewError builds a stable business error with the given code, operation id
// and aggregate version. Reasons must be added by the caller and are sorted
// before the error is serialised.
func NewError(code ErrorCode, operationID string, aggregateVersion int64) *Error {
	return &Error{
		Code:             code,
		OperationID:      operationID,
		AggregateVersion: aggregateVersion,
	}
}

// AddReason appends a reason with a full stable sort key.
func (e *Error) AddReason(warehouse, zone string, slot int64, point string, code ErrorCode, message string) *Error {
	e.Reasons = append(e.Reasons, Reason{
		WarehouseCode: warehouse,
		ZoneCode:      zone,
		LogicalSlot:   slot,
		PointCode:     point,
		Code:          code,
		Message:       message,
	})
	return e
}

// Sorted returns a copy of the reasons in their mandated ascending order.
func (e *Error) Sorted() []Reason {
	out := append([]Reason(nil), e.Reasons...)
	SortReasons(out)
	return out
}

// JoinMessages flattens the sorted reason messages into a single readable
// string, used by audit payloads and logs.
func (e *Error) JoinMessages() string {
	parts := make([]string, 0, len(e.Reasons))
	for _, r := range e.Sorted() {
		parts = append(parts, r.Message)
	}
	return strings.Join(parts, "; ")
}
