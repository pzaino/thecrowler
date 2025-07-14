// Package vdi is an abstraction layer for the Virtual Desktop Infrastructure (VDI) used by the Crowler.
package vdi

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	selenium "github.com/go-auxiliaries/selenium"
	"github.com/go-auxiliaries/selenium/chrome"
	"github.com/go-auxiliaries/selenium/firefox"
	"github.com/go-auxiliaries/selenium/log"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const (
	browserGpuDefault        = "--disable-gpu"
	browserJSDefault         = "--enable-javascript"
	browserStartMaxDefault   = "--start-maximized"
	browserWindowSizeDefault = "--window-size=1920,1080"
	browserExtensionsDefault = "--disable-extensions"
	browserSandboxDefault    = "--no-sandbox"
	browserInfoBarsDefault   = "--disable-infobars"
	browserPopupsDefault     = "--disable-popup-blocking"
	browserShmDefault        = "--disable-dev-shm-usage"

	// BrowserChrome represents the Chrome browser
	BrowserChrome = "chrome"
	// BrowserFirefox represents the Firefox browser
	BrowserFirefox = "firefox"
	// BrowserChromium represents the Chromium browser
	BrowserChromium = "chromium"

	// Error messages

	// VDIConnError is the error message for a connection error to the VDI
	VDIConnError = "connecting to the VDI: %v, retrying in %d seconds...\n"
)

var (
	browserSettingsMap = map[string]map[string]string{
		BrowserChrome: {
			"browserName":   BrowserChrome,
			"windowSize":    browserWindowSizeDefault, // Set the window size to 1920x1080
			"initialWindow": browserStartMaxDefault,   // (--start-maximized) Start with a maximized window
			"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":        browserPopupsDefault,     // Disable pop-up blocking (--disable-popup-blocking)
			"gpu":           browserGpuDefault,        // (--disable-gpu) Disable GPU hardware acceleration, if necessary
			"javascript":    browserJSDefault,         // (--enable-javascript) Enable JavaScript, which is typically enabled in real user browsers
			"headless":      "",                       // Run in headless mode (--headless)
			"incognito":     "",                       // Run in incognito mode
			"disableDevShm": browserShmDefault,        // Disable /dev/shm use
		},
		BrowserFirefox: {
			"browserName":   BrowserFirefox,
			"initialWindow": browserStartMaxDefault, // Start with a maximized window
			//"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			//"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions": browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":     browserPopupsDefault,     // Disable pop-up blocking
			//"gpu":           browserGpuDefault,        // Disable GPU hardware acceleration, if necessary
			"javascript": browserJSDefault, // Enable JavaScript, which is typically enabled in real user browsers
		},
		BrowserChromium: {
			"browserName":   BrowserChromium,
			"windowSize":    browserWindowSizeDefault, // Set the window size to 1920x1080
			"initialWindow": browserStartMaxDefault,   // Start with a maximized window
			"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":        browserPopupsDefault,     // Disable pop-up blocking
			"gpu":           browserGpuDefault,        // Disable GPU hardware acceleration, if necessary
			"javascript":    browserJSDefault,         // Enable JavaScript, which is typically enabled in real user browsers
			"headless":      "",                       // Run in headless mode
			"incognito":     "",                       // Run in incognito mode
			"disableDevShm": browserShmDefault,        // Disable /dev/shm use
		},
	}
)

// ByCSSSelector Abstract type for a ByCSSSelector
const ByCSSSelector = selenium.ByCSSSelector

// ByID Abstract type for a ByID
const ByID = selenium.ByID

// ByName Abstract type for a ByName
const ByName = selenium.ByName

// ByLinkText Abstract type for a ByLinkText
const ByLinkText = selenium.ByLinkText

// ByPartialLinkText Abstract type for a ByPartialLinkText
const ByPartialLinkText = selenium.ByPartialLinkText

// ByTagName Abstract type for a ByTagName
const ByTagName = selenium.ByTagName

// ByClassName Abstract type for a ByClassName
const ByClassName = selenium.ByClassName

// ByXPATH Abstract type for a ByXPATH
const ByXPATH = selenium.ByXPATH

// Condition is an Alias used to pass a function as a parameter
type Condition func(wd WebDriver) (bool, error)

// Status represents the status of the WebDriver
type Status = selenium.Status

// Capabilities represents the capabilities of the WebDriver
type Capabilities map[string]interface{}

// SameSite represents the SameSite attribute of a cookie
type SameSite string

// Cookie represents a cookie
type Cookie = selenium.Cookie

// KeyAction represents a key action
type KeyAction map[string]interface{}

// PointerType represents the type of pointer
type PointerType string

// PointerAction represents a pointer action
type PointerAction map[string]interface{}

// PrintArgs represents the arguments for the Print method
type PrintArgs = selenium.PrintArgs

// WebDriver Abstract type for a WebDriver
type WebDriver = selenium.WebDriver

// WebElement Abstract type for a WebElement
type WebElement = selenium.WebElement

// Service Abstract type for a Service
type Service = selenium.Service

// Pool is a pool of VDI instances
type Pool struct {
	mu   sync.Mutex
	slot []SeleniumInstance
	busy map[int]bool // or status flags
}

// Init initializes the VDI pool
func (p *Pool) Init(size int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	//p.slot = make([]SeleniumInstance, size)
	p.busy = make(map[int]bool, size)
	for i := 0; i < size; i++ {
		p.busy[i] = false
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "VDI pool initialized with %d instances", size)
}

// NewPool creates a new pool of VDI instances
func NewPool(size int) *Pool {
	p := &Pool{}
	p.Init(size)
	return p
}

// Add adds a new VDI instance to the pool
func (p *Pool) Add(instance SeleniumInstance) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	if instance.Config.Host == "" {
		return fmt.Errorf("VDI instance host is empty")
	}
	if instance.Config.Port == 0 {
		return fmt.Errorf("VDI instance port is empty")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.slot = append(p.slot, instance)
	p.busy[len(p.slot)-1] = false
	return nil
}

// Remove removes a VDI instance from the pool
func (p *Pool) Remove(index int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= 0 && index < len(p.slot) {
		p.slot = append(p.slot[:index], p.slot[index+1:]...)
		delete(p.busy, index)
		cmn.DebugMsg(cmn.DbgLvlDebug2, "VDI instance removed from the pool")
	} else {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid index for VDI instance removal: %d", index)
	}
}

// Get returns a a reference to a VDI instance in the pool
func (p *Pool) Get(index int) (*SeleniumInstance, error) {
	if p == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= 0 && index < len(p.slot) {
		return &p.slot[index], nil
	}
	return nil, fmt.Errorf("invalid index for VDI instance: %d", index)
}

// Stop will stop the specified VDI instance
func (p *Pool) Stop(index int) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= 0 && index < len(p.slot) {
		if p.slot[index].Service != nil {
			err := p.slot[index].Service.Stop()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "stopping Selenium: %v", err)
			}
			cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium stopped successfully.")
		}
	}
	return nil
}

// StopAll will stop all VDI instances in the pool
func (p *Pool) StopAll() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.slot {
		if p.slot[i].Service != nil {
			err := p.slot[i].Service.Stop()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "stopping Selenium: %v", err)
			}
			cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium stopped successfully.")
		}
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "All Selenium instances stopped successfully.")
}

// Size returns the size of the pool
func (p *Pool) Size() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slot)
}

// Acquire acquires a VDI instance from the pool
func (p *Pool) Acquire() (int, SeleniumInstance, error) {
	if p == nil {
		return -1, SeleniumInstance{}, fmt.Errorf("acquire failed, pool is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := 0; i < len(p.slot); i++ {
		if p.slot[i].Config.Host == "" || p.slot[i].Config.Port == 0 {
			cmn.DebugMsg(cmn.DbgLvlError, "VDI instance %d is not initialized", i)
			continue
		}
		if !p.busy[i] {
			p.busy[i] = true
			return i, p.slot[i], nil
		}
	}
	return -1, SeleniumInstance{}, fmt.Errorf("acquire failed, no free VDI available out of %d slots", len(p.slot))
}

// Release releases a VDI instance back to the pool
func (p *Pool) Release(index int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if index >= 0 && index < len(p.busy) {
		p.busy[index] = false
	}
}

// Available returns the number of available VDI instances in the pool
func (p *Pool) Available() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	available := 0
	for _, busy := range p.busy {
		if !busy {
			available++
		}
	}
	return available
}

// SeleniumInstance holds a Selenium service and its configuration
type SeleniumInstance struct {
	Service *Service
	Config  cfg.Selenium
	//Mutex   *sync.Mutex
}

// ProcessContextInterface abstracts the necessary methods required by ConnectVDI.
type ProcessContextInterface interface {
	GetWebDriver() *WebDriver
	GetConfig() *cfg.Config // Assuming Config is a struct used inside ProcessContext
	GetVDIClosedFlag() *bool
	SetVDIClosedFlag(bool)
	GetVDIOperationMutex() *sync.Mutex
	GetVDIReturnedFlag() *bool
	SetVDIReturnedFlag(bool)
	GetVDIInstance() *SeleniumInstance
}

// WebDriverToSeleniumWebDriver converts a VDI WebDriver to a Selenium WebDriver
func WebDriverToSeleniumWebDriver(wd WebDriver) selenium.WebDriver {
	return wd
}

// SeleniumWebDriverToVDIWebDriver converts a Selenium WebDriver to a VDI WebDriver
func SeleniumWebDriverToVDIWebDriver(wd selenium.WebDriver) WebDriver {
	return wd
}

// WebElementToSeleniumWebElement converts a VDI WebElement to a Selenium WebElement
func WebElementToSeleniumWebElement(we WebElement) selenium.WebElement {
	return we
}

// SeleniumWebElementToVDIWebElement converts a Selenium WebElement to a VDI WebElement
func SeleniumWebElementToVDIWebElement(we selenium.WebElement) WebElement {
	return we
}

// NewVDIService is responsible for initializing Selenium Driver
// The commented out code could be used to initialize a local Selenium server
// instead of using only a container based one. However, I found that the container
// based Selenium server is more stable and reliable than the local one.
// and it's obviously easier to setup and more secure.
func NewVDIService(c cfg.Selenium) (*Service, error) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Configuring Selenium...")
	var service *Service

	if strings.TrimSpace(c.Host) == "" {
		c.Host = "selenium"
	}

	var protocol string
	if c.SSLMode == cmn.EnableStr {
		protocol = cmn.HTTPSStr
	} else {
		protocol = cmn.HTTPStr
	}

	var err error
	var retries int
	if c.UseService {
		for {
			service, err = selenium.NewSeleniumService(fmt.Sprintf(protocol+"://"+c.Host+":%d/wd/hub", c.Port), c.Port)
			if err == nil {
				cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium service started successfully.")
				break
			}
			cmn.DebugMsg(cmn.DbgLvlError, "starting Selenium service: %v", err)
			cmn.DebugMsg(cmn.DbgLvlDebug, "Check if Selenium is running on host '%s' at port '%d' and if that host is reachable from the system that is running thecrowler engine.", c.Host, c.Port)
			cmn.DebugMsg(cmn.DbgLvlInfo, "Retrying in 5 seconds...")
			retries++
			if retries > 5 {
				cmn.DebugMsg(cmn.DbgLvlError, "Exceeded maximum retries. Exiting...")
				break
			}
			time.Sleep(5 * time.Second)
		}
	}

	if err == nil {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium server started successfully.")
	}
	return service, err
}

// StopSelenium Stops the Selenium server instance (if local)
func StopSelenium(sel *selenium.Service) error {
	if sel == nil {
		return errors.New("selenium service is not running")
	}
	// Stop the Selenium vdi.WebDriver server instance
	err := sel.Stop()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "stopping Selenium: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium stopped successfully.")
	}
	return err
}

// ResetVDI resets the Selenium WebDriver and reinitializes the session with a fresh instance
// ResetVDI restarts the Selenium session for the current VDI instance only
func ResetVDI(ctx ProcessContextInterface, browserType int) error {
	if ctx == nil {
		return fmt.Errorf("ProcessContext is nil")
	}

	ctx.GetVDIOperationMutex().Lock()
	defer ctx.GetVDIOperationMutex().Unlock()

	// get current session
	vdi := ctx.GetWebDriver()

	// Quit current session
	(*vdi).Close()
	(*vdi).Quit()

	instance := ctx.GetVDIInstance()

	// Stop the current service
	/*
		if instance.Service != nil {
			err := instance.Service.Stop()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "ResetVDI: failed to stop Selenium service: %v", err)
			}
		}

		// Start new Selenium service
		service, err := NewVDIService(instance.Config)
		if err != nil {
			return fmt.Errorf("ResetVDI: failed to start Selenium service: %v", err)
		}
		instance.Service = service
	*/

	// Reconnect WebDriver
	wd, err := ConnectVDI(ctx, *instance, browserType)
	if err != nil {
		return fmt.Errorf("ResetVDI: failed to reconnect to VDI: %v", err)
	}

	*ctx.GetWebDriver() = wd
	ctx.SetVDIClosedFlag(false)
	ctx.SetVDIReturnedFlag(false)

	return nil
}

// ConnectVDI is responsible for connecting to the Selenium server instance
func ConnectVDI(ctx ProcessContextInterface, sel SeleniumInstance, browseType int) (WebDriver, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if sel.Config.Host == "" {
		return nil, errors.New("VDI instance host is empty")
	}
	if sel.Config.Port == 0 {
		return nil, errors.New("VDI instance port is empty")
	}

	// Get the required browser
	browser := strings.ToLower(strings.TrimSpace(sel.Config.Type))
	if browser == "" {
		browser = BrowserChrome
	}

	// If it's not being initialized yet, initialize the UserAgentsDB
	if cmn.UADB.IsEmpty() {
		err := cmn.UADB.InitUserAgentsDB()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to initialize UserAgentsDB: %v", err)
		}
	}

	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": browser}

	// Get process configuration
	pConfig := ctx.GetConfig()

	// Define the user agent string for a desktop Google Chrome browser
	var userAgent string

	// Get the user agent string from the UserAgentsDB
	userAgent = cmn.UADB.GetAgentByTypeAndOSAndBRG(pConfig.Crawler.Platform, pConfig.Crawler.BrowserPlatform, browser)

	// Parse the User Agent string for {random_int1}
	if strings.Contains(userAgent, "{random_int1}") {
		// Generates a random integer in the range [0, 999)
		randInt := rand.IntN(8000) // nolint:gosec // We are using "math/rand/v2" here
		userAgent = strings.ReplaceAll(userAgent, "{random_int1}", strconv.Itoa(randInt))
	}

	// Parse the User Agent string for {random_int2}
	if strings.Contains(userAgent, "{random_int2}") {
		// Generates a random integer in the range [0, 999)
		randInt := rand.IntN(999) // nolint:gosec // We are using "math/rand/v2" here
		userAgent = strings.ReplaceAll(userAgent, "{random_int2}", strconv.Itoa(randInt))
	}

	// Fallback in case the user agent is not found in the UserAgentsDB
	if userAgent == "" {
		if browseType == 0 {
			userAgent = cmn.UsrAgentStrMap[browser+"-desktop01"]
		} else if browseType == 1 {
			userAgent = cmn.UsrAgentStrMap[browser+"-mobile01"]
		}
	}

	var args []string

	// Populate the args slice based on the browser type
	keys := []string{"WindowSize", "initialWindow", "gpu", "headless", "javascript", "incognito"}
	for _, key := range keys {
		if value, ok := browserSettingsMap[sel.Config.Type][key]; ok && value != "" {
			args = append(args, value)
		}
	}

	// Append user-agent separately as it's a constant value
	args = append(args, "--user-agent="+userAgent)

	// CDP Config for Chrome/Chromium
	var cdpActive bool
	if browser == BrowserChrome || browser == BrowserChromium {
		args = append(args, "--no-first-run")
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Setting up Chrome DevTools Protocol (CDP)...")
		// Set the CDP port
		args = append(args, "--remote-debugging-port=9222")
		// Set the CDP host
		args = append(args, "--remote-debugging-address=0.0.0.0")
		// Ensure that the CDP is active
		//args = append(args, "--auto-open-devtools-for-tabs")
		cdpActive = true
	}

	// Append proxy settings if available
	if sel.Config.ProxyURL != "" {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Setting up Proxy...")
		args = append(args, "--proxy-server="+sel.Config.ProxyURL)
		args = append(args, "--force-proxy-for-all")
		// Get local network:
		localNet, err := cmn.DetectLocalNetwork()
		if err == nil {
			args = append(args, "--proxy-bypass-list=localhost,"+localNet)
		} else {
			args = append(args, "--proxy-bypass-list=localhost")
		}

		cmn.DebugMsg(cmn.DbgLvlDebug2, "Proxy settings: URL '%s', Exclusions: '%s'", sel.Config.ProxyURL, "localhost,"+localNet)

		/*
			proxyURL, err := url.Parse(sel.Config.ProxyURL)
			if err != nil {
				return nil, err
			}

			// extract port number if available
			if sel.Config.ProxyPort == 0 {
				proxyPort, _ := strconv.Atoi(proxyURL.Port())
				sel.Config.ProxyPort = proxyPort
			}
			proxyFQDN := proxyURL.Hostname()
			// add port if any available in the original proxy URL
			if proxyURL.Port() != "" {
				proxyFQDN = fmt.Sprintf("%s:%s", proxyFQDN, proxyURL.Port())
			}

			// convert proxy URL to socks5h:// format
			proxyURL.Scheme = "socks5h"
			ProxySocksURL := "socks5h://" + proxyURL.Hostname()

			// Set NoProxy []string to local host and networks
			NoProxy := []string{cmn.LoalhostStr}
			NoProxy = append(NoProxy, getLocalNetworks()...)

			// Proxy settings
			selProxy := selenium.Proxy{
				Type:          "manual",
				HTTP:          "http://" + proxyFQDN,
				SSL:           "https://" + proxyFQDN,
				SOCKS:         ProxySocksURL,
				SOCKSVersion:  5,
				SOCKSUsername: sel.Config.ProxyUser,
				SOCKSPassword: sel.Config.ProxyPass,
				SocksPort:     sel.Config.ProxyPort,
				NoProxy:       NoProxy,
			}
			caps.AddProxy(selProxy)
			cmn.DebugMsg(cmn.DbgLvlDebug, "Proxy settings: %v\n", selProxy)
		*/
	}

	// General settings
	if browser == BrowserChrome || browser == BrowserChromium {
		args = append(args, "--disable-software-rasterizer")
		args = append(args, "--use-fake-ui-for-media-stream")
	}

	// Avoid funny localizations/detections
	if browser == BrowserChrome || browser == BrowserChromium {
		// DNS over HTTPS (DoH) settings
		args = append(args, "--dns-prefetch-disable")
		//args = append(args, "--host-resolver-rules=MAP * 8.8.8.8")
		args = append(args, "--host-resolver-rules=MAP * 1.1.1.1")
		args = append(args, "--host-resolver-rules=MAP *:443")

		// Reduce geolocation leaks
		args = append(args, "--disable-geolocation")
		args = append(args, "--disable-notifications")
		args = append(args, "--disable-quic")
		args = append(args, "--disable-blink-features=AutomationControlled")

		// Reduce Hardware fingerprinting
		args = append(args, "--override-hardware-concurrency=4")
		args = append(args, "--override-device-memory=4")
		args = append(args, "--disable-features=Battery")

		// Reduce Browser fingerprinting
		args = append(args, "--disable-infobars")
		args = append(args, "--disable-extensions")
		args = append(args, "--disable-plugins")
		args = append(args, "--disable-plugins-discovery")
		args = append(args, "--disable-peer-to-peer")

		// Disable WebRTC
		args = append(args, "--disable-rtc-smoothness-algorithm")
		args = append(args, "--disable-webrtc")
		args = append(args, "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
		args = append(args, "--webrtc-ip-handling-policy=default_public_interface_only")
		args = append(args, "--webrtc-max-cpu-consumption-percentage=1")
		args = append(args, "--disable-webrtc-multiple-routes")
		args = append(args, "--disable-webrtc-hw-encoding")
		args = append(args, "--disable-webrtc-hw-decoding")
		args = append(args, "--disable-webrtc-encryption")
		args = append(args, "--disable-webrtc")

		// Disable WebUSB
		args = append(args, "--disable-webusb")

		// Disable WebBluetooth
		args = append(args, "--disable-web-bluetooth")

		// Disable Plugins
		args = append(args, "--disable-plugins")
		args = append(args, "--disable-extensions")

		// Disable Sandboxing
		/*
			args = append(args, "--no-sandbox")
			args = append(args, "--disable-dev-shm-usage")
		*/

		args = append(args, "--disable-popup-blocking")

		// Ensure Screen Resolution is correct
		args = append(args, "--force-device-scale-factor=1")

		// Enable/Disable JavaScript, Images, CSS, and Plugins requests
		// based on user's configuration
		if pConfig.Crawler.RequestImages {
			args = append(args, "--blink-settings=imagesEnabled=true")
		} else {
			args = append(args, "--blink-settings=imagesEnabled=false")
		}
		if pConfig.Crawler.RequestCSS {
			args = append(args, "--blink-settings=CSSImagesEnabled=true")
		} else {
			args = append(args, "--blink-settings=CSSImagesEnabled=false")
		}
		if pConfig.Crawler.RequestScripts {
			args = append(args, "--blink-settings=JavaScriptEnabled=true")
		} else {
			args = append(args, "--blink-settings=JavaScriptEnabled=false")
		}
		if pConfig.Crawler.RequestPlugins {
			args = append(args, "--blink-settings=PluginsEnabled=true")
		} else {
			args = append(args, "--blink-settings=PluginsEnabled=false")
		}

		// Reduce Cookie based tracking
		if pConfig.Crawler.ResetCookiesPolicy != "" && pConfig.Crawler.ResetCookiesPolicy != "none" {
			args = append(args, "--disable-site-isolation-trials")
			args = append(args, "--disable-features=IsolateOrigins,site-per-process")
			if pConfig.Crawler.NoThirdPartyCookies {
				args = append(args, "--disable-features=SameSiteByDefaultCookies")
			}
		}

		// Disable video auto-play:
		//args = append(args, "--autoplay-policy=user-required") // this option does't work and cause chrome/chromium to crash
	}

	// Append logging settings if available
	args = append(args, "--enable-logging")
	args = append(args, "--v=1")

	// Configure the download directory
	downloadDir := strings.TrimSpace(sel.Config.DownloadDir)
	if downloadDir == "" {
		downloadDir = "/tmp"
		sel.Config.DownloadDir = downloadDir
	}

	// Configure the browser preferences
	if browser == BrowserChrome || browser == BrowserChromium {
		chromePrefs := map[string]interface{}{
			"download.default_directory":               downloadDir,
			"download.prompt_for_download":             false, // Disable download prompt
			"profile.default_content_settings.popups":  0,     // Suppress popups
			"safebrowsing.enabled":                     true,  // Enable Safe Browsing
			"safebrowsing.disable_download_protection": false,
			"safebrowsing.disable_extension_blacklist": false,
			"safebrowsing.disable_automatic_downloads": false,
			"useAutomationExtension":                   false,
			"excludeSwitches":                          []string{"enable-automation"},
		}

		// Configure user content capabilities:
		if !pConfig.Crawler.RequestImages {
			// Disable images
			chromePrefs["profile.managed_default_content_settings.images"] = 2
		} else {
			// Allow images (default behavior)
			chromePrefs["profile.managed_default_content_settings.images"] = 1
		}
		if !pConfig.Crawler.RequestCSS {
			// Disable images and CSS
			chromePrefs["profile.managed_default_content_settings.stylesheets"] = 2
		} else {
			// Allow images and CSS (default behavior)
			chromePrefs["profile.managed_default_content_settings.stylesheets"] = 1
		}
		if !pConfig.Crawler.RequestScripts {
			// Disable scripts
			chromePrefs["profile.managed_default_content_settings.javascript"] = 2
		} else {
			// Allow scripts (default behavior)
			chromePrefs["profile.managed_default_content_settings.javascript"] = 1
		}
		if !pConfig.Crawler.RequestPlugins {
			// Disable plugins
			chromePrefs["profile.managed_default_content_settings.plugins"] = 2
		} else {
			// Allow plugins (default behavior)
			chromePrefs["profile.managed_default_content_settings.plugins"] = 1
		}

		// Finalize the capabilities
		caps.AddChrome(chrome.Capabilities{
			Args:  args,
			W3C:   true,
			Prefs: chromePrefs,
		})
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Chrome capabilities: %v\n", caps)
	} else if browser == "firefox" {
		firefoxCaps := map[string]interface{}{
			"browser.download.folderList":               2,
			"browser.download.dir":                      downloadDir,
			"browser.helperApps.neverAsk.saveToDisk":    "application/zip",
			"browser.download.manager.showWhenStarting": false,
		}

		// Configure user content capabilities:
		if !pConfig.Crawler.RequestImages {
			// Disable images
			firefoxCaps["permissions.default.image"] = 2
		} else {
			// Allow images (default behavior)
			firefoxCaps["permissions.default.image"] = 1
		}
		if !pConfig.Crawler.RequestCSS {
			// Disable images and CSS
			firefoxCaps["permissions.default.stylesheet"] = 2
		} else {
			// Allow images and CSS (default behavior)
			firefoxCaps["permissions.default.stylesheet"] = 1
		}
		if !pConfig.Crawler.RequestScripts {
			// Disable scripts
			firefoxCaps["permissions.default.script"] = 2
		} else {
			// Allow scripts (default behavior)
			firefoxCaps["permissions.default.script"] = 1
		}
		if !pConfig.Crawler.RequestPlugins {
			// Disable plugins
			firefoxCaps["permissions.default.object"] = 2
		} else {
			// Allow plugins (default behavior)
			firefoxCaps["permissions.default.object"] = 1
		}

		// Finalize the capabilities
		caps.AddFirefox(firefox.Capabilities{
			Args:  args,
			Prefs: firefoxCaps,
		})
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Firefox capabilities: %v\n", caps)
	}

	// Enable logging
	logSel := log.Capabilities{}
	const all = "ALL"
	logSel["performance"] = all
	logSel["browser"] = all
	caps.AddLogging(logSel)

	var protocol string
	if sel.Config.SSLMode == cmn.EnableStr {
		protocol = cmn.HTTPSStr
	} else {
		protocol = cmn.HTTPStr
	}

	if strings.TrimSpace(sel.Config.Host) == "" {
		sel.Config.Host = "crowler-vdi-1"
	}

	// Connect to the WebDriver instance running remotely.
	var wd WebDriver
	var err error
	maxRetry := 500
	for i := 0; i < maxRetry; i++ {
		urlType := "wd/hub"
		wd, err = selenium.NewRemote(caps, fmt.Sprintf(protocol+"://"+sel.Config.Host+":%d/"+urlType, sel.Config.Port))
		if err != nil {
			if i == 0 || (i%maxRetry) == 0 {
				cmn.DebugMsg(cmn.DbgLvlError, VDIConnError, err, 5)
			}
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "to connect to the VDI: %v, no more retries left, setting crawling as failed", err)
		return nil, err
	}

	// Post-connection settings
	setNavigatorProperties(&wd, sel.Config.Language, userAgent)

	// Retrieve Browser Configuration and display it for debugging purposes:
	result, err2 := getBrowserConfiguration(&wd)
	if err2 != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error executing script: %v\n", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Browser Configuration: %v\n", result)
	}

	err2 = addLoadListener(&wd)
	if err2 != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "adding Load Listener to the VDI session: %v", err)
	}

	// Configure CDP
	if cdpActive {
		/*
			blockVideo := `chrome.debugger.attach({tabId: chrome.devtools.inspectedWindow.tabId}, "1.0", () => {
				chrome.debugger.sendCommand({tabId: chrome.devtools.inspectedWindow.tabId}, "Network.enable");
				chrome.debugger.onEvent.addListener((source, message) => {
					if (message.method === "Network.requestIntercepted" && message.params.request.url.includes(".mp4")) {
						chrome.debugger.sendCommand({tabId: source.tabId}, "Network.continueInterceptedRequest", {
							interceptionId: message.params.interceptionId,
							errorReason: "BlockedByClient"
						});
					}
				});
			});`

			_, err2 := wd.ExecuteScript(blockVideo, nil)
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to configure browser to block video content: %v", err2)
			}
		*/
		_, err2 := wd.ExecuteChromeDPCommand("Network.enable", nil)
		if err2 != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable CDP Network domain: %v", err2)
		} else {
			_, err2 = wd.ExecuteChromeDPCommand("Network.setBlockedURLs", map[string]interface{}{
				"urls": []string{"*.mp4"},
			})
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to block .mp4 URLs: %v", err2)
			}
		}
	}

	ctx.SetVDIClosedFlag(false)
	ctx.SetVDIReturnedFlag(false)
	return wd, err
}

func addLoadListener(wd *WebDriver) error {
	script := `
        window.addEventListener('load', () => {
            try {
                Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});
                Object.defineProperty(window, 'RTCDataChannel', {value: undefined});
                Object.defineProperty(navigator, 'mediaDevices', {value: undefined});
                Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
                Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
                Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4});
                Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
            } catch (err) {
                console.error('Error applying browser settings on page load:', err);
            }
        });
    `

	_, err := (*wd).ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("error adding load listener: %v", err)
	}

	return nil
}

// SetGPU sets default GPU for session
func SetGPU(wd WebDriver) error {
	script := `
	const canvasProto = HTMLCanvasElement.prototype;
	const getContextOrig = canvasProto.getContext;

	canvasProto.getContext = function(type, attribs) {
		const ctx = getContextOrig.call(this, type, attribs);

		if (type === 'webgl' || type === 'webgl2') {
			const getExtOrig = ctx.getExtension;
			ctx.getExtension = function(ext) {
				if (ext === 'WEBGL_debug_renderer_info') {
					return getExtOrig.call(this, ext);
				}
				return getExtOrig.call(this, ext);
			};

			const getParamOrig = ctx.getParameter;
			ctx.getParameter = function(param) {
				const debugInfo = getExtOrig.call(this, 'WEBGL_debug_renderer_info');
				if (debugInfo) {
					if (param === debugInfo.UNMASKED_RENDERER_WEBGL) {
						return 'Intel(R) UHD Graphics 620';
					}
					if (param === debugInfo.UNMASKED_VENDOR_WEBGL) {
						return 'Intel Inc.';
					}
				}
				return getParamOrig.call(this, param);
			};
		}

		return ctx;
	};
	`

	_, err := wd.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("error reinforcing browser GPU settings: %v", err)
	}

	return nil
}

// ReinforceBrowserSettings applies additional settings to the WebDriver instance
func ReinforceBrowserSettings(wd WebDriver) error {
	// Reapply WebRTC and navigator spoofing settings
	script := `
        try {
			Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
			delete window._Selenium_IDE_Recorder;
			delete navigator.webdriver;
			delete navigator.webdriver.chrome;
			delete navigator.__proto__.webdriver;
		} catch (err) {
            console.error('Error reinforcing browser settings stage 1:', err);
        }

		try {
            Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});
            Object.defineProperty(window, 'RTCDataChannel', {value: undefined});
            Object.defineProperty(navigator, 'mediaDevices', {value: undefined});
            Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
            Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4});
            Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
			Object.defineProperty(navigator, 'getUserMedia', {value: undefined});
			Object.defineProperty(window, 'webkitRTCPeerConnection', {value: undefined});
		} catch (err) {
            console.error('Error reinforcing browser settings stage 2:', err);
        }

		try {
			HTMLCanvasElement.prototype.toDataURL = function() { return 'data:image/png;base64,fakemockdata'; };
		} catch (err) {
            console.error('Error reinforcing browser settings stage 3:', err);
        }

		try {
			const getParameter = WebGLRenderingContext.prototype.getParameter;
			WebGLRenderingContext.prototype.getParameter = function(parameter) {
				if (parameter === 37445) return 'Intel Inc.'; // Mock Vendor
				if (parameter === 37446) return 'Intel Iris OpenGL'; // Mock Renderer
				return getParameter(parameter);
			};
		} catch (err) {
			console.error('Error reinforcing browser settings stage 4:', err);
		}

		try {
			Object.defineProperty(window, 'devicePixelRatio', {
				get: function() { return 1; }
			});
		} catch (err) {
			console.error('Error reinforcing browser settings stage 5:', err);
		}

		try {
			Element.prototype.attachShadow = function() {
    			return null; // Disable shadow DOM if necessary
			};
		} catch (err) {
			console.error('Error reinforcing browser settings stage 5:', err);
		}

		try {
			const originalQuery = navigator.permissions.query;
			navigator.permissions.query = (parameters) => (
    			parameters.name === 'notifications'
        		? Promise.resolve({ state: 'denied' })
        		: originalQuery(parameters)
			);
		} catch (err) {
			console.error('Error reinforcing browser settings stage 6:', err);
		}

		try {
			Object.defineProperty(navigator, 'webdriver', {
    			get: () => undefined,
    			configurable: true,
			});

			Object.defineProperty(document, 'selenium-evaluate', { value: undefined });
			Error.stackTraceLimit = 0; // Limit error stack traces
        } catch (err) {
            console.error('Error reinforcing browser settings stage 7:', err);
        }

		try {
			Object.defineProperty(navigator, 'doNotTrack', {get: () => '1'}); // User enables Do Not Track
		} catch (err) {
			console.error('Error reinforcing browser settings stage 8:', err);
		}

		try {
			// Clean IndexedDB
            indexedDB.deleteDatabase('webdriver');
		} catch (err) {
			console.error('Error reinforcing browser settings stage 9:', err);
		}
    `

	_, err := wd.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("error reinforcing browser settings: %v", err)
	}

	script = `
	await page.evaluateOnNewDocument(() => {
	const canvasProto = HTMLCanvasElement.prototype;
	const getContextOrig = canvasProto.getContext;

	canvasProto.getContext = function(type, attribs) {
		const ctx = getContextOrig.call(this, type, attribs);

		if (type === 'webgl' || type === 'webgl2') {
		const getExtOrig = ctx.getExtension;
		ctx.getExtension = function(ext) {
			if (ext === 'WEBGL_debug_renderer_info') {
			return getExtOrig.call(this, ext); // Keep it available
			}
			return getExtOrig.call(this, ext);
		};

		const getParamOrig = ctx.getParameter;
		ctx.getParameter = function(param) {
			const debugInfo = getExtOrig.call(this, 'WEBGL_debug_renderer_info');
			if (debugInfo) {
			if (param === debugInfo.UNMASKED_RENDERER_WEBGL) {
				return 'Intel(R) UHD Graphics 620';
			}
			if (param === debugInfo.UNMASKED_VENDOR_WEBGL) {
				return 'Intel Inc.';
			}
			}
			return getParamOrig.call(this, param);
		};
		}

		return ctx;
	};
	});
	`

	_, err = wd.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("error reinforcing browser GPU settings: %v", err)
	}

	return nil
}

func getBrowserConfiguration(wd *WebDriver) (map[string]interface{}, error) {
	script := `
		return {
			// WebRTC settings
			RTCDataChannel: typeof RTCDataChannel,
			RTCPeerConnection: typeof RTCPeerConnection,
			getUserMedia: typeof navigator.mediaDevices?.getUserMedia,

			// Navigator properties
			userAgent: navigator.userAgent,
			languages: navigator.languages,
			deviceMemory: navigator.deviceMemory,
			hardwareConcurrency: navigator.hardwareConcurrency,
			webdriver: navigator.webdriver,
			platform: navigator.platform,
			plugins: navigator.plugins.length,

			// Screen dimensions
			screenWidth: window.screen.width,
			screenHeight: window.screen.height,
			innerWidth: window.innerWidth,
			innerHeight: window.innerHeight,
			outerWidth: window.outerWidth,
			outerHeight: window.outerHeight
		};
	`

	// Execute the script and get the result
	result, err := (*wd).ExecuteScript(script, nil)
	if err != nil {
		return nil, fmt.Errorf("error executing browser configuration script: %v", err)
	}

	// Convert result to a Go map and return
	config, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result format: %v", result)
	}

	return config, nil
}

func setNavigatorProperties(wd *WebDriver, lang, userAgent string) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	selectedLanguage := ""

	switch lang {
	case "en-gb":
		selectedLanguage = "['en-GB', 'en']"
	case "fr-fr":
		selectedLanguage = "['fr-FR', 'fr']"
	case "de-de":
		selectedLanguage = "['de-DE', 'de']"
	case "es-es":
		selectedLanguage = "['es-ES', 'es']"
	case "it-it":
		selectedLanguage = "['it-IT', 'it']"
	case "pt-pt":
		selectedLanguage = "['pt-PT', 'pt']"
	case "pt-br":
		selectedLanguage = "['pt-BR', 'pt']"
	case "ja-jp":
		selectedLanguage = "['ja-JP', 'ja']"
	case "ko-kr":
		selectedLanguage = "['ko-KR', 'ko']"
	case "zh-cn":
		selectedLanguage = "['zh-CN', 'zh']"
	case "zh-tw":
		selectedLanguage = "['zh-TW', 'zh']"
	default:
		selectedLanguage = "['en-US', 'en']"
	}

	// Set the navigator properties
	scripts := []string{
		"Object.defineProperty(navigator, 'webdriver', {get: () => undefined})",
		"window.navigator.chrome = {runtime: {}}",
		"Object.defineProperty(navigator, 'languages', {get: () => " + selectedLanguage + "})",
		"Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]})",
		"Object.defineProperty(navigator, 'deviceMemory', {get: () => 8})",        // Example device memory spoof
		"Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4})", // Example cores
		// Disable geolocation API
		"Object.defineProperty(navigator, 'geolocation', {get: () => null})",

		// Mock `navigator.doNotTrack`
		"Object.defineProperty(navigator, 'doNotTrack', {get: () => '1'})", // User enables Do Not Track

		// Mock `navigator.vendor` and `navigator.platform`
		"Object.defineProperty(navigator, 'vendor', {get: () => 'Google Inc.'})",
		"Object.defineProperty(navigator, 'platform', {get: () => 'Linux'})", // Mimic Linux platform

		// Spoof `navigator.deviceMemory`
		//"Object.defineProperty(navigator, 'deviceMemory', {get: () => 4})", // 4 GB memory

		// Spoof `navigator.hardwareConcurrency`
		//"Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4})", // 4 CPU cores

		//"Object.defineProperty(navigator, 'getBattery', {get: () => undefined})", // Disable battery API

		// Override screen width and height
		"Object.defineProperty(screen, 'width', {get: () => 1920})",
		"Object.defineProperty(screen, 'height', {get: () => 1080})",

		// Override window inner and outer dimensions
		"Object.defineProperty(window, 'innerWidth', {get: () => 1920})",
		"Object.defineProperty(window, 'innerHeight', {get: () => 1080})",
		"Object.defineProperty(window, 'outerWidth', {get: () => 1920})",
		"Object.defineProperty(window, 'outerHeight', {get: () => 1080})",

		// Mock `navigator.appVersion` and `navigator.userAgent`
		fmt.Sprintf("Object.defineProperty(navigator, 'appVersion', {get: () => '%s'})", userAgent),
		fmt.Sprintf("Object.defineProperty(navigator, 'userAgent', {get: () => '%s'})", userAgent),

		"Object.defineProperty(navigator, 'getBattery', {get: () => undefined})", // Disable battery API

		// Mock `navigator.connection`
		"Object.defineProperty(navigator, 'connection', {get: () => ({type: 'wifi', downlink: 10.0})})", // Mimic WiFi connection

		// Disable WebRTC APIs to prevent IP leakage
		"Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});",
		"Object.defineProperty(window, 'RTCDataChannel', {value: undefined});",
	}
	for _, script := range scripts {
		_, err := (*wd).ExecuteScript(script, nil)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting navigator properties: %v", err)
		}
	}
}

// ReturnVDIInstance is responsible for returning the Selenium server instance
func ReturnVDIInstance(_ *sync.WaitGroup, pCtx ProcessContextInterface, sel *SeleniumInstance, releaseVDI chan<- SeleniumInstance) {
	if pCtx == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid parameters: ProcessContext is nil")
		return
	}
	// Get Process VDI Operation Mutex
	pVDIMutex := pCtx.GetVDIOperationMutex()
	pVDIMutex.Lock()
	defer pVDIMutex.Unlock()

	// Get process VDIReturned flag
	pVDIReturned := pCtx.GetVDIReturnedFlag()

	// Prevent multiple return attempts
	if *pVDIReturned {
		cmn.DebugMsg(cmn.DbgLvlDebug, "VDI session already returned, skipping duplicate return.")
		return
	}
	*pVDIReturned = true

	cmn.DebugMsg(cmn.DbgLvlDebug2, "Returning VDI object instance...")

	if sel == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid parameters: VDIInstance is nil")
		return
	}

	// Quit Selenium WebDriver session BEFORE returning it to the pool
	cmn.DebugMsg(cmn.DbgLvlDebug, "Quitting VDI session before returning instance...")
	QuitSelenium(pCtx)

	// Get the process Selenium instance
	pSel := pCtx.GetVDIInstance() // ctx.Sel

	// Ensure it is returned to the correct queue
	if pSel != nil {
		releaseVDI <- (*sel) // no need to return sel, it's returned by the caller of CrawlWebsite
		cmn.DebugMsg(cmn.DbgLvlDebug2, "VDI object instance successfully returned.")
		time.Sleep(1 * time.Second)
	} else {
		cmn.DebugMsg(cmn.DbgLvlError, "Attempted to return a nil VDI instance!")
	}
}

// QuitSelenium is responsible for quitting the Selenium server instance
func QuitSelenium(ctx ProcessContextInterface) {
	if ctx == nil {
		return
	}
	// Get process VDI Close flag
	pVDIClosed := ctx.GetVDIClosedFlag() // ctx.SelClosed

	if *pVDIClosed {
		return
	}
	*pVDIClosed = true

	// Get the process WebDriver instance
	wd := ctx.GetWebDriver()

	if wd == nil || *wd == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Attempted to quit nil WebDriver session!")
		return
	}
	cmn.DebugMsg(cmn.DbgLvlDebug, "Checking WebDriver session before quitting...")

	// Ensure we aren't quitting an already-closed session
	_, err := (*wd).CurrentURL()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug1, "WebDriver session already invalid: %v", err)
		return
	}

	// Force quit session before returning instance
	cmn.DebugMsg(cmn.DbgLvlDebug, "Quitting WebDriver session now...")
	err = (*wd).Close()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to close WebDriver: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "WebDriver closed successfully.")
		time.Sleep(1 * time.Second)
		err = (*wd).Quit()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to quit WebDriver: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "WebDriver quitted successfully.")
		}
	}
}
