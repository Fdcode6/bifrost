package governance

import (
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type OutcomeKind string

const (
	OutcomeSuccess  OutcomeKind = "success"
	OutcomeSlow     OutcomeKind = "slow"
	OutcomeSoftFail OutcomeKind = "soft_fail"
	OutcomeHardFail OutcomeKind = "hard_fail"
)

func ClassifyOutcome(err *schemas.BifrostError, latency time.Duration, slowThreshold time.Duration) OutcomeKind {
	if err == nil {
		if slowThreshold > 0 && latency >= slowThreshold {
			return OutcomeSlow
		}
		return OutcomeSuccess
	}

	if err.StatusCode != nil {
		switch *err.StatusCode {
		case 408, 429, 502, 503, 504:
			return OutcomeSoftFail
		}
	}

	msg := strings.ToLower(errorMessage(err))
	softKeywords := []string{
		"deadline",
		"timeout",
		"timed out",
		"saturated",
		"no capacity",
		"quota",
		"rate limit",
		"too many",
		"overloaded",
		"upstream busy",
		"model is currently overloaded",
		"context_length_exceeded",
	}
	for _, keyword := range softKeywords {
		if strings.Contains(msg, keyword) {
			return OutcomeSoftFail
		}
	}

	if err.StatusCode != nil {
		if *err.StatusCode >= 500 {
			return OutcomeHardFail
		}
		if *err.StatusCode >= 400 {
			return OutcomeSuccess
		}
	}

	return OutcomeHardFail
}

func errorMessage(err *schemas.BifrostError) string {
	if err == nil || err.Error == nil {
		return ""
	}
	return err.Error.Message
}
