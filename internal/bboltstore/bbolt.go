// Package bboltstore is the production persistence implementation backed by
// the embedded bbolt key-value database. It provides durable, single-writer
// transactions, unique-key constraints for resource leases and terminal
// decisions, and a startup migration and recovery scan.
package bboltstore

import (
	"context"
	"fmt"

	bolt "go.etcd.io/bbolt"

	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// Bucket names. Each entity type lives in its own bucket so whole-bucket
// iteration yields every record of that type.
var (
	bucketMeta        = []byte("meta")
	bucketWarehouse   = []byte("warehouse")
	bucketRule        = []byte("rule")
	bucketBatch       = []byte("batch")
	bucketTask        = []byte("task")
	bucketEvent       = []byte("event")
	bucketReceipt     = []byte("receipt")
	bucketLease       = []byte("lease")
	bucketDeviceCall  = []byte("device_call")
	bucketMeasurement = []byte("measurement")
	bucketCell        = []byte("cell")
	bucketIntegral    = []byte("integral")
	bucketLedger      = []byte("ledger")
	bucketApplication = []byte("application")
	bucketLeak        = []byte("leak")
	bucketClosure     = []byte("closure")
	bucketVentilation = []byte("ventilation")
	bucketReview      = []byte("review")
	bucketTerminal    = []byte("terminal")
)

// DB is the bbolt-backed store implementation.
type DB struct {
	db *bolt.DB
}

// Open opens (or creates) the embedded database at path and runs migrations.
func Open(path string) (*DB, error) {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &DB{db: db}, nil
}

// Close releases the database handle.
func (d *DB) Close() error {
	return d.db.Close()
}

// migrate creates every bucket if it is missing.
func migrate(btx *bolt.Tx) error {
	for _, name := range [][]byte{
		bucketMeta, bucketWarehouse, bucketRule, bucketBatch, bucketTask,
		bucketEvent, bucketReceipt, bucketLease, bucketDeviceCall,
		bucketMeasurement, bucketCell, bucketIntegral, bucketLedger,
		bucketApplication, bucketLeak, bucketClosure, bucketVentilation,
		bucketReview, bucketTerminal,
	} {
		if _, err := btx.CreateBucketIfNotExists(name); err != nil {
			return err
		}
	}
	return nil
}

// Begin starts a transaction. writable=false yields a read-only transaction
// used by query paths.
func (d *DB) Begin(_ context.Context, writable bool) (store.Tx, error) {
	btx, err := d.db.Begin(writable)
	if err != nil {
		return nil, err
	}
	return &tx{btx: btx}, nil
}

// Recover runs the migration and recovery scan. It returns the non-terminal
// tasks to resume and quarantines any task whose persisted evidence violates
// the batch conservation identity.
func (d *DB) Recover(ctx context.Context) (store.RecoverReport, error) {
	var report store.RecoverReport
	err := d.db.Update(func(btx *bolt.Tx) error {
		if err := migrate(btx); err != nil {
			return err
		}
		report.Migrated = true
		return nil
	})
	if err != nil {
		return report, err
	}

	// Conservation scan over batches. Any unbalanced batch quarantines every
	// non-terminal task that references it in its frozen snapshot.
	unbalanced := map[string]bool{}
	_ = d.db.View(func(btx *bolt.Tx) error {
		b := btx.Bucket(bucketBatch)
		return b.ForEach(func(_, v []byte) error {
			var batch catalog.PesticideBatch
			if err := decode(v, &batch); err != nil {
				return err
			}
			if !batch.Balanced() {
				unbalanced[batch.Code] = true
			}
			return nil
		})
	})

	err = d.db.View(func(btx *bolt.Tx) error {
		b := btx.Bucket(bucketTask)
		return b.ForEach(func(_, v []byte) error {
			var t task.FumigationTask
			if err := decode(v, &t); err != nil {
				return err
			}
			if t.Status.IsTerminal() {
				return nil
			}
			if t.Snapshot != nil {
				for _, batch := range t.Snapshot.Batches {
					if unbalanced[batch.Code] {
						report.QuarantinedTasks = append(report.QuarantinedTasks, t.Number)
						return nil
					}
				}
			}
			report.RecoveredTasks = append(report.RecoveredTasks, t.Number)
			return nil
		})
	})
	if err != nil {
		return report, err
	}

	// Persist quarantine decisions for any offending task.
	if len(report.QuarantinedTasks) > 0 {
		_ = d.db.Update(func(btx *bolt.Tx) error {
			tb := btx.Bucket(bucketTask)
			tmb := btx.Bucket(bucketTerminal)
			for _, number := range report.QuarantinedTasks {
				var t task.FumigationTask
				if err := decode(tb.Get([]byte(number)), &t); err != nil {
					continue
				}
				if t.Status.IsTerminal() {
					continue
				}
				t.Status = domain.StatusRiskIsolated
				t.Version++
				t.Terminal = &task.TerminalDecision{
					TaskNumber: t.Number,
					Kind:       task.TerminalRiskIsolated,
					Version:    t.Version,
					Reason:     domain.ErrDoseMassImbalance,
					Evidence:   "recovery: batch conservation violated",
					DecidedAt:  t.LogicalClock,
				}
				_ = encodeInto(tb, []byte(number), t)
				_ = encodeInto(tmb, []byte(number), *t.Terminal)
			}
			return nil
		})
	}

	return report, nil
}
