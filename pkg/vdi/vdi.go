// Package vdi is an abstraction layer for the Virtual Desktop Infrastructure (VDI) used by the Crowler.
package vdi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	selenium "github.com/go-auxiliaries/selenium"
	"github.com/go-auxiliaries/selenium/chrome"
	"github.com/go-auxiliaries/selenium/firefox"
	"github.com/go-auxiliaries/selenium/log"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
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
	mu     sync.Mutex
	slot   []SeleniumInstance
	busy   map[int]bool // or status flags
	leases map[int]*Lease
	wakeup chan struct{}
}

// Lease represents exclusive ownership of one VDI pool slot.
//
// Index and VDIName identify the acquired slot. Call Release when the VDI is no
// longer needed; repeated calls are safe.
type Lease struct {
	Index   int
	VDIName string

	pool        *Pool
	index       int
	vdiName     string
	releaseOnce sync.Once
	releaseErr  error
}

// Init initializes the VDI pool
func (p *Pool) Init(size int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	//p.slot = make([]SeleniumInstance, size)
	p.busy = make(map[int]bool, size)
	p.leases = make(map[int]*Lease, size)
	p.wakeup = make(chan struct{})
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
	p.ensureStateLocked()
	p.slot = append(p.slot, instance)
	p.busy[len(p.slot)-1] = false
	p.signalLocked()
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

// AcquireContext acquires a VDI instance from the pool based on the provided
// comma-separated list of allowed VDI names. An empty list allows any valid
// instance. If every matching instance is busy, AcquireContext waits until the
// context is canceled or a matching instance is released.
func (p *Pool) AcquireContext(ctx context.Context, allowedNames string) (*Lease, SeleniumInstance, error) {
	if p == nil {
		return nil, SeleniumInstance{}, fmt.Errorf("acquire failed, pool is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, SeleniumInstance{}, err
	}

	allowedNames = strings.TrimSpace(allowedNames)
	allowed := splitAllowedNames(allowedNames)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-VDI-Acquire] Trying to acquire VDI: %s", allowedNames)

	for {
		p.mu.Lock()
		p.ensureStateLocked()

		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, SeleniumInstance{}, err
		}

		matchingInstance := false
		for i := range p.slot {
			instance := p.slot[i]
			if instance.Config.Host == "" || instance.Config.Port == 0 {
				cmn.DebugMsg(cmn.DbgLvlError, "VDI instance %d is not initialized", i)
				continue
			}
			if !nameAllowed(instance.Config.Name, allowed) {
				continue
			}

			matchingInstance = true
			if p.busy[i] {
				continue
			}

			lease := &Lease{
				Index:   i,
				VDIName: instance.Config.Name,
				pool:    p,
				index:   i,
				vdiName: instance.Config.Name,
			}
			p.busy[i] = true
			p.leases[i] = lease
			p.mu.Unlock()
			return lease, instance, nil
		}

		if !matchingInstance {
			p.mu.Unlock()
			return nil, SeleniumInstance{}, fmt.Errorf("acquire failed, no matching VDI available out of %d slots", len(p.slot))
		}

		wakeup := p.wakeup
		p.mu.Unlock()
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Acquire] No matching VDI instances available in the pool, waiting...")

		select {
		case <-ctx.Done():
			return nil, SeleniumInstance{}, ctx.Err()
		case <-wakeup:
		}
	}
}

// Acquire acquires a VDI instance using a background context. It is retained
// for compatibility with callers that release instances by index and name.
func (p *Pool) Acquire(allowedNames string) (int, SeleniumInstance, error) {
	lease, instance, err := p.AcquireContext(context.Background(), allowedNames)
	if err != nil {
		return -1, SeleniumInstance{}, err
	}
	return lease.Index, instance, nil
}

// Release relinquishes this lease. It is safe to call Release more than once.
// A stale or altered lease cannot release a slot owned by another acquisition.
func (l *Lease) Release() error {
	if l == nil || l.pool == nil {
		return nil
	}

	l.releaseOnce.Do(func() {
		l.releaseErr = l.release()
	})
	return l.releaseErr
}

func (l *Lease) release() error {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()

	if l.Index != l.index || !strings.EqualFold(strings.TrimSpace(l.VDIName), strings.TrimSpace(l.vdiName)) {
		return fmt.Errorf("release failed, lease identity was modified")
	}
	if l.index < 0 || l.index >= len(p.slot) {
		return fmt.Errorf("release failed, invalid VDI index: %d", l.index)
	}
	if !strings.EqualFold(strings.TrimSpace(l.vdiName), strings.TrimSpace(p.slot[l.index].Config.Name)) {
		return fmt.Errorf("release failed, VDI name %q does not match slot %d", l.vdiName, l.index)
	}

	active, ok := p.leases[l.index]
	if !ok {
		return nil
	}
	if active != l {
		return fmt.Errorf("release failed, VDI slot %d is owned by another lease", l.index)
	}

	p.releaseLocked(l.index)
	return nil
}

// Release releases a VDI instance acquired through the compatibility Acquire
// method. The supplied name may be the acquired name or the original allowed
// name list, but Release never redirects the request to another pool index.
func (p *Pool) Release(index int, vdiName string) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()

	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Release] Received request to release VDI instance with index %d and name '%s' back to pool", index, vdiName)
	if index < 0 || index >= len(p.slot) {
		return
	}
	if strings.TrimSpace(vdiName) != "" && !nameAllowed(p.slot[index].Config.Name, splitAllowedNames(vdiName)) {
		return
	}
	if _, ok := p.leases[index]; !ok {
		return
	}

	p.releaseLocked(index)
}

func (p *Pool) ensureStateLocked() {
	if p.busy == nil {
		p.busy = make(map[int]bool, len(p.slot))
	}
	if p.leases == nil {
		p.leases = make(map[int]*Lease, len(p.slot))
	}
	if p.wakeup == nil {
		p.wakeup = make(chan struct{})
	}
}

func (p *Pool) releaseLocked(index int) {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Release] Releasing VDI instance '%s' with index %d back to pool", p.slot[index].Config.Name, index)
	delete(p.leases, index)
	p.busy[index] = false
	p.signalLocked()
}

func (p *Pool) signalLocked() {
	close(p.wakeup)
	p.wakeup = make(chan struct{})
}

func splitAllowedNames(allowedNames string) []string {
	if strings.TrimSpace(allowedNames) == "" {
		return nil
	}

	names := strings.Split(allowedNames, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	return names
}

func nameAllowed(name string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	name = strings.TrimSpace(name)
	for _, allowedName := range allowed {
		if strings.EqualFold(allowedName, name) {
			return true
		}
	}
	return false
}

// This is a special implementation for internal use-only
func checkAvailable(p *Pool) int {
	if p == nil {
		return -1
	}

	available := 0
	for _, busy := range p.busy {
		if !busy {
			available++
		}
	}
	return available
}

// Available returns the number of available VDI instances in the pool
func (p *Pool) Available() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	return checkAvailable(p)
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
	GetSource() *cdb.Source // Assuming Source is a struct used inside ProcessContext
	GetVDIClosedFlag() *bool
	SetVDIClosedFlag(bool)
	GetVDIOperationMutex() *sync.Mutex
	GetVDIReturnedFlag() *bool
	SetVDIReturnedFlag(bool)
	GetVDIInstance() *SeleniumInstance
	GetVDIAbortError() error
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

	if vdi != nil && *vdi != nil {
		// Quit current session
		_ = (*vdi).Close()
		_ = (*vdi).Quit()
	}

	instance := ctx.GetVDIInstance()

	if instance == nil {
		return fmt.Errorf("ResetVDI: SeleniumInstance is nil")
	}

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

type ProxySettings struct {
	ProxyURL  string
	ProxyUser string
	ProxyPass string
}

var seleniumHTTPClientOnce sync.Once

func configureSeleniumHTTPClient(sessionCreationTimeout time.Duration) {
	seleniumHTTPClientOnce.Do(func() {
		client := selenium.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}

		baseTransport := client.Transport
		if baseTransport == nil {
			baseTransport = http.DefaultTransport
		}

		clonedClient := *client
		clonedClient.Transport = &seleniumTransport{
			base:                   baseTransport,
			sessionCreationTimeout: sessionCreationTimeout,
		}

		selenium.HTTPClient = &clonedClient
	})
}

func waitForVDIRetry(ctx ProcessContextInterface, delay time.Duration) error {
	if abortErr := ctx.GetVDIAbortError(); abortErr != nil {
		return abortErr
	}

	timer := time.NewTimer(delay)
	ticker := time.NewTicker(250 * time.Millisecond)

	defer timer.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return nil

		case <-ticker.C:
			if abortErr := ctx.GetVDIAbortError(); abortErr != nil {
				return abortErr
			}
		}
	}
}

// baseBrowserArgs returns the launch arguments derived from
// browserSettingsMap for the given Selenium browser type.
//
// Preservation note (PR1, VDI-010): the map key is lowercase "windowSize".
// The previous lookup used "WindowSize", which never matched, so
// --window-size=1920,1080 was silently dropped for Chrome/Chromium. Only the
// key spelling is corrected; the map contents and selection semantics are
// unchanged.
func baseBrowserArgs(selType string) []string {
	var args []string
	keys := []string{"windowSize", "initialWindow", "gpu", "headless", "javascript", "incognito"}
	for _, key := range keys {
		if value, ok := browserSettingsMap[selType][key]; ok && value != "" {
			args = append(args, value)
		}
	}
	return args
}

// chromeDisabledFeaturesValue returns the single comma-joined value for the
// Chromium --disable-features switch, preserving every feature the
// configuration intends to disable.
//
// Preservation note (PR1, VDI-010): the previous code appended up to four
// separate --disable-features flags (UserAgentClientHint, Battery,
// IsolateOrigins/site-per-process, SameSiteByDefaultCookies). Chromium honors
// a single value per switch, so only the last flag took effect and the rest
// were silently lost. The union of intended features is unchanged; only the
// encoding is corrected to one flag.
func chromeDisabledFeaturesValue(resetCookiesPolicy string, noThirdPartyCookies bool) string {
	features := []string{"UserAgentClientHint", "Battery"}
	if resetCookiesPolicy != "" && resetCookiesPolicy != "none" {
		features = append(features, "IsolateOrigins", "site-per-process")
		if noThirdPartyCookies {
			features = append(features, "SameSiteByDefaultCookies")
		}
	}
	return strings.Join(features, ",")
}

// chromeBlinkSettingsValue returns the single comma-joined value for the
// Chromium --blink-settings switch, preserving all four resource policies.
//
// Preservation note (PR1, VDI-010): the previous code appended four separate
// --blink-settings flags, so only the last one (PluginsEnabled) took effect.
// Key spellings are kept exactly as configured; validity of individual keys
// against the pinned Chromium is covered by real-browser integration
// validation (VDI-021), not asserted here.
func chromeBlinkSettingsValue(requestImages, requestCSS, requestScripts, requestPlugins bool) string {
	return fmt.Sprintf("imagesEnabled=%t,CSSImagesEnabled=%t,JavaScriptEnabled=%t,PluginsEnabled=%t",
		requestImages, requestCSS, requestScripts, requestPlugins)
}

// resolveEffectiveProxyURL applies the existing source-vs-VDI proxy
// precedence without changing its semantics: an absent source key (ok=false,
// including source-JSON unmarshal failure at the caller) or an empty source
// value inherits the VDI default; the exact sentinel "no_proxy" forces a
// direct connection; otherwise the trimmed source value wins.
func resolveEffectiveProxyURL(sourceProxy string, ok bool, vdiProxy string) string {
	if !ok {
		return vdiProxy
	}
	trimmed := strings.TrimSpace(sourceProxy)
	if trimmed == "" {
		return vdiProxy
	}
	if trimmed == "no_proxy" {
		return ""
	}
	return trimmed
}

// buildChromeCapabilities assembles goog:chromeOptions with ExcludeSwitches
// at the capability level, where ChromeDriver reads it.
//
// Preservation note (PR1, VDI-007): "excludeSwitches" was previously placed
// inside the prefs dictionary, where ChromeDriver ignores it, so
// --enable-automation was never excluded. "useAutomationExtension" has no
// supported equivalent in this Selenium binding (go-auxiliaries/selenium
// v0.9.10 chrome.Capabilities exposes only ExcludeSwitches) or in modern
// ChromeDriver, so it is dropped from prefs where it was inert. The
// automation-hiding intent is preserved through ExcludeSwitches together
// with the existing --disable-blink-features=AutomationControlled flag.
func buildChromeCapabilities(args []string, prefs map[string]interface{}) chrome.Capabilities {
	return chrome.Capabilities{
		Args:            args,
		ExcludeSwitches: []string{"enable-automation"},
		W3C:             true,
		Prefs:           prefs,
	}
}

// buildLaunchArgs assembles the browser command-line arguments for a VDI
// session. It is a pure function of its inputs (apart from the local-network
// probe used for the proxy bypass list) so launch-argument defects can be
// covered by unit tests without a live Selenium server.
//
// Preservation notes (PR1, VDI-007/VDI-010): every previously emitted
// capability is still emitted. Changed encodings only:
//   - window size now resolves via baseBrowserArgs (case fix);
//   - one --disable-features flag (union preserved);
//   - one --blink-settings flag (all four policies preserved);
//   - the --proxy-server flag uses the resolved effective proxy;
//   - exact-duplicate flags (--disable-webrtc, --disable-plugins,
//     --disable-extensions) appear once;
//   - the two --host-resolver-rules flags are removed (MAP * <ip> rewrites
//     every hostname to a fixed destination instead of selecting a resolver,
//     and the second entry was malformed); --dns-prefetch-disable is retained
//     and proxy egress still carries DNS when configured.
func buildLaunchArgs(browser, selType, userAgent string, crawler cfg.Crawler, effectiveProxy string) []string {
	var args []string

	// Base map entries (window size, maximized, gpu, headless, javascript,
	// incognito).
	args = append(args, baseBrowserArgs(selType)...)

	// User agent.
	args = append(args, "--user-agent="+userAgent)

	if browser == BrowserChrome || browser == BrowserChromium {
		args = append(args, "--incognito")                              // Run in incognito mode
		args = append(args, "--disable-client-side-phishing-detection") // Disable client-side phishing detection
		args = append(args, "--disable-component-update")               // Disable component updates
		args = append(args, "--no-first-run")
	}

	// Proxy settings use the resolved effective proxy (VDI-009 P0 portion:
	// the previous code resolved the per-source value but then emitted the
	// VDI-global string in the flag).
	if effectiveProxy != "" {
		args = append(args, "--proxy-server="+effectiveProxy)
		args = append(args, "--force-proxy-for-all")
		// Get local network:
		localNet, err := cmn.DetectLocalNetwork()
		if err == nil {
			args = append(args, "--proxy-bypass-list=localhost,"+localNet)
		} else {
			args = append(args, "--proxy-bypass-list=localhost")
		}
	}

	// General settings
	if browser == BrowserChrome || browser == BrowserChromium {
		args = append(args, "--disable-software-rasterizer")
		args = append(args, "--use-fake-ui-for-media-stream")
	}

	// Avoid funny localizations/detections
	if browser == BrowserChrome || browser == BrowserChromium {
		// DNS privacy: prefetch disabling is retained. The previous pair of
		// --host-resolver-rules flags is intentionally not emitted (see the
		// function documentation). Proxy egress carries DNS when a proxy is
		// configured, system DNS otherwise.
		args = append(args, "--dns-prefetch-disable")

		// Reduce geolocation leaks
		args = append(args, "--disable-geolocation")
		args = append(args, "--disable-notifications")
		args = append(args, "--disable-quic")
		args = append(args, "--disable-blink-features=AutomationControlled")

		// Reduce Hardware fingerprinting
		args = append(args, "--override-hardware-concurrency=4")
		args = append(args, "--override-device-memory=4")

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

		// Disable WebUSB
		args = append(args, "--disable-webusb")

		// Disable WebBluetooth
		args = append(args, "--disable-web-bluetooth")

		args = append(args, "--disable-popup-blocking")

		// Ensure Screen Resolution is correct
		args = append(args, "--force-device-scale-factor=1")

		// Enable/Disable JavaScript, Images, CSS, and Plugins requests
		// based on user's configuration (single flag; all four preserved).
		args = append(args, "--blink-settings="+chromeBlinkSettingsValue(
			crawler.RequestImages, crawler.RequestCSS, crawler.RequestScripts, crawler.RequestPlugins))

		// Reduce Cookie based tracking
		if crawler.ResetCookiesPolicy != "" && crawler.ResetCookiesPolicy != "none" {
			args = append(args, "--disable-site-isolation-trials")
		}
		// Single --disable-features flag (union of intended disables).
		args = append(args, "--disable-features="+chromeDisabledFeaturesValue(
			crawler.ResetCookiesPolicy, crawler.NoThirdPartyCookies))

		// Disable video auto-play:
		//args = append(args, "--autoplay-policy=user-required") // this option does't work and cause chrome/chromium to crash
	}

	// Append logging settings if available
	args = append(args, "--enable-logging")
	args = append(args, "--v=1")

	return args
}

// ConnectVDI is responsible for connecting to the Selenium server instance
func ConnectVDI(ctx ProcessContextInterface, sel SeleniumInstance, browseType int) (WebDriver, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}

	// Validate the VDI configuration
	if sel.Config.Host == "" {
		return nil, errors.New("VDI instance host is empty")
	}
	if sel.Config.Port == 0 {
		return nil, errors.New("VDI instance port is empty")
	}

	var err error

	// Get the required browser
	browser := strings.ToLower(strings.TrimSpace(sel.Config.Type))
	if browser == "" {
		browser = BrowserChrome
	}

	// If it's not being initialized yet, initialize the UserAgentsDB
	if cmn.UADB.IsEmpty() {
		err := cmn.UADB.InitUserAgentsDB()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "failed to initialize UserAgentsDB: %v", err)
		}
	}

	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": browser}

	// Get process configuration
	pConfig := ctx.GetConfig()

	VDIHost := sel.Config.Host
	if pConfig.Crawler.ResolveVDIDNS {
		// Pre-resolve the VDI DNS if required
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] Pre-resolving VDI DNS for host '%s'...", sel.Config.Host)
		VDIHost, err = cmn.ResolveDNS(sel.Config.Host)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "failed to pre-resolve VDI DNS for host '%s': %v", sel.Config.Host, err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] VDI DNS for host '%s' resolved successfully.", sel.Config.Host)
		}
	}

	// Select the User-Agent for this session event: shared mechanic for
	// connection-time and request-time paths (VDI-003/VDI-004). Family
	// mapping (chromium shares chrome's group), template expansion, and
	// profile-aware fallback are centralized in common; rotation policy
	// (when selection runs) is unchanged. resolvedOS is the single
	// advertised-OS key every surface in this event derives from.
	userAgent, resolvedOS, fallbackUsed := cmn.UADB.SelectUserAgent(
		pConfig.Crawler.Platform, pConfig.Crawler.BrowserPlatform, browser, browseType == 1, rand.IntN)
	if note := cmn.UserAgentCompatibilityNote(userAgent, browser, fallbackUsed); note != "" {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] User-Agent note on '%s': %s", sel.Config.Name, note)
	}
	ident := MapAdvertisedOSIdentity(resolvedOS, browseType == 1)
	chromeMajor, _ := ChromeMajorVersion(userAgent)

	// Resolve the effective proxy preserving the existing source-vs-VDI
	// precedence (absent/empty inherits the VDI default; "no_proxy" forces a
	// direct connection; otherwise the trimmed source value wins).
	var sourceProxy string
	var sourceProxyOK bool
	if rawSource := ctx.GetSource(); rawSource != nil && rawSource.Config != nil {
		var sourceConfig map[string]interface{}
		if err := json.Unmarshal(*rawSource.Config, &sourceConfig); err == nil {
			if val, ok := sourceConfig["proxy_url"].(string); ok {
				sourceProxy, sourceProxyOK = val, true
			}
		}
	}
	effectiveProxy := resolveEffectiveProxyURL(sourceProxy, sourceProxyOK, sel.Config.ProxyURL)

	// Assemble the browser launch arguments with the shared builder so
	// multi-value switches stay coherent (single --disable-features and
	// --blink-settings values) and every fix is unit-testable without a live
	// Selenium server. (before I used sel.Config.Type for the 2nd parameter here, but I think using browser is a better idea)
	args := buildLaunchArgs(browser, browser, userAgent, pConfig.Crawler, effectiveProxy)

	// CDP Config for Chrome/Chromium
	var cdpActive bool
	if browser == BrowserChrome || browser == BrowserChromium {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] Setting up Chrome DevTools Protocol (CDP) for '%s'...", sel.Config.Name)
		// Set the CDP port
		//args = append(args, "--remote-debugging-port=9222")
		// Set the CDP host
		//args = append(args, "--remote-debugging-address=0.0.0.0")
		// Ensure that the CDP is active
		//args = append(args, "--auto-open-devtools-for-tabs")
		cdpActive = true
	}

	// Proxy observability for the resolved effective value (same shape as before).
	if effectiveProxy != "" {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] Setting up Proxy for '%s'...", sel.Config.Name)
		// Get local network:
		localNet, err := cmn.DetectLocalNetwork()
		if err == nil {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] Proxy settings: URL '%s', Exclusions: '%s' for '%s'", effectiveProxy, "localhost,"+localNet, sel.Config.Name)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] Proxy settings: URL '%s', Exclusions: '%s' for '%s'", effectiveProxy, "localhost", sel.Config.Name)
		}

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
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] No proxy settings for '%s'", sel.Config.Name)
	}

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

		// Finalize the capabilities (ExcludeSwitches lives at the
		// goog:chromeOptions level via buildChromeCapabilities, where
		// ChromeDriver reads it, not inside prefs).
		caps.AddChrome(buildChromeCapabilities(args, chromePrefs))
		cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-ConnectVDI] Chrome capabilities: %v\n", caps)
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
		cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-ConnectVDI] Firefox capabilities: %v\n", caps)
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
	maxRetry := 500

	// Install the Selenium transport once before entering the retry loop.
	// Only session creation requests are bounded by this timeout.
	configureSeleniumHTTPClient(60 * time.Second)
	for i := 0; i < maxRetry; i++ {
		// The crawler monitor may already have decided that this pipeline
		// must terminate.
		if abortErr := ctx.GetVDIAbortError(); abortErr != nil {
			return nil, abortErr
		}

		urlType := "wd/hub"

		wd, err = selenium.NewRemote(caps, fmt.Sprintf(protocol+"://"+VDIHost+":%d/"+urlType, sel.Config.Port))
		// The stale state may have been raised while NewRemote was waiting
		// for the Selenium server.
		if abortErr := ctx.GetVDIAbortError(); abortErr != nil {
			// NewRemote may have succeeded at approximately the same time
			// that the crawler declared the pipeline stale. Do not leak
			// that newly-created session.
			if wd != nil {
				_ = wd.Quit()
			}
			return nil, abortErr
		}

		if err == nil {
			break
		}

		if (i == 0) || ((i % maxRetry) == 0) {
			cmn.DebugMsg(cmn.DbgLvlError, VDIConnError, err, 5)
		}

		if retryErr := waitForVDIRetry(ctx, 5*time.Second); retryErr != nil {
			return nil, retryErr
		}
	}
	// err may still be non-nil here (for example when we failed all the attempts), but we will return it to the caller for handling.
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "to connect to the VDI: %v, no more retries left, setting crawling as failed for '%s'", err, sel.Config.Name)
		return nil, err
	}
	// Sleep a few milliseconds to give time for the session to be fully established before we start sending commands to it
	time.Sleep(500 * time.Millisecond)
	// Inject anti-detection patch for the identity selected for this event.
	// ident/chromeMajor derive from the same selection so UA, Client Hints,
	// and navigator surfaces agree; rotation-time replacement is owned by
	// VDI-014 (see antiDetectionScript lifecycle note).
	err = InjectAntiDetectionPatches(wd, userAgent, ident, chromeMajor)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ConnectVDI] Patch injection failed on '%s': %v", sel.Config.Name, err)
		// Check if we have lost the session
		if strings.Contains(strings.ToLower(err.Error()), "invalid session id") {
			// We lost the session (this is a Selenium bug sometimes)
			return nil, err
		}
	}

	// Post-connection settings
	if !(*ctx.GetVDIReturnedFlag()) {
		setNavigatorProperties(wd, sel.Config.Language, userAgent, ident)
	}

	// Retrieve Browser Configuration and display it for debugging purposes:
	result, err2 := getBrowserConfiguration(wd)
	if err2 != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error executing script: %v\n", err)
		// Check if the error contains: "invalid session id"
		if strings.Contains(err2.Error(), "invalid session id") {
			return nil, fmt.Errorf("VDI session is invalid, something closed it, cannot continue")
		}
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ConnectVDI] Browser Configuration on '%s': %v\n", sel.Config.Name, result)
	}

	err2 = addLoadListener(wd)
	if err2 != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "adding Load Listener to the VDI session: %v", err)
		// Check if the error contains: "invalid session id"
		if strings.Contains(err2.Error(), "invalid session id") {
			return nil, fmt.Errorf("VDI session is invalid, something closed it, cannot continue")
		}
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
		err2 := EnableNetwork(wd, ctx.GetConfig().Crawler.CDPDelay, nil)
		if err2 != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "failed to enable CDP Network domain: %v", err2)
			// Check if the error contains: "invalid session id"
			if strings.Contains(err2.Error(), "invalid session id") {
				return nil, fmt.Errorf("VDI session is invalid, something closed it, cannot continue")
			}
		} else {
			err2 = SetBlockedURLs(wd, ctx.GetConfig().Crawler.CDPDelay, []string{"*.mp4"})
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "failed to block .mp4 URLs: %v", err2)
			}
		}
	}

	ctx.SetVDIClosedFlag(false)
	ctx.SetVDIReturnedFlag(false)
	return wd, err
}

func addLoadListener(wd WebDriver) error {
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

	_, err := wd.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("adding load listener: %v", err)
	}

	return nil
}

// GPUPatch sets default GPU for session
func GPUPatch(wd WebDriver) error {
	script := `
	HTMLCanvasElement.prototype.getContext = (function(orig) {
	return function(type, attribs) {
		const ctx = orig.call(this, type, attribs);
		if (type === 'webgl' || type === 'webgl2') {
		const ext = ctx.getExtension('WEBGL_debug_renderer_info');
		if (ext) {
			const getParamOrig = ctx.getParameter.bind(ctx);
			ctx.getParameter = function(param) {
			if (param === ext.UNMASKED_RENDERER_WEBGL) return 'Intel(R) UHD Graphics 620';
			if (param === ext.UNMASKED_VENDOR_WEBGL) return 'Intel Inc.';
			return getParamOrig(param);
			};
		}
		}
		return ctx;
	};
	})(HTMLCanvasElement.prototype.getContext);
	`

	_, err := wd.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("error reinforcing browser GPU settings: %v", err)
	}

	/* Old patch:
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
	*/

	return nil
}

// ReinforceBrowserSettings applies additional settings to the WebDriver instance
func ReinforceBrowserSettings(wd WebDriver) error {
	if wd == nil {
		return fmt.Errorf("WebDriver is nil")
	}

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

	return nil
}

func getBrowserConfiguration(wd WebDriver) (map[string]interface{}, error) {
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
	result, err := wd.ExecuteScript(script, nil)
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

func setNavigatorProperties(wd WebDriver, lang, userAgent string, ident OSIdentity) {
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

		// Mock `navigator.vendor` and `navigator.platform` from the selected
		// advertised OS (VDI-005); the previous hardcoded 'Linux' ignored the
		// user's browser_platform choice.
		"Object.defineProperty(navigator, 'vendor', {get: () => 'Google Inc.'})",
		"Object.defineProperty(navigator, 'platform', {get: () => " + cmn.JSStringLiteral(ident.NavigatorPlatform) + "})",

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

		// Mock `navigator.appVersion` and `navigator.userAgent` with safe
		// string encoding (VDI-006: raw interpolation broke on quotes,
		// backslashes, or non-ASCII text in the UA).
		"Object.defineProperty(navigator, 'appVersion', {get: () => " + cmn.JSStringLiteral(userAgent) + "})",
		"Object.defineProperty(navigator, 'userAgent', {get: () => " + cmn.JSStringLiteral(userAgent) + "})",

		"Object.defineProperty(navigator, 'getBattery', {get: () => undefined})", // Disable battery API

		// Mock `navigator.connection`
		"Object.defineProperty(navigator, 'connection', {get: () => ({type: 'wifi', downlink: 10.0})})", // Mimic WiFi connection

		// Disable WebRTC APIs to prevent IP leakage
		"Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});",
		"Object.defineProperty(window, 'RTCDataChannel', {value: undefined});",
	}
	for _, script := range scripts {
		_, err := wd.ExecuteScript(script, nil)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting navigator properties: %v", err)
		}
	}
}

// InjectAntiDetectionPatches injects JavaScript anti-fingerprinting logic
// for one selection event. ident and chromeMajor must derive from the same
// User-Agent selection (see ConnectVDI) so navigator, Client Hints, and CDP
// surfaces agree. The script body is built by antiDetectionScript: identity
// derivation, safe string encoding, and removal of the debug trace changed;
// WebGL, Audio, plugin, MIME, and marker behavior is preserved.
//
// Custom (including synthetic) identities are preserved: values are derived
// from the selection, never forced to host-native ones.
func InjectAntiDetectionPatches(driver WebDriver, uaString string, ident OSIdentity, chromeMajor string) error {
	script := antiDetectionScript(uaString, ident, chromeMajor)

	err := AddScriptToEvaluateOnNewDocument(driver, 0, script)
	if err != nil {
		return fmt.Errorf("failed to inject stealth patches: %w", err)
	}

	return nil
}

// Refresh Session
// Refresh is responsible for refreshing the Selenium server instance
func Refresh(ctx ProcessContextInterface) error {
	if ctx == nil || ctx.GetWebDriver() == nil {
		return fmt.Errorf("invalid parameters: ProcessContext or SeleniumInstance is nil")
	}

	wd := *ctx.GetWebDriver()
	if wd == nil {
		return fmt.Errorf("invalid parameters: WebDriver is nil")
	}

	// Check if we have a SessionID
	sessionID := wd.SessionID()
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("invalid parameters: WebDriver SessionID is empty, unable to find session with id")
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-Refresh] Refreshing session with ID '%s'...", sessionID)

	// get the page title
	title, err := wd.Title()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to get page title during session refresh: %v", err)
		return err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-Refresh] Sent 'Keep Session Alive' command for page '%s'", title)

	return nil
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
