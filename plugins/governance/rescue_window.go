package governance

import "time"

// Fixed minute buckets keep recent rescue evidence independent of long-window recovery.
type rescueMinute struct {
	minute         int64
	attempts, fast int
}

type rescueCounts struct{ attempts, fast int }

func (s *TargetHealthState) rescueWindow(stream bool) *[10]rescueMinute {
	if stream {
		return &s.rescueWindows[1]
	}
	return &s.rescueWindows[0]
}

func (ht *HealthTracker) observeRescue(ruleID, key string, stream bool, kind OutcomeKind, latency time.Duration, now time.Time) {
	if kind == OutcomeIgnore {
		return
	}
	s := ht.getOrCreate(scopedTargetKey(ruleID, key))
	s.mu.Lock()
	defer s.mu.Unlock()
	minute := now.Unix() / 60
	b := &s.rescueWindow(stream)[minute%10]
	if b.minute > minute {
		return
	}
	if b.minute != minute {
		*b = rescueMinute{minute: minute}
	}
	b.attempts++
	if (kind == OutcomeSuccess || kind == OutcomeSlow) && latency <= 16*time.Second {
		b.fast++
	}
}

func (ht *HealthTracker) recentRescue(ruleID, key string, stream bool, now time.Time) (n rescueCounts) {
	ht.mu.RLock()
	s := ht.targets[scopedTargetKey(ruleID, key)]
	ht.mu.RUnlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	minute := now.Unix() / 60
	for _, b := range s.rescueWindow(stream) {
		if b.minute <= minute && b.minute > minute-10 {
			n.fast += b.fast
			n.attempts += b.attempts
		}
	}
	return
}
