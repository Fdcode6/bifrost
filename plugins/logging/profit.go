package logging

import (
	"context"

	"github.com/maximhq/bifrost/framework/logstore"
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
