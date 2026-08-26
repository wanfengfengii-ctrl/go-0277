package app_test

import (
	"context"
	"testing"

	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/lease"
	"granary-phosphine-fumigation-closure/internal/store"
)

func TestModel_DuplicateDeviceCallSchedulingPreservesLifecycle(t *testing.T) {
	tests := []struct {
		name        string
		run         bool
		outcome     lease.Outcome
		wantAttempt int64
		wantResult  string
		wantFailure string
		wantDone    bool
	}{
		{name: "pending call", outcome: lease.OutcomeSuccess},
		{name: "failed call awaiting retry", run: true, outcome: lease.OutcomeTimeout, wantAttempt: 1, wantResult: string(lease.OutcomeTimeout), wantFailure: string(lease.OutcomeTimeout)},
		{name: "completed call", run: true, outcome: lease.OutcomeSuccess, wantAttempt: 1, wantResult: string(lease.OutcomeSuccess), wantDone: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestApp(t)
			ctx := context.Background()
			tsk := createAndLock(t, a)
			a.Devices = lease.NewScriptedAdapter(map[string][]lease.Outcome{
				"FAN-1": {tc.outcome},
			})

			if _, err := a.ScheduleDeviceCall(ctx, tsk.Number, "FAN-1", "FAN_CIRCUIT", 3); err != nil {
				t.Fatalf("first schedule failed: %v", err)
			}
			if tc.run {
				if _, err := a.RunDueDeviceCalls(ctx, 1); err != nil {
					t.Fatalf("run scheduled call: %v", err)
				}
			}

			calls, err := a.ListDeviceCalls(ctx, tsk.Number)
			if err != nil || len(calls) != 1 {
				t.Fatalf("calls before duplicate schedule = %+v, err = %v", calls, err)
			}
			before := calls[0]
			if before.Attempts != tc.wantAttempt || before.Result != tc.wantResult || before.FailureKind != tc.wantFailure || before.Completed != tc.wantDone {
				t.Fatalf("unexpected lifecycle before duplicate schedule: %+v", before)
			}

			if _, err := a.ScheduleDeviceCall(ctx, tsk.Number, "FAN-1", "FAN_CIRCUIT", 3); err != nil {
				if de, ok := err.(*domain.Error); !ok || de.Code != domain.ErrOperationContentConflict {
					t.Fatalf("duplicate schedule returned non-idempotency error: %v", err)
				}
			}

			calls, err = a.ListDeviceCalls(ctx, tsk.Number)
			if err != nil || len(calls) != 1 {
				t.Fatalf("calls after duplicate schedule = %+v, err = %v", calls, err)
			}
			after := calls[0]
			beforeLifecycle := store.DeviceCall{
				Attempts: before.Attempts, NextAt: before.NextAt, Result: before.Result,
				FailureKind: before.FailureKind, Completed: before.Completed,
			}
			afterLifecycle := store.DeviceCall{
				Attempts: after.Attempts, NextAt: after.NextAt, Result: after.Result,
				FailureKind: after.FailureKind, Completed: after.Completed,
			}
			if afterLifecycle != beforeLifecycle {
				t.Fatalf("duplicate schedule changed lifecycle: before=%+v after=%+v", beforeLifecycle, afterLifecycle)
			}

			if tc.wantDone {
				due, err := a.RunDueDeviceCalls(ctx, 100)
				if err != nil {
					t.Fatalf("check completed call queue: %v", err)
				}
				if len(due) != 0 {
					t.Fatalf("completed call was queued again: %+v", due)
				}
			}
		})
	}
}
