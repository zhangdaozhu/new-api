package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	copilotChannel "github.com/QuantumNous/new-api/relay/channel/copilot"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func copilotOAuthSessionKey(channelID int, field string) string {
	return fmt.Sprintf("copilot_oauth_%s_%d", field, channelID)
}

// StartCopilotOAuth initiates a GitHub OAuth Device Flow for a new channel.
func StartCopilotOAuth(c *gin.Context) {
	startCopilotOAuthWithChannelID(c, 0)
}

// StartCopilotOAuthForChannel initiates a GitHub OAuth Device Flow for an existing channel.
func StartCopilotOAuthForChannel(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	startCopilotOAuthWithChannelID(c, channelID)
}

func startCopilotOAuthWithChannelID(c *gin.Context, channelID int) {
	if channelID > 0 {
		ch, err := model.GetChannelById(channelID, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if ch == nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
			return
		}
		if ch.Type != constant.ChannelTypeCopilot {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not GitHub Copilot"})
			return
		}
	}

	device, err := copilotChannel.StartDeviceFlow()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	session := sessions.Default(c)
	session.Set(copilotOAuthSessionKey(channelID, "device_code"), device.DeviceCode)
	_ = session.Save()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_code":        device.UserCode,
			"verification_uri": device.VerificationURI,
			"expires_in":       device.ExpiresIn,
			"interval":         device.Interval,
			"device_code":      device.DeviceCode,
		},
	})
}

// PollCopilotOAuth polls for the OAuth token after user authorization.
func PollCopilotOAuth(c *gin.Context) {
	pollCopilotOAuthWithChannelID(c, 0)
}

// PollCopilotOAuthForChannel polls for the OAuth token for an existing channel.
func PollCopilotOAuthForChannel(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	pollCopilotOAuthWithChannelID(c, channelID)
}

type copilotPollRequest struct {
	DeviceCode string `json:"device_code"`
}

func pollCopilotOAuthWithChannelID(c *gin.Context, channelID int) {
	req := copilotPollRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	if req.DeviceCode == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "missing device_code"})
		return
	}

	tokenResp, err := copilotChannel.PollForToken(req.DeviceCode)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// Still waiting for user authorization
	if tokenResp.Error == "authorization_pending" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"status": "pending",
			},
		})
		return
	}

	if tokenResp.Error == "slow_down" {
		interval := tokenResp.Interval
		if interval == 0 {
			interval = 10
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"status":   "slow_down",
				"interval": interval,
			},
		})
		return
	}

	if tokenResp.Error != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": tokenResp.ErrorDescription,
		})
		return
	}

	if tokenResp.AccessToken == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "empty access token"})
		return
	}

	// Get user info
	user, userErr := copilotChannel.GetGitHubUser(tokenResp.AccessToken)
	userName := ""
	if userErr == nil && user != nil {
		userName = user.Login
	}

	// For existing channel, append the token
	if channelID > 0 {
		ch, err := model.GetChannelById(channelID, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if ch == nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
			return
		}

		newKey := tokenResp.AccessToken
		if ch.Key != "" {
			newKey = ch.Key + "\n" + tokenResp.AccessToken
		}
		if err := model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("key", newKey).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		model.InitChannelCache()

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"status":    "authorized",
				"user_name": userName,
			},
		})
		return
	}

	// For new channel, return the token to the frontend
	session := sessions.Default(c)
	session.Delete(copilotOAuthSessionKey(channelID, "device_code"))
	_ = session.Save()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"status":       "authorized",
			"access_token": tokenResp.AccessToken,
			"user_name":    userName,
		},
	})
}
