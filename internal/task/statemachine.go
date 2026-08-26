package task

import "granary-phosphine-fumigation-closure/internal/domain"

// Transition defines one legal state movement of the fumigation task
// aggregate. The full graph is fixed here so no caller can invent an
// undocumented transition.
type Transition struct {
	From domain.TaskStatus
	To   domain.TaskStatus
}

// legalTransitions is the authoritative state machine. Terminal states are
// absent from the left-hand side because they can never be left.
var legalTransitions = map[domain.TaskStatus][]domain.TaskStatus{
	domain.StatusPendingLock:      {domain.StatusAirtightChecking, domain.StatusCancelled},
	domain.StatusAirtightChecking: {domain.StatusApplying, domain.StatusCancelled},
	domain.StatusApplying:         {domain.StatusExposureMaintain, domain.StatusLeakContaining, domain.StatusCancelled},
	domain.StatusExposureMaintain: {domain.StatusSupplementing, domain.StatusLeakContaining, domain.StatusVentilating, domain.StatusCancelled},
	domain.StatusSupplementing:    {domain.StatusExposureMaintain, domain.StatusLeakContaining, domain.StatusCancelled},
	domain.StatusLeakContaining:   {domain.StatusExposureMaintain, domain.StatusSupplementing, domain.StatusCancelled},
	domain.StatusVentilating:      {domain.StatusReentryReady, domain.StatusCancelled},
	domain.StatusReentryReady:     {domain.StatusCompleted, domain.StatusRiskIsolated, domain.StatusCancelled},
}

// CanTransition reports whether moving from -> to is a legal, non-terminal
// state change.
func CanTransition(from, to domain.TaskStatus) bool {
	if from.IsTerminal() {
		return false
	}
	for _, candidate := range legalTransitions[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

// TerminalStatuses returns the three terminal kinds as task statuses, for
// loops that must treat them uniformly.
func TerminalStatuses() []domain.TaskStatus {
	return []domain.TaskStatus{domain.StatusCompleted, domain.StatusRiskIsolated, domain.StatusCancelled}
}

// StatusForTerminal maps a terminal decision kind to its task status.
func StatusForTerminal(k TerminalKind) domain.TaskStatus {
	switch k {
	case TerminalCompleted:
		return domain.StatusCompleted
	case TerminalRiskIsolated:
		return domain.StatusRiskIsolated
	case TerminalCancelled:
		return domain.StatusCancelled
	default:
		return domain.StatusCancelled
	}
}
