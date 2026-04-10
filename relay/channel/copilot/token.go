package copilot

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// QuotaInfo holds aggregated quota information for a Copilot account.
type QuotaInfo struct {
	// Total entitled requests across all quota types
	TotalRequests float64 `json:"total_requests"`
	// Requests already used
	UsedRequests float64 `json:"used_requests"`
	// Remaining requests
	RemainingRequests float64 `json:"remaining_requests"`
	// Remaining percentage (0.0 to 1.0)
	RemainingPercentage float64 `json:"remaining_percentage"`
	// Reset date (ISO 8601), from the first quota snapshot that has one
	ResetDate string `json:"reset_date,omitempty"`
	// Whether the account has unlimited entitlement
	IsUnlimited bool `json:"is_unlimited"`
	// Per-quota-type snapshots
	Snapshots map[string]rpc.QuotaSnapshot `json:"snapshots,omitempty"`
}

// clientEntry holds a long-lived Copilot SDK Client for a GitHub token.
type clientEntry struct {
	client *copilot.Client
	mu     sync.Mutex
}

var (
	clientPool = make(map[string]*clientEntry)
	poolMu     sync.RWMutex
)

// getClient returns a started Copilot SDK Client for the given GitHub token.
// Clients are cached and reused across requests.
func getClient(ctx context.Context, githubToken string) (*copilot.Client, error) {
	poolMu.RLock()
	entry, ok := clientPool[githubToken]
	poolMu.RUnlock()

	if ok {
		return entry.client, nil
	}

	poolMu.Lock()
	defer poolMu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := clientPool[githubToken]; ok {
		return entry.client, nil
	}

	client := copilot.NewClient(&copilot.ClientOptions{
		GitHubToken: githubToken,
	})

	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start copilot client: %w", err)
	}

	clientPool[githubToken] = &clientEntry{client: client}
	return client, nil
}

// GetQuota returns the quota/usage information for a single GitHub token.
func GetQuota(ctx context.Context, githubToken string) (*QuotaInfo, error) {
	client, err := getClient(ctx, githubToken)
	if err != nil {
		return nil, err
	}

	result, err := client.RPC.Account.GetQuota(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get copilot quota: %w", err)
	}

	info := &QuotaInfo{
		Snapshots: result.QuotaSnapshots,
	}

	for _, snap := range result.QuotaSnapshots {
		info.TotalRequests += snap.EntitlementRequests
		info.UsedRequests += snap.UsedRequests
		if snap.ResetDate != nil && info.ResetDate == "" {
			info.ResetDate = *snap.ResetDate
		}
	}
	info.RemainingRequests = info.TotalRequests - info.UsedRequests
	if info.TotalRequests > 0 {
		info.RemainingPercentage = info.RemainingRequests / info.TotalRequests
	}

	return info, nil
}

// GetAggregatedQuota returns aggregated quota across multiple GitHub tokens (newline-separated).
func GetAggregatedQuota(ctx context.Context, keys string) (*QuotaInfo, error) {
	tokens := splitKeys(keys)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no valid tokens provided")
	}

	aggregated := &QuotaInfo{}
	var lastErr error
	successCount := 0
	for _, token := range tokens {
		q, err := GetQuota(ctx, token)
		if err != nil {
			lastErr = err
			common.SysError(fmt.Sprintf("failed to get copilot quota for token %s...: %v", token[:min(8, len(token))], err))
			continue
		}
		successCount++
		aggregated.TotalRequests += q.TotalRequests
		aggregated.UsedRequests += q.UsedRequests
		aggregated.RemainingRequests += q.RemainingRequests
		if q.ResetDate != "" && aggregated.ResetDate == "" {
			aggregated.ResetDate = q.ResetDate
		}
		if q.IsUnlimited {
			aggregated.IsUnlimited = true
		}
	}
	if aggregated.TotalRequests > 0 {
		aggregated.RemainingPercentage = aggregated.RemainingRequests / aggregated.TotalRequests
	}
	if successCount == 0 && lastErr != nil {
		return nil, fmt.Errorf("all tokens failed, last error: %w", lastErr)
	}
	return aggregated, nil
}

func splitKeys(keys string) []string {
	var result []string
	for _, line := range strings.Split(keys, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
