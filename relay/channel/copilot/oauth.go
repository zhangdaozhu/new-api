package copilot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	// Copilot CLI's OAuth App Client ID
	CopilotOAuthClientID = "Ov23ctDVkRmgkPke0Mmm"
	deviceCodeURL        = "https://github.com/login/device/code"
	accessTokenURL       = "https://github.com/login/oauth/access_token"
	userURL              = "https://api.github.com/user"
)

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Interval         int    `json:"interval"`
}

type GitHubUser struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// StartDeviceFlow initiates a GitHub OAuth Device Flow and returns the device code info.
func StartDeviceFlow() (*DeviceCodeResponse, error) {
	payload := fmt.Sprintf("client_id=%s&scope=read:user", CopilotOAuthClientID)
	req, err := http.NewRequest(http.MethodPost, deviceCodeURL, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to start device flow: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device flow request failed: %s %s", resp.Status, string(body))
	}

	var result DeviceCodeResponse
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode device code response: %w", err)
	}
	return &result, nil
}

// PollForToken polls GitHub for an access token after user authorization.
func PollForToken(deviceCode string) (*TokenResponse, error) {
	payload := fmt.Sprintf("client_id=%s&device_code=%s&grant_type=urn:ietf:params:oauth:grant-type:device_code",
		CopilotOAuthClientID, deviceCode)

	req, err := http.NewRequest(http.MethodPost, accessTokenURL, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to poll for token: %w", err)
	}
	defer resp.Body.Close()

	var result TokenResponse
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}
	return &result, nil
}

// GetGitHubUser fetches the authenticated user's info.
func GetGitHubUser(token string) (*GitHubUser, error) {
	req, err := http.NewRequest(http.MethodGet, userURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "new-api-copilot-channel")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API failed: %s %s", resp.Status, string(body))
	}

	var user GitHubUser
	if err := common.DecodeJson(resp.Body, &user); err != nil {
		return nil, fmt.Errorf("failed to decode user response: %w", err)
	}
	return &user, nil
}
