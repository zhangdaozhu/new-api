package copilot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	CopilotAPIEndpoint = "https://api.githubcopilot.com"
	copilotTokenURL    = "https://api.github.com/copilot_internal/v2/token"
)

// copilotTokenResponse is the response from the Copilot token exchange endpoint.
type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// tokenEntry caches a Copilot JWT for a GitHub OAuth token.
type tokenEntry struct {
	jwt       string
	expiresAt time.Time
}

var (
	tokenCache = make(map[string]*tokenEntry)
	tokenMu    sync.RWMutex
)

// GetCopilotToken exchanges a GitHub OAuth token for a Copilot API JWT.
// Results are cached and auto-refreshed.
func GetCopilotToken(githubToken string) (string, error) {
	githubToken = normalizeGitHubToken(githubToken)
	if githubToken == "" {
		return "", fmt.Errorf("empty github token")
	}
	tokenMu.RLock()
	if entry, ok := tokenCache[githubToken]; ok {
		if time.Now().Before(entry.expiresAt.Add(-60 * time.Second)) {
			tokenMu.RUnlock()
			return entry.jwt, nil
		}
	}
	tokenMu.RUnlock()

	return refreshCopilotToken(githubToken)
}

func refreshCopilotToken(githubToken string) (string, error) {
	githubToken = normalizeGitHubToken(githubToken)
	if githubToken == "" {
		return "", fmt.Errorf("empty github token")
	}
	tokenMu.Lock()
	defer tokenMu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := tokenCache[githubToken]; ok {
		if time.Now().Before(entry.expiresAt.Add(-60 * time.Second)) {
			return entry.jwt, nil
		}
	}

	req, err := http.NewRequest(http.MethodGet, copilotTokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("copilot token exchange: %w", err)
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("User-Agent", "new-api/1.0")
	req.Header.Set("Editor-Version", "vscode/1.99.0")
	req.Header.Set("Editor-Plugin-Version", "copilot/1.0.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("copilot token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("copilot token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result copilotTokenResponse
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return "", fmt.Errorf("failed to decode copilot token response: %w", err)
	}

	if result.Token == "" {
		return "", fmt.Errorf("empty copilot token - check Copilot subscription")
	}

	tokenCache[githubToken] = &tokenEntry{
		jwt:       result.Token,
		expiresAt: time.Unix(result.ExpiresAt, 0),
	}

	return result.Token, nil
}

// QuotaInfo holds aggregated quota information for a Copilot account.
type QuotaInfo struct {
	TotalRequests       float64 `json:"total_requests"`
	UsedRequests        float64 `json:"used_requests"`
	RemainingRequests   float64 `json:"remaining_requests"`
	RemainingPercentage float64 `json:"remaining_percentage"`
	ResetDate           string  `json:"reset_date,omitempty"`
	IsUnlimited         bool    `json:"is_unlimited"`
}

// copilotUserResponse represents the /copilot_internal/user API response.
type copilotUserResponse struct {
	ChatEnabled    bool            `json:"chat_enabled"`
	CopilotPlan    string          `json:"copilot_plan"`
	QuotaSnapshots []quotaSnapshot `json:"quota_snapshots"`
}

type quotaSnapshot struct {
	EntitlementRequests float64 `json:"entitlement_requests"`
	UsedRequests        float64 `json:"used_requests"`
	ResetDate           *string `json:"reset_date"`
	Unlimited           bool    `json:"unlimited"`
}

const copilotUserURL = "https://api.github.com/copilot_internal/user"

// GetQuota returns the quota/usage information for a single GitHub token.
func GetQuota(_ context.Context, githubToken string) (*QuotaInfo, error) {
	githubToken = normalizeGitHubToken(githubToken)
	if githubToken == "" {
		return nil, fmt.Errorf("empty github token")
	}
	req, err := http.NewRequest(http.MethodGet, copilotUserURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("User-Agent", "new-api/1.0")
	req.Header.Set("Editor-Version", "vscode/1.99.0")
	req.Header.Set("Editor-Plugin-Version", "copilot/1.0.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get copilot user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("copilot user API failed (status %d): %s", resp.StatusCode, string(body))
	}

	var userResp copilotUserResponse
	if err := common.DecodeJson(resp.Body, &userResp); err != nil {
		return nil, fmt.Errorf("failed to decode copilot user response: %w", err)
	}

	info := &QuotaInfo{}
	for _, snap := range userResp.QuotaSnapshots {
		info.TotalRequests += snap.EntitlementRequests
		info.UsedRequests += snap.UsedRequests
		if snap.ResetDate != nil && info.ResetDate == "" {
			info.ResetDate = *snap.ResetDate
		}
		if snap.Unlimited {
			info.IsUnlimited = true
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
	var tokens []string
	for _, line := range strings.Split(keys, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			tokens = append(tokens, line)
		}
	}
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

func normalizeGitHubToken(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
