package bifrost

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestSelectAdaptiveFallbackAction_FastFailVsSlowTimeoutAndLastResort(t *testing.T) {
	// Fallback chain:
	// [0] slowCheap2 (not fast, not last resort)
	// [1] fastMain43 (IsFastRescue=true)
	// [2] fastMain60 (IsFastRescue=true)
	// [3] backup70   (IsFastRescue=true)
	// [4] full100    (IsFastRescue=true, IsLastResort=true)
	hints := []schemas.FallbackAttemptHint{
		{AttemptTimeoutMs: 38000, IsFastRescue: false, IsLastResort: false},
		{AttemptTimeoutMs: 38000, IsFastRescue: true, IsLastResort: false},
		{AttemptTimeoutMs: 38000, IsFastRescue: true, IsLastResort: false},
		{AttemptTimeoutMs: 38000, IsFastRescue: true, IsLastResort: false},
		{AttemptTimeoutMs: 20000, IsFastRescue: true, IsLastResort: true},
	}

	t.Run("FastFailKeepsCheapSlowCandidate", func(t *testing.T) {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(55*time.Second))
		ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{
			RequestStartTime:       time.Now().Add(-1 * time.Second), // only 1s elapsed (< 15s)
			FastRescueElapsedMs:    15000,
			RescueAttemptTimeoutMs: 18000,
			MaxTotalAttempts:       5,
			PrimaryHint:            schemas.FallbackAttemptHint{AttemptTimeoutMs: 38000},
			FallbackHints:          hints,
		})

		skip, stop, timeoutMs := selectAdaptiveFallbackAction(ctx, len(hints), 0, 1)
		if skip || stop {
			t.Fatalf("expected slowCheap2 NOT to be skipped on 1s fast fail, got skip=%v stop=%v", skip, stop)
		}
		if timeoutMs != 38000 {
			t.Fatalf("expected 38000ms timeout on fast fail, got %d", timeoutMs)
		}
	})

	t.Run("SlowTimeoutSkipsSlowCheapAndJumpsToFastRescue", func(t *testing.T) {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(20*time.Second))
		ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{
			RequestStartTime:       time.Now().Add(-38 * time.Second), // 38s elapsed (>= 15s!)
			FastRescueElapsedMs:    15000,
			RescueAttemptTimeoutMs: 18000,
			MaxTotalAttempts:       5,
			PrimaryHint:            schemas.FallbackAttemptHint{AttemptTimeoutMs: 38000},
			FallbackHints:          hints,
		})

		// Candidate [0] (slowCheap2) MUST be skipped!
		skip0, stop0, _ := selectAdaptiveFallbackAction(ctx, len(hints), 0, 1)
		if !skip0 || stop0 {
			t.Fatalf("expected slowCheap2 to be skipped after 38s timeout, got skip=%v stop=%v", skip0, stop0)
		}

		// Candidate [1] (fastMain43) MUST be selected with RescueAttemptTimeoutMs (18000ms)!
		skip1, stop1, timeout1 := selectAdaptiveFallbackAction(ctx, len(hints), 1, 1)
		if skip1 || stop1 {
			t.Fatalf("expected fastMain43 to run as fast rescue, got skip=%v stop=%v", skip1, stop1)
		}
		if timeout1 != 18000 {
			t.Fatalf("expected rescue timeout 18000ms, got %d", timeout1)
		}
	})

	t.Run("LastResort100PctLockedStrictlyToFinalAttempt", func(t *testing.T) {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(25*time.Second))
		ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{
			RequestStartTime:       time.Now().Add(-5 * time.Second),
			FastRescueElapsedMs:    15000,
			RescueAttemptTimeoutMs: 18000,
			MaxTotalAttempts:       5,
			PrimaryHint:            schemas.FallbackAttemptHint{AttemptTimeoutMs: 38000},
			FallbackHints:          hints,
		})

		// When 4 attempts already ran (attemptsExecuted == 4, remainingSlots == 1 = FINAL slot),
		// non-LastResort [3] yields so [4] (100% LastResort) runs as the final attempt!
		skip3, stop3, _ := selectAdaptiveFallbackAction(ctx, len(hints), 3, 4)
		if !skip3 || stop3 {
			t.Fatalf("expected remaining non-last-resort to yield final slot #5 to LastResort, got skip=%v stop=%v", skip3, stop3)
		}
		skip4, stop4, _ := selectAdaptiveFallbackAction(ctx, len(hints), 4, 4)
		if skip4 || stop4 {
			t.Fatalf("expected 100%% LastResort to execute on final attempt #5, got skip=%v stop=%v", skip4, stop4)
		}
		// After 5 attempts executed, budget stops
		_, stopAfter5, _ := selectAdaptiveFallbackAction(ctx, len(hints), 4, 5)
		if !stopAfter5 {
			t.Fatalf("expected stop=true once MaxTotalAttempts (5) reached")
		}
	})
}
