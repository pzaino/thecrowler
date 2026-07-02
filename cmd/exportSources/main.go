package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

const exportQuery = `
	SELECT
		s.source_id,
		s.url AS source_url,

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
	LEFT JOIN WebObjectsIndex woi
		ON woi.index_id = si.index_id
	LEFT JOIN WebObjects wo
		ON wo.object_id = woi.object_id
	ORDER BY s.source_id, si.index_id, wo.object_id;`

var (
	loadConfig   = cfg.LoadConfig
	openDatabase = sql.Open
	now          = time.Now
	createFile   = func(name string) (io.WriteCloser, error) { return os.Create(name) }
)

// Export represents the overall export structure.
type Export struct {
	ExportedAt time.Time `json:"exported_at"`
	Sources    []Source  `json:"sources"`
}

// Source represents a source with its pages and web objects.
type Source struct {
	SourceID  uint64  `json:"source_id"`
	SourceURL string  `json:"source_url"`
	Pages     []*Page `json:"pages"`
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
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("exportSources", flag.ContinueOnError)
	configFile := flags.String("config", "config.yaml", "Path to config file")
	output := flags.String("out", "", "Output JSON file (default stdout)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	config, err := loadConfig(*configFile)
	if err != nil {
		return err
	}

	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host,
		config.Database.Port,
		config.Database.User,
		config.Database.Password,
		config.Database.DBName,
	)

	db, err := openDatabase(cdb.DBPostgresStr, psqlInfo)
	if err != nil {
		return err
	}
	defer db.Close()

	export, err := collectExport(db, now().UTC())
	if err != nil {
		return err
	}

	writer := stdout
	var outputFile io.WriteCloser
	if *output != "" {
		outputFile, err = createFile(*output)
		if err != nil {
			return err
		}
		defer outputFile.Close()
		writer = outputFile
	}

	return encodeExport(writer, export)
}

func collectExport(db *sql.DB, exportedAt time.Time) (Export, error) {
	rows, err := db.Query(exportQuery)
	if err != nil {
		return Export{}, err
	}
	defer rows.Close()

	export := Export{
		ExportedAt: exportedAt,
		Sources:    []Source{},
	}
	sourceIndexes := make(map[uint64]int)
	pageIndexes := make(map[uint64]map[uint64]int)

	for rows.Next() {
		var (
			sourceID  uint64
			sourceURL string
			indexID   uint64
			pageURL   string

			pageCreatedAt     time.Time
			pageLastUpdatedAt sql.NullTime

			objectID        sql.NullInt64
			objectType      sql.NullString
			objectLink      sql.NullString
			objectHash      sql.NullString
			objectContent   sql.NullString
			objectHTML      sql.NullString
			objectDetails   []byte
			objectCreatedAt sql.NullTime
			objectUpdatedAt sql.NullTime
		)

		if err := rows.Scan(
			&sourceID, &sourceURL,
			&indexID, &pageURL, &pageCreatedAt, &pageLastUpdatedAt,
			&objectID, &objectType, &objectLink, &objectHash,
			&objectContent, &objectHTML, &objectDetails,
			&objectCreatedAt, &objectUpdatedAt,
		); err != nil {
			return Export{}, err
		}

		sourceIndex, found := sourceIndexes[sourceID]
		if !found {
			sourceIndex = len(export.Sources)
			sourceIndexes[sourceID] = sourceIndex
			pageIndexes[sourceID] = make(map[uint64]int)
			export.Sources = append(export.Sources, Source{
				SourceID:  sourceID,
				SourceURL: sourceURL,
				Pages:     []*Page{},
			})
		}

		pageIndex, found := pageIndexes[sourceID][indexID]
		if !found {
			pageIndex = len(export.Sources[sourceIndex].Pages)
			pageIndexes[sourceID][indexID] = pageIndex
			export.Sources[sourceIndex].Pages = append(export.Sources[sourceIndex].Pages, &Page{
				IndexID: indexID,
				PageURL: pageURL,
				Objects: []WebObject{},
			})
		}

		if !objectID.Valid {
			continue
		}
		if objectID.Int64 < 0 {
			return Export{}, errors.New("web object ID cannot be negative")
		}

		object := WebObject{
			ObjectID:   uint64(objectID.Int64),
			ObjectType: objectType.String,
			ObjectLink: objectLink.String,
			ObjectHash: objectHash.String,
			Details:    json.RawMessage(objectDetails),
		}
		if objectContent.Valid {
			object.ObjectContent = &objectContent.String
		}
		if objectHTML.Valid {
			object.ObjectHTML = &objectHTML.String
		}
		if objectCreatedAt.Valid {
			object.CreatedAt = objectCreatedAt.Time
		}
		if objectUpdatedAt.Valid {
			updatedAt := objectUpdatedAt.Time
			object.LastUpdatedAt = &updatedAt
		}

		page := export.Sources[sourceIndex].Pages[pageIndex]
		page.Objects = append(page.Objects, object)
	}

	if err := rows.Err(); err != nil {
		return Export{}, err
	}
	return export, nil
}

func encodeExport(writer io.Writer, export Export) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(export)
}
