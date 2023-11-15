package main

import (
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

func crawlWebsite(url string, wd selenium.WebDriver) {
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
		go worker(w, jobs, &wg)
	}

	// Enqueue jobs (links)
	for _, link := range links {
		jobs <- link
	}
	close(jobs)

	// Wait for all the workers to finish
	wg.Wait()
}

func getHTMLContent(url string, wd selenium.WebDriver) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	if err := wd.Get(url); err != nil {
		panic(err) // Replace with more robust error handling
	}
	time.Sleep(time.Second * 2) // Pause to let page load
	return wd, nil
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

func worker(id int, jobs <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for url := range jobs {
		fmt.Printf("Worker %d started job %s\n", id, url)
		// Implement the crawling logic for each link
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
