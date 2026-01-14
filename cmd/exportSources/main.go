package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

// Export represents the overall export structure.
type Export struct {
	ExportedAt time.Time `json:"exported_at"`
	Sources    []Source  `json:"sources"`
}

// Source represents a source with its pages and web objects.
type Source struct {
	SourceID  uint64 `json:"source_id"`
	SourceURL string `json:"source_url"`
	Pages     []Page `json:"pages"`
}

// Page represents a page with its web objects.
type Page struct {
	IndexID uint64      `json:"index_id"`
	PageURL string      `json:"page_url"`
	Objects []WebObject `json:"objects"`
}

// WebObject represents a web object extracted from a page.
type WebObject struct {
	ObjectID      uint64          `json:"object_id"`
	ObjectType    string          `json:"object_type"`
	ObjectLink    string          `json:"object_link"`
	ObjectHash    string          `json:"object_hash"`
	ObjectContent *string         `json:"object_content,omitempty"`
	ObjectHTML    *string         `json:"object_html,omitempty"`
	Details       json.RawMessage `json:"details"`
	CreatedAt     time.Time       `json:"created_at"`
	LastUpdatedAt *time.Time      `json:"last_updated_at,omitempty"`
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to config file")
	output := flag.String("out", "", "Output JSON file (default stdout)")
	flag.Parse()

	config, err := cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host,
		config.Database.Port,
		config.Database.User,
		config.Database.Password,
		config.Database.DBName,
	)

	db, err := sql.Open(cdb.DBPostgresStr, psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT
			s.source_id,
			s.url              AS source_url,

			si.index_id,
			si.page_url,
			si.created_at,
			si.last_updated_at,

			wo.object_id,
			wo.object_type,
			wo.object_link,
			wo.object_hash,
			wo.object_content,
			wo.object_html,
			wo.details,
			wo.created_at,
			wo.last_updated_at
		FROM Sources s
		JOIN SourceSearchIndex ssi
			ON ssi.source_id = s.source_id
		JOIN SearchIndex si
			ON si.index_id = ssi.index_id
		JOIN WebObjectsIndex woi
			ON woi.index_id = si.index_id
		JOIN WebObjects wo
			ON wo.object_id = woi.object_id
		ORDER BY s.source_id, si.index_id, wo.object_id;
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	sourceMap := map[uint64]*Source{}
	pageMap := map[uint64]*Page{}

	for rows.Next() {
		var (
			sourceID  uint64
			sourceURL string

			indexID           uint64
			pageURL           string
			pageCreatedAt     time.Time
			pageLastUpdatedAt sql.NullTime

			obj WebObject

			objCreatedAt     time.Time
			objLastUpdatedAt sql.NullTime

			objContent sql.NullString
			objHTML    sql.NullString
		)

		err := rows.Scan(
			&sourceID,
			&sourceURL,

			&indexID,
			&pageURL,
			&pageCreatedAt,
			&pageLastUpdatedAt,

			&obj.ObjectID,
			&obj.ObjectType,
			&obj.ObjectLink,
			&obj.ObjectHash,
			&objContent,
			&objHTML,
			&obj.Details,
			&objCreatedAt,
			&objLastUpdatedAt,
		)
		if err != nil {
			log.Fatal(err)
		}

		if objContent.Valid {
			obj.ObjectContent = &objContent.String
		}
		if objHTML.Valid {
			obj.ObjectHTML = &objHTML.String
		}
		obj.CreatedAt = objCreatedAt
		if objLastUpdatedAt.Valid {
			t := objLastUpdatedAt.Time
			obj.LastUpdatedAt = &t
		}

		src, ok := sourceMap[sourceID]
		if !ok {
			src = &Source{
				SourceID:  sourceID,
				SourceURL: sourceURL,
			}
			sourceMap[sourceID] = src
		}

		pg, ok := pageMap[indexID]
		if !ok {
			pg = &Page{
				IndexID: indexID,
				PageURL: pageURL,
			}
			pageMap[indexID] = pg
			src.Pages = append(src.Pages, *pg)
		}

		pg.Objects = append(pg.Objects, obj)
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	export := Export{
		ExportedAt: time.Now().UTC(),
	}

	for _, src := range sourceMap {
		export.Sources = append(export.Sources, *src)
	}

	var out *os.File
	if *output == "" {
		out = os.Stdout
	} else {
		out, err = os.Create(*output)
		if err != nil {
			log.Fatal(err)
		}
		defer out.Close()
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(export); err != nil {
		log.Fatal(err)
	}
}
