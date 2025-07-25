// Package main (addCategory) is a command line that allows to add categories and subcategories to the CROWler DB.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/qri-io/jsonschema"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"gopkg.in/yaml.v2"
)

var (
	config cfg.Config
)

// Category represents the structure of a category
type Category struct {
	Name          string        `json:"name" yaml:"name" validate:"required"`
	Description   string        `json:"description" yaml:"description"`
	Subcategories []Subcategory `json:"subcategories" yaml:"subcategories"`
}

// Subcategory represents the structure of a subcategory
type Subcategory struct {
	Name        string `json:"name" yaml:"name" validate:"required"`
	Description string `json:"description" yaml:"description"`
}

// CategoriesSchema represents the structure of the categories schema file
type CategoriesSchema struct {
	Categories []Category `json:"categories" yaml:"categories" validate:"required,dive"`
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to the configuration file")
	schemaFile := flag.String("schema", "./schemas/crowler-source-categories-schema.json", "Path to the schema file")
	flag.Parse()

	if len(os.Args) < 2 {
		fmt.Println("Usage: ./addCategory <path-to-json-or-yaml-file>")
		return
	}

	// Read the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	filePath := os.Args[1]
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	var categories CategoriesSchema
	if err := unmarshalFile(filePath, content, &categories); err != nil {
		fmt.Printf("Error unmarshalling file: %v\n", err)
		return
	}

	if err := validateSchema(content, *schemaFile); err != nil {
		fmt.Printf("Validation error: %v\n", err)
		return
	}

	// Database connection setup
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host, config.Database.Port,
		config.Database.User, config.Database.Password, config.Database.DBName)

	// Connect to the database
	db, err := sqlx.Connect(cdb.DBPostgresStr, psqlInfo)
	if err != nil {
		fmt.Printf("Error connecting to database: %v\n", err)
		return
	}
	defer db.Close() //nolint:errcheck // We can't check the error in a defer statement

	// Insert categories and subcategories
	for _, category := range categories.Categories {
		insertCategory(db, category, nil)
	}

	fmt.Println("Categories and subcategories inserted successfully.")
}

func unmarshalFile(filePath string, content []byte, v interface{}) error {
	switch ext := getFileExtension(filePath); ext {
	case ".json":
		return json.Unmarshal(content, v)
	case ".yaml", ".yml":
		return yaml.Unmarshal(content, v)
	default:
		return fmt.Errorf("unsupported file extension: %s", ext)
	}
}

func getFileExtension(filePath string) string {
	if len(filePath) < 5 {
		return ""
	}
	return filePath[len(filePath)-5:]
}

func validateSchema(content []byte, schemaPath string) error {
	// load the schema
	schemaFile, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("error reading schema file: %v", err)
	}
	// transform the schemaFile into a string
	schemaFileString := string(schemaFile)
	// load the schema into a jsonschema.Schema
	schemaLoader := jsonschema.Must(schemaFileString)

	// load the schema
	rs := &jsonschema.Schema{}
	schemaBytes, err := json.Marshal(schemaLoader)
	if err != nil {
		return fmt.Errorf("error marshalling schema: %v", err)
	}
	if err := json.Unmarshal(schemaBytes, rs); err != nil {
		return fmt.Errorf("error unmarshalling schema: %v", err)
	}

	var document interface{}
	if err := json.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("error unmarshalling document: %v", err)
	}

	errs, err := rs.ValidateBytes(context.Background(), content)
	if err != nil {
		return fmt.Errorf("error validating document: %v", err)
	}

	if len(errs) > 0 {
		var validationErrors string
		for _, err := range errs {
			validationErrors += fmt.Sprintf("- %s\n", err)
		}
		return fmt.Errorf("validation errors: \n%s", validationErrors)
	}

	return nil
}

func insertCategory(db *sqlx.DB, category Category, parentID *int64) {
	var categoryID int64
	query := `INSERT INTO Categories (name, description, parent_id, created_at)
              VALUES ($1, $2, $3, $4) RETURNING category_id`
	err := db.QueryRowx(query, category.Name, category.Description, parentID, time.Now()).Scan(&categoryID)
	if err != nil {
		fmt.Printf("Error inserting category: %v\n", err)
		return
	}

	for _, subcategory := range category.Subcategories {
		insertCategory(db, Category{
			Name:        subcategory.Name,
			Description: subcategory.Description,
		}, &categoryID)
	}
}
