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
	URLID   uint64      `json:"url_id"`
	PageURL string      `json:"page_url"`
	Objects []WebObject `json:"objects"`
}

// WebObject represents a web object extracted from a page.
type WebObject struct {
	ObjectID   uint64          `json:"object_id"`
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	Confidence float64         `json:"confidence"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  time.Time       `json:"created_at"`
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
			s.url,
			u.url_id,
			u.url,
			wo.object_id,
			wo.object_type,
			wo.object_name,
			wo.confidence,
			wo.data,
			wo.created_at
		FROM sources s
		JOIN urls u ON u.source_id = s.source_id
		JOIN web_objects wo ON wo.url_id = u.url_id
		ORDER BY s.source_id, u.url_id, wo.object_id;
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
			urlID     uint64
			pageURL   string
			obj       WebObject
		)

		err := rows.Scan(
			&sourceID,
			&sourceURL,
			&urlID,
			&pageURL,
			&obj.ObjectID,
			&obj.Type,
			&obj.Name,
			&obj.Confidence,
			&obj.Data,
			&obj.CreatedAt,
		)
		if err != nil {
			log.Fatal(err)
		}

		src, ok := sourceMap[sourceID]
		if !ok {
			src = &Source{
				SourceID:  sourceID,
				SourceURL: sourceURL,
			}
			sourceMap[sourceID] = src
		}

		pg, ok := pageMap[urlID]
		if !ok {
			pg = &Page{
				URLID:   urlID,
				PageURL: pageURL,
			}
			pageMap[urlID] = pg
			src.Pages = append(src.Pages, *pg)
		}

		pg.Objects = append(pg.Objects, obj)
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
