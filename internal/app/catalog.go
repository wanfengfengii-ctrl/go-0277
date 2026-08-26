package app

import (
	"context"
	"errors"

	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
)

// RegisterWarehouse persists a warehouse catalogue entry.
func (a *App) RegisterWarehouse(ctx context.Context, w catalog.Warehouse) error {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Catalog().SaveWarehouse(ctx, w); err != nil {
		return err
	}
	return tx.Commit()
}

// RegisterRule persists a versioned fumigation rule.
func (a *App) RegisterRule(ctx context.Context, r catalog.FumigationRule) error {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Catalog().SaveRule(ctx, r); err != nil {
		return err
	}
	return tx.Commit()
}

// RegisterBatch persists a pesticide batch. The initial mass must equal the
// sum of available, reserved, applied, returned and adjusted masses.
func (a *App) RegisterBatch(ctx context.Context, b catalog.PesticideBatch) error {
	if !b.Balanced() {
		return domain.NewError(domain.ErrDoseMassImbalance, "", 0).
			AddReason(b.Code, "", 0, "", domain.ErrDoseMassImbalance, "batch mass not conserved")
	}
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Catalog().SaveBatch(ctx, b); err != nil {
		return err
	}
	return tx.Commit()
}

// GetWarehouse returns a warehouse by code.
func (a *App) GetWarehouse(ctx context.Context, code string) (catalog.Warehouse, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return catalog.Warehouse{}, err
	}
	defer tx.Rollback()
	return tx.Catalog().GetWarehouse(ctx, code)
}

// ListWarehouses returns all warehouses in stable key order.
func (a *App) ListWarehouses(ctx context.Context) ([]catalog.Warehouse, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Catalog().ListWarehouses(ctx)
}

// ListRules returns all rules in version order.
func (a *App) ListRules(ctx context.Context) ([]catalog.FumigationRule, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Catalog().ListRules(ctx)
}

// ListBatches returns all pesticide batches.
func (a *App) ListBatches(ctx context.Context) ([]catalog.PesticideBatch, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Catalog().ListBatches(ctx)
}

// latestRule returns the highest-version rule.
func latestRule(ctx context.Context, repo interface {
	ListRules(context.Context) ([]catalog.FumigationRule, error)
}) (catalog.FumigationRule, error) {
	rules, err := repo.ListRules(ctx)
	if err != nil {
		return catalog.FumigationRule{}, err
	}
	var best catalog.FumigationRule
	for _, r := range rules {
		if r.Version > best.Version {
			best = r
		}
	}
	if best.Version == 0 && len(rules) == 0 {
		return catalog.FumigationRule{}, errors.New("no rule registered")
	}
	return best, nil
}

// SummarizePreview computes the canonical summary for a warehouse with its
// highest-version rule, used by the locking wizard before a lock is attempted.
func (a *App) SummarizePreview(ctx context.Context, warehouseCode string) (catalog.Summary, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return catalog.Summary{}, err
	}
	defer tx.Rollback()
	w, err := tx.Catalog().GetWarehouse(ctx, warehouseCode)
	if err != nil {
		return catalog.Summary{}, err
	}
	r, err := latestRule(ctx, tx.Catalog())
	if err != nil {
		return catalog.Summary{}, err
	}
	return catalog.Summarize(w, r), nil
}
