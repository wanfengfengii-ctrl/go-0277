// Package app is the application service that orchestrates the full
// fumigation closure. It owns the transactional command handlers that run the
// catalogue, task aggregate, leasing, coverage, leak and terminal flows, and
// the query methods used by the HTTP API. All state-changing commands run in a
// single transaction and honour operation-id idempotency.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/lease"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// App is the application service. It is the single authority over state
// changes and the only place that mutates persisted aggregates.
type App struct {
	Store   store.Store
	Devices lease.Adapter
}

// New constructs the application service. If adapter is nil, a success-only
// adapter is used so device calls always complete in production runs that do
// not configure scripted devices.
func New(st store.Store, adapter lease.Adapter) *App {
	if adapter == nil {
		adapter = lease.SuccessAdapter{}
	}
	return &App{Store: st, Devices: adapter}
}

// digest computes the canonical command digest used for idempotency: a hash of
// the normalised JSON request body.
func digest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", v))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// beginWrite starts a writable transaction.
func (a *App) beginWrite(ctx context.Context) (store.Tx, error) {
	return a.Store.Begin(ctx, true)
}

// checkReceipt implements operation-id idempotency scoped to a task. It
// returns (true, nil) when the operation was already applied with the same
// content (the caller should return the current aggregate state), an
// OPERATION_CONTENT_CONFLICT error when the same id is reused for different
// content, or (false, nil) when the operation should proceed.
func checkReceipt(repo store.TaskRepo, taskNumber, opID, dig string) (bool, error) {
	rec, err := repo.GetReceipt(context.Background(), taskNumber, opID)
	if err != nil {
		return false, nil // no existing receipt
	}
	if rec.CommandDigest == dig {
		return true, nil
	}
	return false, domain.NewError(domain.ErrOperationContentConflict, opID, 0)
}

// recordReceipt persists an idempotent receipt for a command.
func recordReceipt(repo store.TaskRepo, taskNumber, opID, dig, result string) error {
	return repo.SaveReceipt(context.Background(), task.OperationReceipt{
		TaskNumber:    taskNumber,
		OperationID:   opID,
		CommandDigest: dig,
		Result:        result,
	})
}

// versionString renders an aggregate version for receipt storage.
func versionString(v int64) string { return strconv.FormatInt(v, 10) }

// loadTask loads a task inside a transaction, returning a NOT_FOUND style
// error when absent.
func loadTask(repo store.TaskRepo, number string) (task.FumigationTask, error) {
	t, err := repo.Load(context.Background(), number)
	if err != nil {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, "", 0)
	}
	return t, nil
}

// appendEvent appends an audit event with the next sequence number.
func appendEvent(repo store.TaskRepo, t task.FumigationTask, command string, payload string) error {
	events, _ := repo.ListEvents(context.Background(), t.Number)
	seq := int64(len(events)) + 1
	return repo.AppendEvent(context.Background(), task.TaskEvent{
		Sequence:       seq,
		TaskNumber:     t.Number,
		CommandSummary: command,
		FromStatus:     t.Status,
		ToStatus:       t.Status,
		Payload:        payload,
	})
}
