//go:build integration

package infoseed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-auxiliaries/selenium"
	"github.com/go-auxiliaries/selenium/chrome"

	"github.com/pzaino/thecrowler/pkg/infoseed/searchproviders"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

func TestBrowserSearchWebDriverIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the Selenium/Chrome browser-search integration test")
	}

	fixtureServer := httptest.NewServer(http.FileServer(http.Dir(filepath.Join("testdata", "browser_search"))))
	defer fixtureServer.Close()

	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}
	capabilities := selenium.Capabilities{"browserName": "chrome"}
	capabilities.AddChrome(chrome.Capabilities{Args: []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--window-size=1280,900",
	}})
	wd, err := selenium.NewRemote(capabilities, webdriverURL)
	if err != nil {
		t.Skipf("Selenium/Chrome unavailable at %s: %v", webdriverURL, err)
	}

	sessions := &seleniumFixtureSessionProvider{driver: wd}
	handler := openInformationSeedE2ESQLiteDB(t)
	runner, seed := newBrowserSearchRunner(t, handler, fixtureServer.URL, sessions)
	result, err := runner.RunSeed(context.Background(), seed)
	if err != nil {
		t.Fatalf("RunSeed() with Selenium/Chrome error = %v", err)
	}
	if result.CandidatesFound != 3 || result.Candidates != 3 || result.SourcesCreated != 3 || result.Linked != 3 {
		t.Fatalf("RunSeed() with Selenium/Chrome result = %#v, want three capped and persisted candidates", result)
	}
	if sessions.acquireCount != 1 || sessions.session.closeCount != 1 || sessions.lease.releaseCount != 1 {
		t.Fatalf("Selenium lease lifecycle: acquire=%d close=%d release=%d", sessions.acquireCount, sessions.session.closeCount, sessions.lease.releaseCount)
	}
	assertBrowserDiscoveryRows(t, handler, seed.ID)
}

type seleniumFixtureSessionProvider struct {
	driver       vdi.WebDriver
	acquireCount int
	session      *seleniumFixtureSession
	lease        *seleniumFixtureLease
}

func (p *seleniumFixtureSessionProvider) Acquire(context.Context, searchproviders.BrowserSessionRequest) (searchproviders.BrowserSession, searchproviders.BrowserSessionLease, error) {
	p.acquireCount++
	p.session = &seleniumFixtureSession{driver: p.driver}
	p.lease = &seleniumFixtureLease{}
	return p.session, p.lease, nil
}

type seleniumFixtureSession struct {
	driver     vdi.WebDriver
	closeCount int
}

func (s *seleniumFixtureSession) Navigate(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.driver.Get(target)
}
func (s *seleniumFixtureSession) CurrentURL(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.driver.CurrentURL()
}
func (s *seleniumFixtureSession) Close(context.Context) error {
	s.closeCount++
	return s.driver.Quit()
}
func (s *seleniumFixtureSession) webDriver() vdi.WebDriver { return s.driver }

type seleniumFixtureLease struct{ releaseCount int }

func (l *seleniumFixtureLease) Release(context.Context) error {
	l.releaseCount++
	return nil
}
