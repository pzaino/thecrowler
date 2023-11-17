package database

// Source represents the structure of the Sources table
// for a record we have decided we need to crawl
type Source struct {
	URL        string
	Restricted bool
}
