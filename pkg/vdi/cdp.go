package vdi

import (
	"fmt"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// ExecuteCDPCommand executes a CDP command via Selenium's ExecuteChromeDPCommand.
// This keeps all CDP usage centralized in the VDI abstraction layer.
func ExecuteCDPCommand(wd WebDriver, command string, params map[string]interface{}) (interface{}, error) {
	if wd == nil {
		return nil, fmt.Errorf("webdriver is nil")
	}

	cmn.DebugMsg(cmn.DbgLvlDebug5, "[CDP] Executing command '%s' with params: %v", command, params)
	result, err := wd.ExecuteChromeDPCommand(command, params)
	if err != nil {
		return nil, fmt.Errorf("executing CDP command '%s': %w", command, err)
	}

	return result, nil
}

// EnableNetwork enables the CDP Network domain.
func EnableNetwork(wd WebDriver, maxPostDataSize *int) error {
	params := map[string]interface{}{}
	if maxPostDataSize != nil {
		params["maxPostDataSize"] = *maxPostDataSize
	}
	if len(params) == 0 {
		params = nil
	}
	_, err := ExecuteCDPCommand(wd, "Network.enable", params)
	return err
}

// SetCacheDisabled enables/disables browser cache via CDP.
func SetCacheDisabled(wd WebDriver, cacheDisabled bool) error {
	_, err := ExecuteCDPCommand(wd, "Network.setCacheDisabled", map[string]interface{}{
		"cacheDisabled": cacheDisabled,
	})
	return err
}

// EnableServiceWorker enables ServiceWorker domain events.
func EnableServiceWorker(wd WebDriver) error {
	_, err := ExecuteCDPCommand(wd, "ServiceWorker.enable", map[string]interface{}{})
	return err
}

// SetTargetAutoAttach configures auto-attach behavior for targets/frames.
func SetTargetAutoAttach(wd WebDriver, autoAttach, waitForDebuggerOnStart, flatten bool) error {
	_, err := ExecuteCDPCommand(wd, "Target.setAutoAttach", map[string]interface{}{
		"autoAttach":             autoAttach,
		"waitForDebuggerOnStart": waitForDebuggerOnStart,
		"flatten":                flatten,
	})
	return err
}

// EnableLog enables the CDP Log domain.
func EnableLog(wd WebDriver) error {
	_, err := ExecuteCDPCommand(wd, "Log.enable", map[string]interface{}{})
	return err
}

// EnablePage enables the CDP Page domain.
func EnablePage(wd WebDriver) error {
	_, err := ExecuteCDPCommand(wd, "Page.enable", map[string]interface{}{})
	return err
}

// SetBlockedURLs blocks URL patterns at network layer.
func SetBlockedURLs(wd WebDriver, urls []string) error {
	_, err := ExecuteCDPCommand(wd, "Network.setBlockedURLs", map[string]interface{}{
		"urls": urls,
	})
	return err
}

// GetResponseBody fetches response body for a request id.
func GetResponseBody(wd WebDriver, requestID string) (string, bool, error) {
	response, err := ExecuteCDPCommand(wd, "Network.getResponseBody", map[string]interface{}{
		"requestId": requestID,
	})
	if err != nil {
		return "", false, err
	}

	bodyData, ok := response.(map[string]interface{})
	if !ok || bodyData["body"] == nil {
		return "", false, fmt.Errorf("invalid response body payload")
	}

	bodyText, _ := bodyData["body"].(string)
	isBase64, _ := bodyData["base64Encoded"].(bool)
	return bodyText, isBase64, nil
}

// SetExtraHTTPHeaders sets HTTP headers sent by the browser.
func SetExtraHTTPHeaders(wd WebDriver, headers map[string]interface{}) error {
	_, err := ExecuteCDPCommand(wd, "Network.setExtraHTTPHeaders", map[string]interface{}{
		"headers": headers,
	})
	return err
}

// SetUserAgentOverride sets user agent and platform via CDP.
func SetUserAgentOverride(wd WebDriver, userAgent string, platform string) error {
	params := map[string]interface{}{
		"userAgent": userAgent,
	}
	if platform != "" {
		params["platform"] = platform
	}
	_, err := ExecuteCDPCommand(wd, "Network.setUserAgentOverride", params)
	return err
}

// AddScriptToEvaluateOnNewDocument injects script before page scripts run.
func AddScriptToEvaluateOnNewDocument(wd WebDriver, source string) error {
	_, err := ExecuteCDPCommand(wd, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
		"source": source,
	})
	return err
}
