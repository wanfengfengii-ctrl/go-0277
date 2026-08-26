package httpapi

import (
	"net/http"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/task"
)

// asDomain converts an arbitrary error into a domain error for the stable
// error envelope.
func asDomain(err error) *domain.Error {
	if de, ok := err.(*domain.Error); ok {
		return de
	}
	return domain.NewError(domain.ErrOperationContentConflict, "", 0).
		AddReason("", "", 0, "", domain.ErrOperationContentConflict, err.Error())
}

type commandBase struct {
	OperationID     string `json:"operation_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		GrainType     string          `json:"grain_type"`
		StackHeightDm int64           `json:"stack_height_dm"`
		Summary       catalog.Summary `json:"summary"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.LockTask(r.Context(), body.OperationID, body.ExpectedVersion, number, app.LockRequest{
		GrainType:     catalog.GrainType(body.GrainType),
		StackHeightDm: body.StackHeightDm,
		Summary:       body.Summary,
	})
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleStartApplication(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		Plans []struct {
			ZoneCode  string `json:"zone_code"`
			BatchCode string `json:"batch_code"`
			MassMg    int64  `json:"mass_mg"`
		} `json:"plans"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	plans := make([]app.ApplicationPlan, 0, len(body.Plans))
	for _, p := range body.Plans {
		plans = append(plans, app.ApplicationPlan{ZoneCode: p.ZoneCode, BatchCode: p.BatchCode, MassMg: p.MassMg})
	}
	t, err := s.app.StartApplication(r.Context(), body.OperationID, body.ExpectedVersion, number, plans)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleRecordApplication(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		ZoneCode  string `json:"zone_code"`
		BatchCode string `json:"batch_code"`
		MassMg    int64  `json:"mass_mg"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.RecordApplication(r.Context(), body.OperationID, body.ExpectedVersion, number, body.ZoneCode, body.BatchCode, body.MassMg)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleSwitchCirculation(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		CircuitCode string `json:"circuit_code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.SwitchCirculation(r.Context(), body.OperationID, body.ExpectedVersion, number, body.CircuitCode)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleMeasurements(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		Measurements []struct {
			PointCode     string `json:"point_code"`
			LogicalSlot   int64  `json:"logical_slot"`
			Concentration int64  `json:"concentration"`
			Sequence      int64  `json:"sequence"`
		} `json:"measurements"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	inputs := make([]app.MeasurementInput, 0, len(body.Measurements))
	for _, m := range body.Measurements {
		inputs = append(inputs, app.MeasurementInput{PointCode: m.PointCode, LogicalSlot: m.LogicalSlot, Concentration: m.Concentration, Sequence: m.Sequence})
	}
	t, err := s.app.SubmitMeasurements(r.Context(), body.OperationID, body.ExpectedVersion, number, inputs)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleSupplement(w http.ResponseWriter, r *http.Request, number string) {
	var body commandBase
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.CreateSupplement(r.Context(), body.OperationID, body.ExpectedVersion, number)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCompleteSupplement(w http.ResponseWriter, r *http.Request, number string) {
	var body commandBase
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.CompleteSupplement(r.Context(), body.OperationID, body.ExpectedVersion, number)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleStartVentilation(w http.ResponseWriter, r *http.Request, number string) {
	var body commandBase
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.StartVentilation(r.Context(), body.OperationID, body.ExpectedVersion, number)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleLeak(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		SourceCode    string `json:"source_code"`
		MeasuredValue int64  `json:"measured_value"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.ReportLeak(r.Context(), body.OperationID, body.ExpectedVersion, number, app.LeakInput{SourceCode: body.SourceCode, MeasuredValue: body.MeasuredValue})
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleResolveLeak(w http.ResponseWriter, r *http.Request, number string) {
	var body commandBase
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.ResolveLeak(r.Context(), body.OperationID, body.ExpectedVersion, number)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleVentilation(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		Samples []struct {
			PointCode     string `json:"point_code"`
			LogicalSlot   int64  `json:"logical_slot"`
			Concentration int64  `json:"concentration"`
		} `json:"samples"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	samples := make([]app.VentilationInput, 0, len(body.Samples))
	for _, s := range body.Samples {
		samples = append(samples, app.VentilationInput{PointCode: s.PointCode, LogicalSlot: s.LogicalSlot, Concentration: s.Concentration})
	}
	t, err := s.app.SubmitVentilation(r.Context(), body.OperationID, body.ExpectedVersion, number, samples)
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		ReviewerID  string             `json:"reviewer_id"`
		QualifiedAt domain.LogicalTime `json:"qualified_at"`
		Approved    bool               `json:"approved"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.SubmitReview(r.Context(), body.OperationID, body.ExpectedVersion, number, app.ReviewInput{ReviewerID: body.ReviewerID, QualifiedAt: body.QualifiedAt, Approved: body.Approved})
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request, number string) {
	var body struct {
		commandBase
		Kind string `json:"kind"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, err := s.app.Terminal(r.Context(), body.OperationID, body.ExpectedVersion, number, task.TerminalKind(body.Kind))
	if err != nil {
		writeError(w, asDomain(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request, number string) {
	v, err := s.app.GetCoverage(r.Context(), number)
	if err != nil {
		writeInternal(w, "")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request, number string) {
	v, err := s.app.GetLedger(r.Context(), number)
	if err != nil {
		writeInternal(w, "")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleClosure(w http.ResponseWriter, r *http.Request, number string) {
	v, err := s.app.GetLeakClosure(r.Context(), number)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no closure"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleLeaks(w http.ResponseWriter, r *http.Request, number string) {
	v, err := s.app.ListLeaks(r.Context(), number)
	if err != nil {
		writeInternal(w, "")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request, number string) {
	v, err := s.app.ListReviews(r.Context(), number)
	if err != nil {
		writeInternal(w, "")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, number string) {
	v, err := s.app.ListEvents(r.Context(), number)
	if err != nil {
		writeInternal(w, "")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleDeviceCalls(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			TaskNumber  string `json:"task_number"`
			DeviceCode  string `json:"device_code"`
			Kind        string `json:"kind"`
			MaxAttempts int64  `json:"max_attempts"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		c, err := s.app.ScheduleDeviceCall(r.Context(), body.TaskNumber, body.DeviceCode, body.Kind, body.MaxAttempts)
		if err != nil {
			writeError(w, asDomain(err))
			return
		}
		writeJSON(w, http.StatusCreated, c)
	case http.MethodGet:
		taskNumber := r.URL.Query().Get("task")
		if taskNumber == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task query required"})
			return
		}
		calls, err := s.app.ListDeviceCalls(r.Context(), taskNumber)
		if err != nil {
			writeInternal(w, "")
			return
		}
		writeJSON(w, http.StatusOK, calls)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleRunDeviceCalls(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Now domain.LogicalTime `json:"now"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	calls, err := s.app.RunDueDeviceCalls(r.Context(), body.Now)
	if err != nil {
		writeInternal(w, "")
		return
	}
	writeJSON(w, http.StatusOK, calls)
}
