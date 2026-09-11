package app

import (
	"fmt"
	"net/http"
	"strings"
)

const crossPanelSubscriptionPathPrefix = "/isub"

func validCrossPanelSubscriptionPath(path string) bool {
	if !strings.HasPrefix(path, crossPanelSubscriptionPathPrefix) {
		return false
	}
	token := strings.TrimPrefix(path, crossPanelSubscriptionPathPrefix)
	return validSubscriptionToken(token) &&
		strings.ContainsAny(token, "abcdefghijklmnopqrstuvwxyz") &&
		strings.ContainsAny(token, "0123456789")
}

func randomCrossPanelSubscriptionPath() (string, error) {
	path, err := randomSubscriptionPath()
	if err != nil {
		return "", err
	}
	return crossPanelSubscriptionPathPrefix + strings.TrimPrefix(path, subscriptionPathPrefix), nil
}

func validateCrossPanelSubscriptionPath(settings Settings) error {
	path := settings.CrossPanelSubscriptionPath
	if !validCrossPanelSubscriptionPath(path) {
		return fmt.Errorf("跨面板订阅路径必须是 /isub 加 16 位小写字母和数字混合 token")
	}
	if err := validateSubscriptionPaths(path, ""); err != nil {
		return fmt.Errorf("跨面板订阅路径无效: %w", err)
	}
	first := strings.Split(strings.TrimPrefix(path, "/"), "/")[0]
	if first == "_m-ui" || first == "_subscription-assets" {
		return fmt.Errorf("跨面板订阅路径不能使用保留路径 /%s", first)
	}
	if path == normalizeSubscriptionPath(settings.SubscriptionPath) {
		return fmt.Errorf("跨面板订阅路径不能与 Subscription Path 相同")
	}
	if path == strings.TrimSuffix(normalizePanelPath(settings.PanelPath), "/") {
		return fmt.Errorf("跨面板订阅路径不能与面板 URI 路径相同")
	}
	return nil
}

func (a *App) handleNewCrossPanelSubscriptionPath(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, 405, apiResponse{Message: "method not allowed"})
		return
	}
	path, err := randomCrossPanelSubscriptionPath()
	if err != nil {
		writeJSON(w, 500, apiResponse{Message: a.tr(err.Error())})
		return
	}
	// Return a draft, just like Subscription Path. Only Save persists it.
	writeJSON(w, 200, apiResponse{OK: true, Data: map[string]string{"path": path}})
}

// The bare cross-panel URL opens the subscription page. Its /clash sibling is
// reserved for the direct Clash client response, matching local subscriptions.
// This step only defines the URL shape; no cross-panel content route is enabled.
func crossPanelSubscriptionPublicPath(settings Settings, token string) string {
	path := normalizeSubscriptionPath(settings.CrossPanelSubscriptionPath)
	if path == "" || !validSubscriptionToken(token) {
		return ""
	}
	return path + "/" + token
}

func crossPanelSubscriptionClashPublicPath(settings Settings, token string) string {
	base := crossPanelSubscriptionPublicPath(settings, token)
	if base == "" {
		return ""
	}
	return base + normalizeClashPath(settings.ClashPath)
}
