// Package lease implements the resource occupancy manager: mutually exclusive
// leases for dosing cages, fan circuits and sampling lines, plus the
// deterministic device-call retry scheduling used by the application layer.
package lease

import (
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/store"
)

// Manager acquires, releases and freezes resource leases inside one storage
// transaction. It never depends on wall-clock time: all durations use the
// logical clock passed in by the caller.
type Manager struct {
	repo store.LeaseRepo
	now  domain.LogicalTime
}

// NewManager binds a lease manager to a repository at a fixed logical time.
func NewManager(repo store.LeaseRepo, now domain.LogicalTime) *Manager {
	return &Manager{repo: repo, now: now}
}

// Acquire obtains an exclusive lease on a resource for a task. If the resource
// is already leased to another unexpired task it returns
// RESOURCE_LEASE_CONFLICT; a re-acquire by the same task bumps the version and
// extends the lease idempotently.
func (m *Manager) Acquire(kind, code, taskNumber string, duration domain.LogicalTime) (store.ResourceLease, domain.ErrorCode) {
	existing, err := m.repo.GetLease(nil, kind, code)
	if err == nil {
		if existing.TaskNumber != "" && existing.TaskNumber != taskNumber && existing.ExpiresAt > m.now {
			return store.ResourceLease{}, domain.ErrResourceLeaseConflict
		}
		existing.TaskNumber = taskNumber
		existing.Version++
		existing.AcquiredAt = m.now
		existing.ExpiresAt = m.now + duration
		existing.FrozenReason = ""
		if err := m.repo.SaveLease(nil, existing); err != nil {
			return store.ResourceLease{}, domain.ErrResourceLeaseConflict
		}
		return existing, domain.ErrNone
	}
	l := store.ResourceLease{
		ResourceKind: kind,
		ResourceCode: code,
		TaskNumber:   taskNumber,
		Version:      1,
		AcquiredAt:   m.now,
		ExpiresAt:    m.now + duration,
	}
	if err := m.repo.SaveLease(nil, l); err != nil {
		return store.ResourceLease{}, domain.ErrResourceLeaseConflict
	}
	return l, domain.ErrNone
}

// Freeze marks a resource as frozen for a reason (for example leak propagation
// closure). A frozen resource cannot be acquired for running.
func (m *Manager) Freeze(kind, code, reason string) domain.ErrorCode {
	existing, err := m.repo.GetLease(nil, kind, code)
	if err != nil {
		existing = store.ResourceLease{ResourceKind: kind, ResourceCode: code, Version: 1}
	}
	existing.FrozenReason = reason
	existing.Version++
	if err := m.repo.SaveLease(nil, existing); err != nil {
		return domain.ErrResourceLeaseConflict
	}
	return domain.ErrNone
}

// IsFrozen reports whether a resource is frozen.
func (m *Manager) IsFrozen(kind, code string) bool {
	existing, err := m.repo.GetLease(nil, kind, code)
	if err != nil {
		return false
	}
	return existing.FrozenReason != ""
}

// SetRunning marks a fan circuit as circulating (or not). The application layer
// uses this to enforce the mutually exclusive circuit constraint.
func (m *Manager) SetRunning(kind, code string, running bool) domain.ErrorCode {
	existing, err := m.repo.GetLease(nil, kind, code)
	if err != nil {
		existing = store.ResourceLease{ResourceKind: kind, ResourceCode: code, Version: 1}
	}
	existing.Running = running
	existing.Version++
	if err := m.repo.SaveLease(nil, existing); err != nil {
		return domain.ErrResourceLeaseConflict
	}
	return domain.ErrNone
}

// IsRunning reports whether a fan circuit is currently circulating.
func (m *Manager) IsRunning(kind, code string) bool {
	existing, err := m.repo.GetLease(nil, kind, code)
	if err != nil {
		return false
	}
	return existing.Running
}

// NextAttempt advances a failed device call to its next deterministic retry:
// attempts increments, and the next logical time advances by baseDelay times
// the attempt count. It reports whether the retry budget is exhausted.
func NextAttempt(attempts, maxAttempts, baseDelay int64, now domain.LogicalTime) (nextAttempts, nextAt int64, exhausted bool) {
	nextAttempts = attempts + 1
	if nextAttempts > maxAttempts {
		return nextAttempts, int64(now), true
	}
	nextAt = int64(now) + baseDelay*nextAttempts
	return nextAttempts, nextAt, false
}
