package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/tebeka/selenium"
)

const (
	numberOfWorkers = 5 // Adjust this based on your needs

)

type PageInfo struct {
	Title           string
	BodyText        string
	ContainsAppInfo bool
}

func crawlWebsite(db *sql.DB, url string, wd selenium.WebDriver) {
	// Crawl the initial URL and get the HTML content
	// This is where you'd use Selenium or another method to get the page content
	pageSource, err := getHTMLContent(url, wd)
	if err != nil {
		log.Println("Error getting HTML content:", err)
		return
	}

	// Parse the HTML and extract links
	htmlContent, err := pageSource.PageSource()
	if err != nil {
		log.Println("Error getting page source:", err)
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
		go worker(db, wd, w, jobs, &wg)
	}

	// Enqueue jobs (links)
	for _, link := range links {
		jobs <- link
	}
	close(jobs)

	// Wait for all the workers to finish
	wg.Wait()
}

func indexPage(db *sql.DB, url string, pageInfo PageInfo) {
	// Check if the URL is already indexed
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM SearchIndex WHERE page_url = $1)", url).Scan(&exists)
	if err != nil {
		log.Printf("Error checking if URL exists: %v\n", err)
		return
	}

	if exists {
		// Update the existing entry (if needed)
		_, err = db.Exec("UPDATE SearchIndex SET title = $1, content = $2 WHERE page_url = $3", pageInfo.Title, pageInfo.BodyText, url)
		if err != nil {
			log.Printf("Error updating indexed page: %v\n", err)
		}
	} else {
		// Insert a new entry
		_, err = db.Exec("INSERT INTO SearchIndex (page_url, title, content) VALUES ($1, $2, $3)", url, pageInfo.Title, pageInfo.BodyText)
		if err != nil {
			log.Printf("Error inserting indexed page: %v\n", err)
		}
	}
}

func getHTMLContent(url string, wd selenium.WebDriver) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	if err := wd.Get(url); err != nil {
		panic(err) // Replace with more robust error handling
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
	bodyText := doc.Find("body").Text()
	//bodyText, _ := webPage.PageSource()

	containsAppInfo := strings.Contains(bodyText, "app") || strings.Contains(bodyText, "mobile")

	return PageInfo{
		Title:           title,
		BodyText:        bodyText,
		ContainsAppInfo: containsAppInfo,
	}
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

func worker(db *sql.DB, wd selenium.WebDriver, id int, jobs <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for url := range jobs {
		fmt.Printf("Worker %d started job %s\n", id, url)

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

		fmt.Printf("Worker %d finished job %s\n", id, url)
	}
}

func startSelenium() (*selenium.Service, error) {
	// Start a Selenium WebDriver server instance (e.g., chromedriver)
	const (
		seleniumPath     = "/path/to/selenium-server-standalone.jar"
		chromeDriverPath = "/path/to/chromedriver"
		port             = 8080
	)
	opts := []selenium.ServiceOption{
		selenium.ChromeDriver(chromeDriverPath), // Specify the path to ChromeDriver
	}
	service, err := selenium.NewSeleniumService(seleniumPath, port, opts...)
	return service, err
}

func stopSelenium(sel *selenium.Service) {
	sel.Stop()
}

func connectSelenium(sel *selenium.Service) (selenium.WebDriver, error) {
	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": "chrome"}
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
	return wd, err
}

func quitSelenium(wd selenium.WebDriver) {
	wd.Quit()
}
