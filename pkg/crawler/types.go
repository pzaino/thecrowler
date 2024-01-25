package crawler

// PageInfo represents the information of a web page.
type PageInfo struct {
	sourceID int               // The ID of the source.
	Title    string            // The title of the web page.
	Summary  string            // A summary of the web page content.
	BodyText string            // The main body text of the web page.
	MetaTags map[string]string // The meta tags of the web page.
}
