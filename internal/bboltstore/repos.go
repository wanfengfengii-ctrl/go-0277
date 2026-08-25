package bboltstore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"

	"granary-phosphine-fumigation-closure/internal/arbitration"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/coverage"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// tx wraps a bbolt transaction and exposes typed repositories.
type tx struct {
	btx *bolt.Tx
}

func (t *tx) Commit() error                      { return t.btx.Commit() }
func (t *tx) Rollback() error                    { return t.btx.Rollback() }
func (t *tx) Catalog() store.CatalogRepo         { return &catalogRepo{btx: t.btx} }
func (t *tx) Tasks() store.TaskRepo              { return &taskRepo{btx: t.btx} }
func (t *tx) Leases() store.LeaseRepo            { return &leaseRepo{btx: t.btx} }
func (t *tx) Coverage() store.CoverageRepo       { return &coverageRepo{btx: t.btx} }
func (t *tx) Arbitration() store.ArbitrationRepo { return &arbitrationRepo{btx: t.btx} }

func encode(v any) ([]byte, error) { return json.Marshal(v) }
func decode(raw []byte, v any) error {
	if raw == nil {
		return fmt.Errorf("record not found")
	}
	return json.Unmarshal(raw, v)
}
func encodeInto(b *bolt.Bucket, key []byte, v any) error {
	raw, err := encode(v)
	if err != nil {
		return err
	}
	return b.Put(key, raw)
}

// nextSeq returns the next monotonic integer for a named sequence, stored in
// the meta bucket. Sequences survive restarts because they live in the same
// file.
func nextSeq(btx *bolt.Tx, name string) (int64, error) {
	mb := btx.Bucket(bucketMeta)
	key := []byte("seq:" + name)
	var v uint64
	if raw := mb.Get(key); raw != nil && len(raw) == 8 {
		v = binary.BigEndian.Uint64(raw)
	}
	v++
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	if err := mb.Put(key, buf); err != nil {
		return 0, err
	}
	return int64(v), nil
}

// ---- Catalog ----

type catalogRepo struct{ btx *bolt.Tx }

func (r *catalogRepo) SaveWarehouse(_ context.Context, w catalog.Warehouse) error {
	return encodeInto(r.btx.Bucket(bucketWarehouse), []byte(w.Code), w)
}
func (r *catalogRepo) GetWarehouse(_ context.Context, code string) (catalog.Warehouse, error) {
	var w catalog.Warehouse
	err := decode(r.btx.Bucket(bucketWarehouse).Get([]byte(code)), &w)
	return w, err
}
func (r *catalogRepo) ListWarehouses(_ context.Context) ([]catalog.Warehouse, error) {
	var out []catalog.Warehouse
	err := r.btx.Bucket(bucketWarehouse).ForEach(func(_, v []byte) error {
		var w catalog.Warehouse
		if err := decode(v, &w); err != nil {
			return err
		}
		out = append(out, w)
		return nil
	})
	return out, err
}
func (r *catalogRepo) SaveRule(_ context.Context, ru catalog.FumigationRule) error {
	return encodeInto(r.btx.Bucket(bucketRule), []byte(fmt.Sprintf("%020d", ru.Version)), ru)
}
func (r *catalogRepo) GetRule(_ context.Context, version int64) (catalog.FumigationRule, error) {
	var ru catalog.FumigationRule
	err := decode(r.btx.Bucket(bucketRule).Get([]byte(fmt.Sprintf("%020d", version))), &ru)
	return ru, err
}
func (r *catalogRepo) ListRules(_ context.Context) ([]catalog.FumigationRule, error) {
	var out []catalog.FumigationRule
	err := r.btx.Bucket(bucketRule).ForEach(func(_, v []byte) error {
		var ru catalog.FumigationRule
		if err := decode(v, &ru); err != nil {
			return err
		}
		out = append(out, ru)
		return nil
	})
	return out, err
}
func (r *catalogRepo) SaveBatch(_ context.Context, b catalog.PesticideBatch) error {
	return encodeInto(r.btx.Bucket(bucketBatch), []byte(b.Code), b)
}
func (r *catalogRepo) GetBatch(_ context.Context, code string) (catalog.PesticideBatch, error) {
	var b catalog.PesticideBatch
	err := decode(r.btx.Bucket(bucketBatch).Get([]byte(code)), &b)
	return b, err
}
func (r *catalogRepo) ListBatches(_ context.Context) ([]catalog.PesticideBatch, error) {
	var out []catalog.PesticideBatch
	err := r.btx.Bucket(bucketBatch).ForEach(func(_, v []byte) error {
		var b catalog.PesticideBatch
		if err := decode(v, &b); err != nil {
			return err
		}
		out = append(out, b)
		return nil
	})
	return out, err
}

// ---- Tasks ----

type taskRepo struct{ btx *bolt.Tx }

func (r *taskRepo) Save(_ context.Context, t task.FumigationTask) error {
	return encodeInto(r.btx.Bucket(bucketTask), []byte(t.Number), t)
}
func (r *taskRepo) Load(_ context.Context, number string) (task.FumigationTask, error) {
	var t task.FumigationTask
	err := decode(r.btx.Bucket(bucketTask).Get([]byte(number)), &t)
	return t, err
}
func (r *taskRepo) List(_ context.Context) ([]task.FumigationTask, error) {
	var out []task.FumigationTask
	err := r.btx.Bucket(bucketTask).ForEach(func(_, v []byte) error {
		var t task.FumigationTask
		if err := decode(v, &t); err != nil {
			return err
		}
		out = append(out, t)
		return nil
	})
	return out, err
}
func (r *taskRepo) AppendEvent(_ context.Context, e task.TaskEvent) error {
	key := []byte(fmt.Sprintf("%s|%020d", e.TaskNumber, e.Sequence))
	return encodeInto(r.btx.Bucket(bucketEvent), key, e)
}
func (r *taskRepo) ListEvents(_ context.Context, taskNumber string) ([]task.TaskEvent, error) {
	var out []task.TaskEvent
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketEvent).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var e task.TaskEvent
		if err := decode(v, &e); err != nil {
			return err
		}
		out = append(out, e)
		return nil
	})
	return out, err
}
func receiptKey(taskNumber, operationID string) []byte {
	return []byte(taskNumber + "|" + operationID)
}
func (r *taskRepo) SaveReceipt(_ context.Context, rec task.OperationReceipt) error {
	return encodeInto(r.btx.Bucket(bucketReceipt), receiptKey(rec.TaskNumber, rec.OperationID), rec)
}
func (r *taskRepo) GetReceipt(_ context.Context, taskNumber, operationID string) (task.OperationReceipt, error) {
	var rec task.OperationReceipt
	err := decode(r.btx.Bucket(bucketReceipt).Get(receiptKey(taskNumber, operationID)), &rec)
	return rec, err
}

// ---- Leases ----

type leaseRepo struct{ btx *bolt.Tx }

func leaseKey(kind, code string) []byte { return []byte(kind + "|" + code) }

func (r *leaseRepo) SaveLease(_ context.Context, l store.ResourceLease) error {
	return encodeInto(r.btx.Bucket(bucketLease), leaseKey(l.ResourceKind, l.ResourceCode), l)
}
func (r *leaseRepo) GetLease(_ context.Context, kind, code string) (store.ResourceLease, error) {
	var l store.ResourceLease
	err := decode(r.btx.Bucket(bucketLease).Get(leaseKey(kind, code)), &l)
	return l, err
}
func (r *leaseRepo) ListLeases(_ context.Context) ([]store.ResourceLease, error) {
	var out []store.ResourceLease
	err := r.btx.Bucket(bucketLease).ForEach(func(_, v []byte) error {
		var l store.ResourceLease
		if err := decode(v, &l); err != nil {
			return err
		}
		out = append(out, l)
		return nil
	})
	return out, err
}
func (r *leaseRepo) SaveDeviceCall(_ context.Context, c store.DeviceCall) error {
	return encodeInto(r.btx.Bucket(bucketDeviceCall), []byte(c.ID), c)
}
func (r *leaseRepo) GetDeviceCall(_ context.Context, id string) (store.DeviceCall, error) {
	var c store.DeviceCall
	err := decode(r.btx.Bucket(bucketDeviceCall).Get([]byte(id)), &c)
	return c, err
}
func (r *leaseRepo) ListDeviceCalls(_ context.Context, taskNumber string) ([]store.DeviceCall, error) {
	var out []store.DeviceCall
	err := r.btx.Bucket(bucketDeviceCall).ForEach(func(_, v []byte) error {
		var c store.DeviceCall
		if err := decode(v, &c); err != nil {
			return err
		}
		if c.TaskNumber == taskNumber {
			out = append(out, c)
		}
		return nil
	})
	return out, err
}
func (r *leaseRepo) ListDueDeviceCalls(_ context.Context, now domain.LogicalTime) ([]store.DeviceCall, error) {
	var out []store.DeviceCall
	err := r.btx.Bucket(bucketDeviceCall).ForEach(func(_, v []byte) error {
		var c store.DeviceCall
		if err := decode(v, &c); err != nil {
			return err
		}
		if !c.Completed && c.NextAt <= now {
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

// ---- Coverage ----

type coverageRepo struct{ btx *bolt.Tx }

func (r *coverageRepo) SaveMeasurement(_ context.Context, m coverage.Measurement) error {
	return encodeInto(r.btx.Bucket(bucketMeasurement), []byte(m.Key.String()), m)
}
func (r *coverageRepo) ListMeasurements(_ context.Context, taskNumber string) ([]coverage.Measurement, error) {
	var out []coverage.Measurement
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketMeasurement).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var m coverage.Measurement
		if err := decode(v, &m); err != nil {
			return err
		}
		out = append(out, m)
		return nil
	})
	return out, err
}
func (r *coverageRepo) SaveCell(_ context.Context, c coverage.CoverageCell) error {
	key := fmt.Sprintf("%s|%s|%s|%020d|%s|%d|%d", c.TaskNumber, c.WarehouseCode, c.ZoneCode, c.LogicalSlot, c.PointCode, c.Generation, c.SupplementGeneration)
	return encodeInto(r.btx.Bucket(bucketCell), []byte(key), c)
}
func (r *coverageRepo) ListCells(_ context.Context, taskNumber string) ([]coverage.CoverageCell, error) {
	var out []coverage.CoverageCell
	err := r.btx.Bucket(bucketCell).ForEach(func(_, v []byte) error {
		var c coverage.CoverageCell
		if err := decode(v, &c); err != nil {
			return err
		}
		if c.TaskNumber == taskNumber {
			out = append(out, c)
		}
		return nil
	})
	return out, err
}
func (r *coverageRepo) SaveIntegral(_ context.Context, i coverage.ExposureIntegral) error {
	seq, err := nextSeq(r.btx, "integral")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s|%020d", i.TaskNumber, seq)
	return encodeInto(r.btx.Bucket(bucketIntegral), []byte(key), i)
}
func (r *coverageRepo) ListIntegrals(_ context.Context, taskNumber string) ([]coverage.ExposureIntegral, error) {
	var out []coverage.ExposureIntegral
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketIntegral).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var i coverage.ExposureIntegral
		if err := decode(v, &i); err != nil {
			return err
		}
		out = append(out, i)
		return nil
	})
	return out, err
}
func (r *coverageRepo) SaveLedger(_ context.Context, e coverage.DoseLedgerEntry) error {
	seq, err := nextSeq(r.btx, "ledger")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s|%020d", e.TaskNumber, seq)
	return encodeInto(r.btx.Bucket(bucketLedger), []byte(key), e)
}
func (r *coverageRepo) ListLedger(_ context.Context, taskNumber string) ([]coverage.DoseLedgerEntry, error) {
	var out []coverage.DoseLedgerEntry
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketLedger).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var e coverage.DoseLedgerEntry
		if err := decode(v, &e); err != nil {
			return err
		}
		out = append(out, e)
		return nil
	})
	return out, err
}
func (r *coverageRepo) SaveApplication(_ context.Context, a coverage.ApplicationRecord) error {
	key := fmt.Sprintf("%s|%s|%s|%d", a.TaskNumber, a.BatchCode, a.ZoneCode, a.Generation)
	return encodeInto(r.btx.Bucket(bucketApplication), []byte(key), a)
}
func (r *coverageRepo) ListApplications(_ context.Context, taskNumber string) ([]coverage.ApplicationRecord, error) {
	var out []coverage.ApplicationRecord
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketApplication).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var a coverage.ApplicationRecord
		if err := decode(v, &a); err != nil {
			return err
		}
		out = append(out, a)
		return nil
	})
	return out, err
}

// ---- Arbitration ----

type arbitrationRepo struct{ btx *bolt.Tx }

func (r *arbitrationRepo) SaveLeak(_ context.Context, e arbitration.LeakEvidence) error {
	seq, err := nextSeq(r.btx, "leak")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s|%020d", e.TaskNumber, seq)
	return encodeInto(r.btx.Bucket(bucketLeak), []byte(key), e)
}
func (r *arbitrationRepo) ListLeaks(_ context.Context, taskNumber string) ([]arbitration.LeakEvidence, error) {
	var out []arbitration.LeakEvidence
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketLeak).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var e arbitration.LeakEvidence
		if err := decode(v, &e); err != nil {
			return err
		}
		out = append(out, e)
		return nil
	})
	return out, err
}
func (r *arbitrationRepo) SaveClosure(_ context.Context, c arbitration.RiskClosure) error {
	return encodeInto(r.btx.Bucket(bucketClosure), []byte(c.TaskNumber), c)
}
func (r *arbitrationRepo) GetClosure(_ context.Context, taskNumber string) (arbitration.RiskClosure, error) {
	var c arbitration.RiskClosure
	err := decode(r.btx.Bucket(bucketClosure).Get([]byte(taskNumber)), &c)
	return c, err
}
func (r *arbitrationRepo) SaveVentilation(_ context.Context, v arbitration.VentilationEvidence) error {
	key := fmt.Sprintf("%s|%s|%020d", v.TaskNumber, v.PointCode, v.LogicalSlot)
	return encodeInto(r.btx.Bucket(bucketVentilation), []byte(key), v)
}
func (r *arbitrationRepo) ListVentilation(_ context.Context, taskNumber string) ([]arbitration.VentilationEvidence, error) {
	var out []arbitration.VentilationEvidence
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketVentilation).ForEach(func(k, raw []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var ev arbitration.VentilationEvidence
		if err := decode(raw, &ev); err != nil {
			return err
		}
		out = append(out, ev)
		return nil
	})
	return out, err
}
func (r *arbitrationRepo) SaveReview(_ context.Context, rv arbitration.ReentryReview) error {
	key := fmt.Sprintf("%s|%s", rv.TaskNumber, rv.ReviewerID)
	return encodeInto(r.btx.Bucket(bucketReview), []byte(key), rv)
}
func (r *arbitrationRepo) ListReviews(_ context.Context, taskNumber string) ([]arbitration.ReentryReview, error) {
	var out []arbitration.ReentryReview
	prefix := []byte(taskNumber + "|")
	err := r.btx.Bucket(bucketReview).ForEach(func(k, v []byte) error {
		if !hasPrefix(k, prefix) {
			return nil
		}
		var rv arbitration.ReentryReview
		if err := decode(v, &rv); err != nil {
			return err
		}
		out = append(out, rv)
		return nil
	})
	return out, err
}
func (r *arbitrationRepo) SaveTerminal(_ context.Context, t task.TerminalDecision) error {
	// Terminal decisions are unique per task: the key is the task number, so a
	// second write for the same task is impossible without overwriting the
	// winning decision (which the application layer guards against).
	return encodeInto(r.btx.Bucket(bucketTerminal), []byte(t.TaskNumber), t)
}
func (r *arbitrationRepo) GetTerminal(_ context.Context, taskNumber string) (task.TerminalDecision, error) {
	var t task.TerminalDecision
	err := decode(r.btx.Bucket(bucketTerminal).Get([]byte(taskNumber)), &t)
	return t, err
}

func hasPrefix(k, prefix []byte) bool {
	if len(k) < len(prefix) {
		return false
	}
	for i := range prefix {
		if k[i] != prefix[i] {
			return false
		}
	}
	return true
}
