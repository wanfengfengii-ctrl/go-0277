package app_test

import (
	"context"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_StartApplicationUsesLockedDeviceSnapshot(t *testing.T) {
	tests := []struct {
		name              string
		updateBeforeFirst bool
	}{
		{name: "directory updated before either application", updateBeforeFirst: true},
		{name: "directory updated between applications", updateBeforeFirst: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := newTestApp(t)
			ctx := context.Background()
			firstLocked := createAndLock(t, a)
			secondLocked := createAndLock(t, a)

			updated := standardWarehouse()
			updated.StructureVersion++
			updated.Devices = []catalog.Device{
				{Code: "CAGE-NEW", Warehouse: "WH-01", Kind: catalog.DeviceDosingCage},
				{Code: "FAN-NEW", Warehouse: "WH-01", Kind: catalog.DeviceFanCircuit},
				{Code: "SL-NEW", Warehouse: "WH-01", Kind: catalog.DeviceSamplingLine},
			}
			updateDirectory := func() {
				t.Helper()
				if err := a.RegisterWarehouse(ctx, updated); err != nil {
					t.Fatalf("update warehouse directory: %v", err)
				}
			}
			if tt.updateBeforeFirst {
				updateDirectory()
			}

			plans := []app.ApplicationPlan{{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500}}
			first, err := a.StartApplication(ctx, "apply-first", firstLocked.Version, firstLocked.Number, plans)
			if err != nil {
				t.Fatalf("first application: %v", err)
			}
			if first.Status != domain.StatusApplying {
				t.Fatalf("first status = %s, want APPLYING", first.Status)
			}
			if !tt.updateBeforeFirst {
				updateDirectory()
			}

			retried, err := a.StartApplication(ctx, "apply-first", firstLocked.Version, firstLocked.Number, plans)
			if err != nil {
				t.Fatalf("idempotent retry: %v", err)
			}
			if retried.Version != first.Version {
				t.Errorf("idempotent retry version = %d, want %d", retried.Version, first.Version)
			}

			_, err = a.StartApplication(ctx, "apply-second", secondLocked.Version, secondLocked.Number, plans)
			if de, ok := err.(*domain.Error); !ok || de.Code != domain.ErrResourceLeaseConflict {
				t.Errorf("second application error = %v, want RESOURCE_LEASE_CONFLICT", err)
			}

			second, err := a.GetTask(ctx, secondLocked.Number)
			if err != nil {
				t.Fatalf("get second task: %v", err)
			}
			if second.Status != domain.StatusAirtightChecking || second.Version != secondLocked.Version {
				t.Errorf("second task changed after conflict: status=%s version=%d", second.Status, second.Version)
			}
			ledger, err := a.GetLedger(ctx, secondLocked.Number)
			if err != nil {
				t.Fatalf("get second ledger: %v", err)
			}
			if len(ledger) != 0 {
				t.Errorf("second task created %d ledger entries after conflict, want 0", len(ledger))
			}
			batches, err := a.ListBatches(ctx)
			if err != nil {
				t.Fatalf("list batches: %v", err)
			}
			if len(batches) != 1 || batches[0].AvailableMg != 99500 || batches[0].ReservedMg != 500 {
				t.Errorf("batch changed by rejected or retried application: %+v", batches)
			}

			leases, err := a.ListLeases(ctx)
			if err != nil {
				t.Fatalf("list leases: %v", err)
			}
			lockedCodes := make(map[string]bool, len(firstLocked.Snapshot.Devices))
			for _, device := range firstLocked.Snapshot.Devices {
				lockedCodes[device.Code] = true
			}
			if len(leases) != len(lockedCodes) {
				t.Errorf("lease count = %d, want %d locked resources", len(leases), len(lockedCodes))
			}
			for _, resourceLease := range leases {
				if !lockedCodes[resourceLease.ResourceCode] {
					t.Errorf("lease acquired from updated directory: %s", resourceLease.ResourceCode)
				}
				if resourceLease.TaskNumber != firstLocked.Number {
					t.Errorf("lease %s owner = %s, want %s", resourceLease.ResourceCode, resourceLease.TaskNumber, firstLocked.Number)
				}
			}
		})
	}
}
