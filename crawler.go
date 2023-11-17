package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"
)

const (
	numberOfWorkers = 5 // Adjust this based on your needs

)

type PageInfo struct {
	Title           string
	Summary         string
	BodyText        string
	ContainsAppInfo bool
	MetaTags        map[string]string // Add a field for meta tags
}

func crawlWebsite(db *sql.DB, source Source, wd selenium.WebDriver) {
	// Crawl the initial URL and get the HTML content
	// This is where you'd use Selenium or another method to get the page content
	pageSource, err := getHTMLContent(source.URL, wd)
	if err != nil {
		log.Println("Error getting HTML content:", err)
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
	}

	// Parse the HTML and extract links
	htmlContent, err := pageSource.PageSource()
	if err != nil {
		log.Println("Error getting page source:", err)
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
	}
	links := extractLinks(htmlContent)

	// Create a channel to enqueue jobs
	jobs := make(chan string, len(links))

	// Use a WaitGroup to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Launch worker goroutines
	for w := 1; w <= numberOfWorkers; w++ {
		wg.Add(1)
		go worker(db, wd, w, jobs, &wg, &source)
	}

	// Enqueue jobs (links)
	for _, link := range links {
		jobs <- link
	}
	close(jobs)

	// Wait for all the workers to finish
	wg.Wait()

	// Update the source state in the database
	updateSourceState(db, source.URL, nil)
}

func updateSourceState(db *sql.DB, sourceURL string, crawlError error) {
	var err error

	if crawlError != nil {
		// Update the source with error details
		_, err = db.Exec(`UPDATE Sources SET last_crawled_at = NOW(), status = 'error', 
                          last_error = $1, last_error_at = NOW() 
                          WHERE url = $2`, crawlError.Error(), sourceURL)
	} else {
		// Update the source as successfully crawled
		_, err = db.Exec(`UPDATE Sources SET last_crawled_at = NOW(), status = 'completed' 
                          WHERE url = $1`, sourceURL)
	}

	if err != nil {
		log.Printf("Error updating source state for URL %s: %v", sourceURL, err)
	}
}

func indexPage(db *sql.DB, url string, pageInfo PageInfo) {
	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v\n", err)
		return
	}

	// Insert or update the page in SearchIndex
	var indexID int
	err = tx.QueryRow(`
        INSERT INTO SearchIndex (page_url, title, summary, content, indexed_at)
        VALUES ($1, $2, $3, $4, NOW())
        ON CONFLICT (page_url) DO UPDATE
        SET title = EXCLUDED.title, summary = EXCLUDED.summary, content = EXCLUDED.content, indexed_at = NOW()
        RETURNING index_id`, url, pageInfo.Title, pageInfo.Summary, pageInfo.BodyText).
		Scan(&indexID)
	if err != nil {
		log.Printf("Error inserting into or updating SearchIndex: %v\n", err)
		tx.Rollback()
		return
	}

	// Insert MetaTags
	for name, content := range pageInfo.MetaTags {
		_, err = tx.Exec(`INSERT INTO MetaTags (index_id, name, content)
                          VALUES ($1, $2, $3)`, indexID, name, content)
		if err != nil {
			log.Printf("Error inserting meta tag: %v\n", err)
			tx.Rollback()
			return
		}
	}

	// Assuming keywords are part of pageInfo (e.g., extracted from MetaTags)
	for _, keyword := range extractKeywords(pageInfo) {
		// Insert or find keyword ID
		fmt.Println("Inserting keyword:", keyword)
		var keywordID int
		err = tx.QueryRow(`INSERT INTO Keywords (keyword)
                           VALUES ($1) ON CONFLICT (keyword) DO UPDATE
                           SET keyword = EXCLUDED.keyword RETURNING keyword_id`, keyword).
			Scan(&keywordID)
		if err != nil {
			log.Printf("Error inserting or finding keyword: %v\n", err)
			tx.Rollback()
			return
		}

		// Insert into KeywordIndex
		_, err = tx.Exec(`INSERT INTO KeywordIndex (keyword_id, index_id)
                          VALUES ($1, $2)`, keywordID, indexID)
		if err != nil {
			log.Printf("Error inserting into KeywordIndex: %v\n", err)
			tx.Rollback()
			return
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		log.Printf("Error committing transaction: %v\n", err)
		tx.Rollback()
		return
	}
}

func getHTMLContent(url string, wd selenium.WebDriver) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	if err := wd.Get(url); err != nil {
		return nil, err
	}
	time.Sleep(time.Second * 2) // Pause to let page load
	return wd, nil
}

func extractPageInfo(webPage selenium.WebDriver) PageInfo {
	htmlContent, _ := webPage.PageSource()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Printf("Error loading HTML content: %v", err)
		return PageInfo{} // Return an empty struct in case of an error
	}

	//title := doc.Find("title").Text()
	title, _ := webPage.Title()
	summary := doc.Find("meta[name=description]").AttrOr("content", "")
	bodyText := doc.Find("body").Text()
	//bodyText, _ := webPage.PageSource()

	containsAppInfo := strings.Contains(bodyText, "app") || strings.Contains(bodyText, "mobile")

	metaTags := extractMetaTags(doc)

	return PageInfo{
		Title:           title,
		Summary:         summary,
		BodyText:        bodyText,
		ContainsAppInfo: containsAppInfo,
		MetaTags:        metaTags,
	}
}

func extractMetaTags(doc *goquery.Document) map[string]string {
	metaTags := make(map[string]string)
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		if name, exists := s.Attr("name"); exists {
			content, _ := s.Attr("content")
			metaTags[name] = content
		}
	})
	return metaTags
}

func extractLinks(htmlContent string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Fatal("Error loading HTTP response body. ", err)
	}

	var links []string
	doc.Find("a").Each(func(index int, item *goquery.Selection) {
		linkTag := item
		link, _ := linkTag.Attr("href")
		links = append(links, link)
	})
	return links
}

func isExternalLink(sourceURL, linkURL string) bool {
	// remove trailing spaces from linkURL
	linkURL = strings.TrimSpace(linkURL)

	// Handle relative URLs (e.g., "/sub/page")
	if strings.HasPrefix(linkURL, "/") {
		return false // This is a relative URL, not external
	}

	// Parse the source URL
	sourceParsed, err := url.Parse(sourceURL)
	if err != nil {
		return false // Handle parsing error
	}

	// Parse the link URL
	linkParsed, err := url.Parse(linkURL)
	if err != nil {
		return false // Handle parsing error
	}

	fmt.Println("Source URL:", sourceURL)
	fmt.Println("Link URL:", linkURL)
	fmt.Println("Source hostname:", sourceParsed.Hostname())
	fmt.Println("Link hostname:", linkParsed.Hostname())

	// Compare hostnames
	return sourceParsed.Hostname() != linkParsed.Hostname()
}

func worker(db *sql.DB, wd selenium.WebDriver, id int, jobs <-chan string, wg *sync.WaitGroup, source *Source) {
	defer wg.Done()
	for url := range jobs {
		if source.Restricted {
			// If the Source is restricted
			// check if the url is outside the Source domain
			// if so skip it
			if isExternalLink(source.URL, url) {
				log.Printf("Worker %d: Skipping restricted URL: %s\n", id, url)
				continue
			}
		}
		log.Printf("Worker %d started job %s\n", id, url)

		// remove trailing space in url
		url = strings.TrimSpace(url)
		if strings.HasPrefix(url, "/") {
			url, _ = combineURLs(source.URL, url)
		}
		if url == "" {
			continue
		}

		// Get HTML content of the page
		htmlContent, err := getHTMLContent(url, wd)
		if err != nil {
			log.Printf("Worker %d: Error getting HTML content for %s: %v\n", id, url, err)
			continue
		}

		// Extract necessary information from the content
		// For simplicity, we're extracting just the title and full content
		// You might want to extract more specific information
		pageCache := extractPageInfo(htmlContent)

		// Index the page content in the database
		indexPage(db, url, pageCache)

		log.Printf("Worker %d finished job %s\n", id, url)
	}
}

func combineURLs(baseURL, relativeURL string) (string, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", err // Handle parsing error
	}

	// Reconstruct the base URL with the scheme and hostname
	reconstructedBaseURL := parsedURL.Scheme + "://" + parsedURL.Host

	// Combine with relative URL
	if strings.HasPrefix(relativeURL, "/") {
		return reconstructedBaseURL + relativeURL, nil
	}
	return relativeURL, nil
}

func startSelenium() (*selenium.Service, error) {
	log.Println("Configuring Selenium...")
	// Start a Selenium WebDriver server instance (e.g., chromedriver)
	/*
		var opts []selenium.ServiceOption
		// Add debug option to see errors
		// opts = append(opts, selenium.Output(os.Stderr))

		// Add options to control the user selected browser
		if config.Selenium.Type == "firefox" {
			opts = append(opts, selenium.GeckoDriver(config.Selenium.DriverPath)) // Specify the path to GeckoDriver
		} else if config.Selenium.Type == "chrome" {
			opts = append(opts, selenium.ChromeDriver(config.Selenium.DriverPath)) // Specify the path to ChromeDriver
		} else if config.Selenium.Type == "chromium" {
			opts = append(opts, selenium.ChromeDriver(config.Selenium.DriverPath)) // Specify the path to ChromeDriver
		} else {
			log.Fatalf("  Error: Unsupported browser type: %s\n", config.Selenium.Type)
		}
	*/
	/*
		var cmd *exec.Cmd
		if config.OS == "darwin" {
			cmd = exec.Command("brew", "services", "start", "selenium-server")
		} else if config.OS == "linux" {
			cmd = exec.Command("sudo", "systemctl", "start", "selenium-server")
		} else {
			log.Fatalf("  Error: Unsupported OS: %s\n", config.OS)
		}
		// Execute the command
		if err := cmd.Run(); err != nil {
			log.Printf("  Error starting Selenium server: %v\n", err)
		} else {
			log.Println("  Selenium server started successfully... ")
		}
	*/

	// Start a Selenium WebDriver server instance (e.g., chromedriver)
	//service, err := selenium.NewSeleniumService(config.Selenium.Path, config.Selenium.Port, opts...)
	//service, err := selenium.NewChromeDriverService(config.Selenium.DriverPath, config.Selenium.Port)
	var service *selenium.Service
	var err error = nil
	log.Printf("Done!\n")

	return service, err
}

func stopSelenium(sel *selenium.Service) {
	sel.Stop()
}

func connectSelenium(sel *selenium.Service) (selenium.WebDriver, error) {
	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": config.Selenium.Type}
	caps.AddChrome(chrome.Capabilities{
		Args: []string{
			"--headless", // Updated headless mode argument
			"--no-sandbox",
			"--disable-dev-shm-usage",
		},
		W3C: true,
	})
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://"+config.Selenium.Host+":%d/wd/hub", config.Selenium.Port))
	//wd, err := selenium.NewRemote(caps, "")
	return wd, err
}

func quitSelenium(wd selenium.WebDriver) {
	wd.Quit()
}
