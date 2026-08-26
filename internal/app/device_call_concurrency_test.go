package app_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/httpapi"
	"granary-phosphine-fumigation-closure/internal/lease"
)

type deviceCallRecordingAdapter struct {
	calls         atomic.Int64
	mu            sync.Mutex
	outcomes      []lease.Outcome
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
}

func (a *deviceCallRecordingAdapter) Run(_ context.Context, _, _ string) (lease.Outcome, error) {
	n := a.calls.Add(1)
	if n == 1 && a.firstEntered != nil {
		close(a.firstEntered)
		<-a.releaseFirst
	}
	if n == 2 && a.secondEntered != nil {
		close(a.secondEntered)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.outcomes) == 0 {
		return lease.OutcomeSuccess, nil
	}
	i := int(n - 1)
	if i >= len(a.outcomes) {
		i = len(a.outcomes) - 1
	}
	return a.outcomes[i], nil
}

func TestModel_DeviceCallRetryExecution(t *testing.T) {
	postRun := func(handler http.Handler, now int64) *httptest.ResponseRecorder {
		t.Helper()
		body := bytes.NewBufferString(`{"now":` + strconv.FormatInt(now, 10) + `}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/device-calls/run", body)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "concurrent requests claim one attempt once",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				tsk := createAndLock(t, a)
				adapter := &deviceCallRecordingAdapter{
					outcomes:      []lease.Outcome{lease.OutcomeSuccess},
					firstEntered:  make(chan struct{}),
					secondEntered: make(chan struct{}),
					releaseFirst:  make(chan struct{}),
				}
				a.Devices = adapter
				if _, err := a.ScheduleDeviceCall(context.Background(), tsk.Number, "FAN-1", "FAN_CIRCUIT", 3); err != nil {
					t.Fatalf("schedule device call: %v", err)
				}

				handler := httpapi.NewServer(a, nil).Handler()
				responses := make(chan *httptest.ResponseRecorder, 2)
				go func() { responses <- postRun(handler, 1) }()
				select {
				case <-adapter.firstEntered:
				case <-time.After(2 * time.Second):
					t.Fatal("first request did not reach the adapter")
				}

				go func() { responses <- postRun(handler, 1) }()
				var release sync.Once
				remaining := 2
				select {
				case <-adapter.secondEntered:
					release.Do(func() { close(adapter.releaseFirst) })
				case response := <-responses:
					remaining--
					if response.Code != http.StatusOK {
						t.Errorf("second response status = %d, want 200", response.Code)
					}
					release.Do(func() { close(adapter.releaseFirst) })
				}

				for i := 0; i < remaining; i++ {
					select {
					case response := <-responses:
						if response.Code != http.StatusOK {
							t.Errorf("response status = %d, want 200", response.Code)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("concurrent run request did not finish")
					}
				}

				if got := adapter.calls.Load(); got != 1 {
					t.Fatalf("Adapter.Run calls = %d, want 1 for the same DeviceCall attempt", got)
				}
				calls, err := a.ListDeviceCalls(context.Background(), tsk.Number)
				if err != nil {
					t.Fatalf("list device calls: %v", err)
				}
				if len(calls) != 1 || !calls[0].Completed || calls[0].Attempts != 1 {
					t.Fatalf("persisted call = %+v, want one completed attempt", calls)
				}
			},
		},
		{
			name: "failed attempt advances and later success completes",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				tsk := createAndLock(t, a)
				adapter := &deviceCallRecordingAdapter{outcomes: []lease.Outcome{lease.OutcomeTimeout, lease.OutcomeSuccess}}
				a.Devices = adapter
				if _, err := a.ScheduleDeviceCall(context.Background(), tsk.Number, "FAN-1", "FAN_CIRCUIT", 3); err != nil {
					t.Fatalf("schedule device call: %v", err)
				}
				handler := httpapi.NewServer(a, nil).Handler()

				for _, now := range []int64{1, 1, 2, 100} {
					if got := postRun(handler, now).Code; got != http.StatusOK {
						t.Fatalf("run at %d status = %d, want 200", now, got)
					}
				}
				calls, err := a.ListDeviceCalls(context.Background(), tsk.Number)
				if err != nil {
					t.Fatalf("list device calls: %v", err)
				}
				if adapter.calls.Load() != 2 || len(calls) != 1 || calls[0].Attempts != 2 || calls[0].NextAt != 2 || !calls[0].Completed {
					t.Fatalf("retry state = %+v, adapter calls = %d; want completed after attempts at 1 and 2", calls, adapter.calls.Load())
				}
			},
		},
		{
			name: "attempts beyond maximum risk isolate the task",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				tsk := createAndLock(t, a)
				adapter := &deviceCallRecordingAdapter{outcomes: []lease.Outcome{lease.OutcomeTimeout}}
				a.Devices = adapter
				if _, err := a.ScheduleDeviceCall(context.Background(), tsk.Number, "FAN-1", "FAN_CIRCUIT", 1); err != nil {
					t.Fatalf("schedule device call: %v", err)
				}
				handler := httpapi.NewServer(a, nil).Handler()
				for _, now := range []int64{1, 2} {
					if got := postRun(handler, now).Code; got != http.StatusOK {
						t.Fatalf("run at %d status = %d, want 200", now, got)
					}
				}
				got, err := a.GetTask(context.Background(), tsk.Number)
				if err != nil {
					t.Fatalf("get task: %v", err)
				}
				if got.Status != domain.StatusRiskIsolated {
					t.Fatalf("task status = %s, want %s", got.Status, domain.StatusRiskIsolated)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
