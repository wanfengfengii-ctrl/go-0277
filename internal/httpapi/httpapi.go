// Package httpapi exposes the Go HTTP API for catalogue, task commands,
// sampling intake, device retries, coverage grid, ledger, risk graph and
// terminal interfaces, and serves the embedded frontend. The stable error
// response shape is defined here and shared by every endpoint.
package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/domain"
)

// ErrorResponse is the stable error envelope required by the failure
// boundaries: code, operation id, aggregate version and deterministically
// sorted reasons.
type ErrorResponse struct {
	Code             string           `json:"code"`
	OperationID      string           `json:"operation_id"`
	AggregateVersion int64            `json:"aggregate_version"`
	Reasons          []ReasonResponse `json:"reasons"`
}

// ReasonResponse is a single ordered reason in the error envelope.
type ReasonResponse struct {
	WarehouseCode string `json:"warehouse_code"`
	ZoneCode      string `json:"zone_code"`
	LogicalSlot   int64  `json:"logical_slot"`
	PointCode     string `json:"point_code"`
	Code          string `json:"code"`
	Message       string `json:"message"`
}

// NewErrorResponse converts a domain error into the stable wire shape,
// sorting reasons before serialisation.
func NewErrorResponse(e *domain.Error) ErrorResponse {
	reasons := append([]domain.Reason(nil), e.Reasons...)
	domain.SortReasons(reasons)
	out := ErrorResponse{
		Code:             string(e.Code),
		OperationID:      e.OperationID,
		AggregateVersion: e.AggregateVersion,
		Reasons:          make([]ReasonResponse, 0, len(reasons)),
	}
	for _, r := range reasons {
		out.Reasons = append(out.Reasons, ReasonResponse{
			WarehouseCode: r.WarehouseCode,
			ZoneCode:      r.ZoneCode,
			LogicalSlot:   r.LogicalSlot,
			PointCode:     r.PointCode,
			Code:          string(r.Code),
			Message:       r.Message,
		})
	}
	return out
}

// writeJSON serialises v with a stable JSON encoding.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError serialises a domain error as a stable error envelope.
func writeError(w http.ResponseWriter, e *domain.Error) {
	writeJSON(w, statusFor(e.Code), NewErrorResponse(e))
}

// statusFor maps a stable error code to an HTTP status.
func statusFor(code domain.ErrorCode) int {
	switch code {
	case domain.ErrTerminalAlreadyDecided,
		domain.ErrResourceLeaseConflict,
		domain.ErrOperationContentConflict,
		domain.ErrSupplementGenerationConflict,
		domain.ErrFanCircuitConflict,
		domain.ErrLeakPropagationActive:
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

// writeInternal writes a generic internal error envelope.
func writeInternal(w http.ResponseWriter, opID string) {
	writeError(w, domain.NewError(domain.ErrOperationContentConflict, opID, 0).
		AddReason("", "", 0, "", domain.ErrOperationContentConflict, "internal error"))
}

// decodeBody parses a JSON request body into v.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return false
	}
	return true
}

// Server is the HTTP handler. It is constructed with the application service
// and the embedded frontend assets.
type Server struct {
	app    *app.App
	assets fs.FS
}

// NewServer constructs the API server.
func NewServer(application *app.App, assets fs.FS) *Server {
	return &Server{app: application, assets: assets}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealth)

	mux.HandleFunc("/api/v1/warehouses", s.handleWarehouses)
	mux.HandleFunc("/api/v1/warehouses/", s.handleWarehouseByCode)
	mux.HandleFunc("/api/v1/rules", s.handleRules)
	mux.HandleFunc("/api/v1/batches", s.handleBatches)

	mux.HandleFunc("/api/v1/tasks", s.handleTasks)
	mux.HandleFunc("/api/v1/tasks/", s.handleTaskByNumber)

	mux.HandleFunc("/api/v1/device-calls", s.handleDeviceCalls)
	mux.HandleFunc("/api/v1/device-calls/run", s.handleRunDeviceCalls)

	if s.assets != nil {
		mux.Handle("/", http.FileServer(http.FS(s.assets)))
	}

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// taskNumberFromPath extracts the task number from a /api/v1/tasks/{number}/
// style path, ignoring trailing sub-resources.
func taskNumberFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/api/v1/tasks/")
	parts := strings.Split(rest, "/")
	return parts[0]
}
