package logging

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/framework/logstore"
)

const (
	profitReconciliationInitialDelay = 2 * time.Minute
	profitReconciliationInterval     = 10 * time.Minute
	profitReconciliationBatchLimit   = 1000
)

func (p *LoggerPlugin) syncProfitEventFromLog(entry *logstore.Log) {
	if p == nil || p.store == nil || entry == nil {
		return
	}
	if _, err := p.store.UpsertProfitEventFromLog(p.ctx, entry); err != nil {
		p.logger.Warn("failed to sync profit event for log %s: %v", entry.ID, err)
	}
}

func (p *LoggerPlugin) syncProfitEventForLogID(ctx context.Context, id string) error {
	if p == nil || p.store == nil {
		return nil
	}
	entry, err := p.store.FindByID(ctx, id)
	if err != nil {
		return err
	}
	_, err = p.store.UpsertProfitEventFromLog(ctx, entry)
	return err
}

func (p *LoggerPlugin) syncProfitEventsForLogIDs(ctx context.Context, ids []string) {
	for _, id := range ids {
		if err := p.syncProfitEventForLogID(ctx, id); err != nil {
			p.logger.Warn("failed to resync profit event for log %s: %v", id, err)
		}
	}
}

func (p *LoggerPlugin) reconcileMissingProfitEvents() (*logstore.ProfitBackfillResult, error) {
	if p == nil || p.store == nil {
		return &logstore.ProfitBackfillResult{}, nil
	}
	return p.store.BackfillProfitEvents(p.ctx, profitReconciliationBatchLimit)
}

func (p *LoggerPlugin) GetProfitReconciliationStatus(ctx context.Context) (*logstore.ProfitReconciliationStatus, error) {
	if p == nil || p.store == nil {
		return &logstore.ProfitReconciliationStatus{
			IntervalSeconds: int64(profitReconciliationInterval.Seconds()),
			BatchLimit:      profitReconciliationBatchLimit,
		}, nil
	}
	missing, err := p.store.CountMissingProfitEvents(ctx)
	if err != nil {
		return nil, err
	}

	p.profitReconciliationMu.RLock()
	lastRunAt := cloneTimePtr(p.profitReconciliationLastRunAt)
	nextRunAt := cloneTimePtr(p.profitReconciliationNextRunAt)
	lastResult := cloneProfitBackfillResult(p.profitReconciliationLastResult)
	lastError := p.profitReconciliationLastError
	p.profitReconciliationMu.RUnlock()

	return &logstore.ProfitReconciliationStatus{
		MissingEventCount: missing,
		LastRunAt:         lastRunAt,
		NextRunAt:         nextRunAt,
		IntervalSeconds:   int64(profitReconciliationInterval.Seconds()),
		BatchLimit:        profitReconciliationBatchLimit,
		LastResult:        lastResult,
		LastError:         lastError,
	}, nil
}

func (p *LoggerPlugin) setNextProfitReconciliationRun(nextRunAt time.Time) {
	if p == nil {
		return
	}
	nextRunAt = nextRunAt.UTC()
	p.profitReconciliationMu.Lock()
	p.profitReconciliationNextRunAt = &nextRunAt
	p.profitReconciliationMu.Unlock()
}

func (p *LoggerPlugin) profitReconciliationWorker() {
	defer p.wg.Done()

	initialDelay := time.NewTimer(profitReconciliationInitialDelay)
	defer initialDelay.Stop()

	select {
	case <-initialDelay.C:
		p.runProfitReconciliation()
	case <-p.done:
		return
	}

	ticker := time.NewTicker(profitReconciliationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.runProfitReconciliation()
		case <-p.done:
			return
		}
	}
}

func (p *LoggerPlugin) runProfitReconciliation() {
	result, err := p.reconcileMissingProfitEvents()
	now := time.Now().UTC()
	nextRunAt := now.Add(profitReconciliationInterval)

	p.profitReconciliationMu.Lock()
	p.profitReconciliationLastRunAt = &now
	p.profitReconciliationNextRunAt = &nextRunAt
	p.profitReconciliationLastResult = cloneProfitBackfillResult(result)
	if err != nil {
		p.profitReconciliationLastError = err.Error()
	} else {
		p.profitReconciliationLastError = ""
	}
	p.profitReconciliationMu.Unlock()

	if err != nil {
		p.logger.Warn("failed to reconcile missing profit events: %v", err)
		return
	}
	if result != nil && result.Created > 0 {
		p.logger.Info("reconciled %d missing profit events", result.Created)
	}
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneProfitBackfillResult(value *logstore.ProfitBackfillResult) *logstore.ProfitBackfillResult {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
