package service

import (
	"sort"
	"sync"
	"time"
)

type WarmupHostStatus struct {
	Host           string `json:"host"`
	Proxy          string `json:"proxy,omitempty"`
	LastStatusCode int    `json:"last_status_code"`
	LastLatencyMs  int64  `json:"last_latency_ms"`
	LastError      string `json:"last_error,omitempty"`
	SuccessCount   int64  `json:"success_count"`
	FailureCount   int64  `json:"failure_count"`
	LastSuccessAt  int64  `json:"last_success_at,omitempty"`
	LastCheckAt    int64  `json:"last_check_at"`
}

var (
	warmupStatusStore sync.Map
	warmupStatusMu    sync.Mutex
)

func recordWarmupSuccess(t warmupTarget, code int, latency time.Duration) {
	warmupStatusMu.Lock()
	defer warmupStatusMu.Unlock()

	status := loadOrInitWarmupStatus(t)
	status.LastStatusCode = code
	status.LastLatencyMs = latency.Milliseconds()
	status.LastError = ""
	status.SuccessCount++
	now := time.Now().Unix()
	status.LastSuccessAt = now
	status.LastCheckAt = now
}

func recordWarmupFailure(t warmupTarget, code int, err error) {
	warmupStatusMu.Lock()
	defer warmupStatusMu.Unlock()

	status := loadOrInitWarmupStatus(t)
	status.LastStatusCode = code
	status.LastLatencyMs = 0
	if err != nil {
		status.LastError = err.Error()
	}
	status.FailureCount++
	status.LastCheckAt = time.Now().Unix()
}

func loadOrInitWarmupStatus(t warmupTarget) *WarmupHostStatus {
	if value, ok := warmupStatusStore.Load(t.key); ok {
		return value.(*WarmupHostStatus)
	}
	status := &WarmupHostStatus{Host: t.host, Proxy: t.proxy}
	actual, _ := warmupStatusStore.LoadOrStore(t.key, status)
	return actual.(*WarmupHostStatus)
}

// pruneWarmupStatus drops status entries whose target is no longer being warmed
// (channel disabled/deleted, or its base URL / proxy changed), so the status
// page reflects only the current target set rather than every host ever seen.
func pruneWarmupStatus(targets []warmupTarget) {
	warmupStatusMu.Lock()
	defer warmupStatusMu.Unlock()

	active := make(map[string]bool, len(targets))
	for _, t := range targets {
		active[t.key] = true
	}
	warmupStatusStore.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && !active[k] {
			warmupStatusStore.Delete(k)
		}
		return true
	})
}

func GetWarmupStatus() []*WarmupHostStatus {
	warmupStatusMu.Lock()
	defer warmupStatusMu.Unlock()

	out := make([]*WarmupHostStatus, 0)
	warmupStatusStore.Range(func(_, value any) bool {
		if status, ok := value.(*WarmupHostStatus); ok {
			snapshot := *status
			out = append(out, &snapshot)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host == out[j].Host {
			return out[i].Proxy < out[j].Proxy
		}
		return out[i].Host < out[j].Host
	})
	return out
}
