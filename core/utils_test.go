package bifrost

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestRestoreFallbackAPIKeyIDFromContext_FirstFallback(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 1)
	ctx.SetValue(schemas.BifrostContextKeyFallbackKeyIDs, []string{"site-a-key", "site-b-key"})

	restoreFallbackAPIKeyIDFromContext(ctx)

	keyID, _ := ctx.Value(schemas.BifrostContextKeyAPIKeyID).(string)
	if keyID != "site-a-key" {
		t.Fatalf("expected first fallback key id to be restored, got %q", keyID)
	}
}

func TestRestoreFallbackAPIKeyIDFromContext_EmptyFallbackKeyClearsPin(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 2)
	ctx.SetValue(schemas.BifrostContextKeyFallbackKeyIDs, []string{"site-a-key", ""})
	ctx.SetValue(schemas.BifrostContextKeyAPIKeyID, "stale-primary-key")

	restoreFallbackAPIKeyIDFromContext(ctx)

	keyID, _ := ctx.Value(schemas.BifrostContextKeyAPIKeyID).(string)
	if keyID != "" {
		t.Fatalf("expected empty fallback key id to keep api key pin cleared, got %q", keyID)
	}
}
