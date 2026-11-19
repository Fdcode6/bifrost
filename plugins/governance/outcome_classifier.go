package governance

import (
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type OutcomeKind string

const (
	OutcomeSuccess      OutcomeKind = "success"
	OutcomeSlow         OutcomeKind = "slow"
	OutcomeSoftFail     OutcomeKind = "soft_fail"
	OutcomeHardFail     OutcomeKind = "hard_fail"
	OutcomeFatalChannel OutcomeKind = "fatal_channel"
	OutcomeIgnore       OutcomeKind = "ignore"
)

var fatalChannelKeywords = []string{
	"insufficient_user_quota",
	"insufficient_quota",
	"insufficient balance",
	"insufficient_balance",
	"credit insufficient",
	"user quota is not enough",
	"no active subscription",
	"deposit required",
	"access restricted",
	"invalid token",
	"invalid_api_key",
	"invalid api key",
	"user has been banned",
	"account banned",
	"account suspended",
	"permission denied",
	"no available channel",
	"没有可用token",
	"没有可用渠道",
	"auth_unavailable",
	"no auth available",
	"fallback_skip_json",
	"does not support json mode",
	"requested entity was not found",
	"model_not_found",
	"model not found",
}

var softFailKeywords = []string{
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
	"temporarily unavailable",
	"resource has been exhausted",
	"high demand",
	"context_length_exceeded",
	"connection reset",
	"closed connection",
	"i/o timeout",
}

func ClassifyOutcome(err *schemas.BifrostError, latency time.Duration, slowThreshold time.Duration) OutcomeKind {
	if err == nil {
		if slowThreshold > 0 && latency >= slowThreshold {
			return OutcomeSlow
		}
		return OutcomeSuccess
	}

	text := strings.ToLower(classifyErrorText(err))

	// Client cancellations must never penalize nor clear health state for a channel.
	if isClientCancelledError(err, text) {
		return OutcomeIgnore
	}

	// Account/channel fatal keywords take precedence over generic 400/503 status codes.
	for _, keyword := range fatalChannelKeywords {
		if strings.Contains(text, keyword) {
			return OutcomeFatalChannel
		}
	}

	// In grouped routing, provider/model/key are operator-configured, so upstream 401/402/403/404
	// indicate broken credentials, billing restrictions, or invalid upstream model routes.
	if err.StatusCode != nil {
		switch *err.StatusCode {
		case 401, 402, 403, 404:
			return OutcomeFatalChannel
		case 408, 429, 502, 503, 504:
			return OutcomeSoftFail
		case 451:
			// Upstream reverse-proxy protocol crash (e.g. "Protocol generation failed")
			return OutcomeHardFail
		}
	}

	for _, keyword := range softFailKeywords {
		if strings.Contains(text, keyword) {
			return OutcomeSoftFail
		}
	}

	if strings.Contains(text, "protocol generation") ||
		strings.Contains(text, "generation failed") ||
		strings.Contains(text, "empty response") ||
		strings.Contains(text, "empty_response") ||
		strings.Contains(text, "upstream error") {
		return OutcomeHardFail
	}

	if err.StatusCode != nil {
		if *err.StatusCode >= 500 {
			return OutcomeHardFail
		}
		if *err.StatusCode >= 400 {
			// Pure client-side 4xx (e.g. malformed request body) must be ignored
			// rather than treated as OutcomeSuccess (which would falsely reset cooldowns).
			return OutcomeIgnore
		}
	}

	return OutcomeHardFail
}

func isClientCancelledError(err *schemas.BifrostError, loweredText string) bool {
	if err == nil {
		return false
	}
	if err.StatusCode != nil && *err.StatusCode == 499 {
		return true
	}
	if err.Type != nil && *err.Type == schemas.RequestCancelled {
		return true
	}
	if err.Error != nil && err.Error.Type != nil && *err.Error.Type == schemas.RequestCancelled {
		return true
	}
	return strings.Contains(loweredText, "context canceled") || strings.Contains(loweredText, "client disconnected")
}

func classifyErrorText(err *schemas.BifrostError) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 5)
	if err.Type != nil && *err.Type != "" {
		parts = append(parts, *err.Type)
	}
	if err.Error != nil {
		if err.Error.Type != nil && *err.Error.Type != "" {
			parts = append(parts, *err.Error.Type)
		}
		if err.Error.Code != nil && *err.Error.Code != "" {
			parts = append(parts, *err.Error.Code)
		}
		if err.Error.Message != "" {
			parts = append(parts, err.Error.Message)
		}
		if err.Error.Error != nil && err.Error.Error.Error() != "" {
			parts = append(parts, err.Error.Error.Error())
		}
	}
	return strings.Join(parts, " ")
}

func errorMessage(err *schemas.BifrostError) string {
	if err == nil || err.Error == nil {
		return ""
	}
	return err.Error.Message
}
