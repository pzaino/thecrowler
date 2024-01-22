package crawler

// This struct represents the information that we want to extract from a page
// and store in the database.
type PageInfo struct {
	Title           string
	Summary         string
	BodyText        string
	ContainsAppInfo bool
	MetaTags        map[string]string // Add a field for meta tags
}
