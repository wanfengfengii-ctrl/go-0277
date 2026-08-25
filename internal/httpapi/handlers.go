package httpapi

import (
	"net/http"

	"granary-phosphine-fumigation-closure/internal/catalog"
)

func (s *Server) handleWarehouses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var wc catalog.Warehouse
		if !decodeBody(w, r, &wc) {
			return
		}
		if err := s.app.RegisterWarehouse(r.Context(), wc); err != nil {
			writeError(w, asDomain(err))
			return
		}
		writeJSON(w, http.StatusCreated, wc)
	case http.MethodGet:
		list, err := s.app.ListWarehouses(r.Context())
		if err != nil {
			writeInternal(w, "")
			return
		}
		writeJSON(w, http.StatusOK, list)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleWarehouseByCode(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Path[len("/api/v1/warehouses/"):]
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing warehouse code"})
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("summary") == "1" {
		sum, err := s.app.SummarizePreview(r.Context(), code)
		if err != nil {
			writeInternal(w, "")
			return
		}
		writeJSON(w, http.StatusOK, sum)
		return
	}
	wc, err := s.app.GetWarehouse(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "warehouse not found"})
		return
	}
	writeJSON(w, http.StatusOK, wc)
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var ru catalog.FumigationRule
		if !decodeBody(w, r, &ru) {
			return
		}
		if err := s.app.RegisterRule(r.Context(), ru); err != nil {
			writeError(w, asDomain(err))
			return
		}
		writeJSON(w, http.StatusCreated, ru)
	case http.MethodGet:
		list, err := s.app.ListRules(r.Context())
		if err != nil {
			writeInternal(w, "")
			return
		}
		writeJSON(w, http.StatusOK, list)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleBatches(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var b catalog.PesticideBatch
		if !decodeBody(w, r, &b) {
			return
		}
		if err := s.app.RegisterBatch(r.Context(), b); err != nil {
			writeError(w, asDomain(err))
			return
		}
		writeJSON(w, http.StatusCreated, b)
	case http.MethodGet:
		list, err := s.app.ListBatches(r.Context())
		if err != nil {
			writeInternal(w, "")
			return
		}
		writeJSON(w, http.StatusOK, list)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			WarehouseCode string `json:"warehouse_code"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		t, err := s.app.CreateTask(r.Context(), body.WarehouseCode)
		if err != nil {
			writeError(w, asDomain(err))
			return
		}
		writeJSON(w, http.StatusCreated, t)
	case http.MethodGet:
		list, err := s.app.ListTasks(r.Context())
		if err != nil {
			writeInternal(w, "")
			return
		}
		writeJSON(w, http.StatusOK, list)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleTaskByNumber dispatches task sub-resources by their path suffix.
func (s *Server) handleTaskByNumber(w http.ResponseWriter, r *http.Request) {
	number := taskNumberFromPath(r.URL.Path)
	if number == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task number"})
		return
	}
	rest := r.URL.Path[len("/api/v1/tasks/"+number):]
	if rest == "" {
		s.handleTaskGet(w, r, number)
		return
	}

	// Sub-resource (starts with "/").
	action := rest[1:]
	switch action {
	case "lock":
		s.handleLock(w, r, number)
	case "start-application":
		s.handleStartApplication(w, r, number)
	case "record-application":
		s.handleRecordApplication(w, r, number)
	case "switch-circulation":
		s.handleSwitchCirculation(w, r, number)
	case "measurements":
		s.handleMeasurements(w, r, number)
	case "supplement":
		s.handleSupplement(w, r, number)
	case "complete-supplement":
		s.handleCompleteSupplement(w, r, number)
	case "start-ventilation":
		s.handleStartVentilation(w, r, number)
	case "leak":
		s.handleLeak(w, r, number)
	case "resolve-leak":
		s.handleResolveLeak(w, r, number)
	case "ventilation":
		s.handleVentilation(w, r, number)
	case "review":
		s.handleReview(w, r, number)
	case "terminal":
		s.handleTerminal(w, r, number)
	case "coverage":
		s.handleCoverage(w, r, number)
	case "ledger":
		s.handleLedger(w, r, number)
	case "closure":
		s.handleClosure(w, r, number)
	case "leaks":
		s.handleLeaks(w, r, number)
	case "reviews":
		s.handleReviews(w, r, number)
	case "events":
		s.handleEvents(w, r, number)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown task sub-resource"})
	}
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request, number string) {
	t, err := s.app.GetTask(r.Context(), number)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}
