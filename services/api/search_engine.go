package main

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

func performSearch(query string) (SearchResult, error) {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host, config.Database.Port,
		config.Database.User, config.Database.Password, config.Database.DBName)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return SearchResult{}, err
	}
	defer db.Close()

	// Adjusted search query to include keywords
	sqlQuery := `
        SELECT DISTINCT si.title, si.page_url, si.summary, si.content
        FROM SearchIndex si
          LEFT JOIN KeywordIndex ki ON si.index_id = ki.index_id
          LEFT JOIN Keywords k ON ki.keyword_id = k.keyword_id
        WHERE 
		  LOWER(si.content) LIKE '%' || LOWER($1) || '%'
		OR LOWER(si.title) LIKE '%' || LOWER($1) || '%'
		OR LOWER(si.summary) LIKE '%' || LOWER($1) || '%'
        OR LOWER(k.keyword) LIKE '%' || LOWER($1) || '%'
    `
	rows, err := db.Query(sqlQuery, query)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close()

	var results SearchResult
	for rows.Next() {
		var title, link, summary, snippet string
		if err := rows.Scan(&title, &link, &summary, &snippet); err != nil {
			return SearchResult{}, err
		}
		results.Items = append(results.Items, struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Summary string `json:"summary"`
			Snippet string `json:"snippet"`
		}{
			Title:   title,
			Link:    link,
			Summary: summary,
			Snippet: snippet,
		})
	}

	return results, nil
}
