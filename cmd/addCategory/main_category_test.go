package main

import (
	"database/sql/driver"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestGetFileExtension(t *testing.T) {

	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{
			name:     "json",
			filePath: "categories.json",
			want:     ".json",
		},
		{
			name:     "yaml",
			filePath: "categories.yaml",
			want:     ".yaml",
		},
		{
			name:     "yml",
			filePath: "categories.yml",
			want:     ".yml",
		},
		{
			name:     "short path",
			filePath: "x.go",
			want:     ".go",
		},
		{
			name:     "no extension",
			filePath: "categories",
			want:     "",
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {

			got := getFileExtension(tt.filePath)
			if got != tt.want {
				t.Fatalf("getFileExtension(%q) = %q, want %q", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestUnmarshalFileJSON(t *testing.T) {

	content := []byte(`{
		"categories": [
			{
				"name": "News",
				"description": "News sources",
				"subcategories": [
					{
						"name": "Music",
						"description": "Music news"
					}
				]
			}
		]
	}`)

	var got CategoriesSchema
	if err := unmarshalFile("categories.json", content, &got); err != nil {
		t.Fatalf("unmarshalFile returned error: %v", err)
	}

	if len(got.Categories) != 1 {
		t.Fatalf("len(got.Categories) = %d, want 1", len(got.Categories))
	}

	if got.Categories[0].Name != "News" {
		t.Fatalf("category name = %q, want %q", got.Categories[0].Name, "News")
	}

	if len(got.Categories[0].Subcategories) != 1 {
		t.Fatalf("len(subcategories) = %d, want 1", len(got.Categories[0].Subcategories))
	}

	if got.Categories[0].Subcategories[0].Name != "Music" {
		t.Fatalf("subcategory name = %q, want %q", got.Categories[0].Subcategories[0].Name, "Music")
	}
}

func TestUnmarshalFileYAML(t *testing.T) {

	content := []byte(`
categories:
  - name: News
    description: News sources
    subcategories:
      - name: Music
        description: Music news
`)

	var got CategoriesSchema
	if err := unmarshalFile("categories.yaml", content, &got); err != nil {
		t.Fatalf("unmarshalFile returned error: %v", err)
	}

	if len(got.Categories) != 1 {
		t.Fatalf("len(got.Categories) = %d, want 1", len(got.Categories))
	}

	if got.Categories[0].Name != "News" {
		t.Fatalf("category name = %q, want %q", got.Categories[0].Name, "News")
	}

	if len(got.Categories[0].Subcategories) != 1 {
		t.Fatalf("len(subcategories) = %d, want 1", len(got.Categories[0].Subcategories))
	}
}

func TestUnmarshalFileYML(t *testing.T) {

	content := []byte(`
categories:
  - name: News
`)

	var got CategoriesSchema
	if err := unmarshalFile("categories.yml", content, &got); err != nil {
		t.Fatalf("unmarshalFile returned error: %v", err)
	}

	if len(got.Categories) != 1 {
		t.Fatalf("len(got.Categories) = %d, want 1", len(got.Categories))
	}

	if got.Categories[0].Name != "News" {
		t.Fatalf("category name = %q, want %q", got.Categories[0].Name, "News")
	}
}

func TestUnmarshalFileUnsupportedExtension(t *testing.T) {

	var got CategoriesSchema
	err := unmarshalFile("categories.toml", []byte(`categories = []`), &got)
	if err == nil {
		t.Fatal("unmarshalFile returned nil error, want unsupported extension error")
	}

	if !regexp.MustCompile(`unsupported file extension`).MatchString(err.Error()) {
		t.Fatalf("error = %q, want unsupported extension error", err.Error())
	}
}

func TestValidateSchemaValidJSON(t *testing.T) {

	schemaPath := writeTempCategorySchema(t)

	content := []byte(`{
		"categories": [
			{
				"name": "News",
				"description": "News sources",
				"subcategories": [
					{
						"name": "Music",
						"description": "Music news"
					}
				]
			}
		]
	}`)

	if err := validateSchema(content, schemaPath); err != nil {
		t.Fatalf("validateSchema returned error: %v", err)
	}
}

func TestValidateSchemaRejectsMissingRequiredCategoryName(t *testing.T) {

	schemaPath := writeTempCategorySchema(t)

	content := []byte(`{
		"categories": [
			{
				"description": "Missing required category name"
			}
		]
	}`)

	err := validateSchema(content, schemaPath)
	if err == nil {
		t.Fatal("validateSchema returned nil error, want validation error")
	}

	if !regexp.MustCompile(`validation errors`).MatchString(err.Error()) {
		t.Fatalf("error = %q, want validation errors", err.Error())
	}
}

func TestValidateSchemaRejectsAdditionalProperties(t *testing.T) {

	schemaPath := writeTempCategorySchema(t)

	content := []byte(`{
		"categories": [
			{
				"name": "News",
				"unexpected": "not allowed"
			}
		]
	}`)

	err := validateSchema(content, schemaPath)
	if err == nil {
		t.Fatal("validateSchema returned nil error, want validation error")
	}

	if !regexp.MustCompile(`validation errors`).MatchString(err.Error()) {
		t.Fatalf("error = %q, want validation errors", err.Error())
	}
}

func TestValidateSchemaRejectsYAMLContentCurrently(t *testing.T) {

	schemaPath := writeTempCategorySchema(t)

	content := []byte(`
categories:
  - name: News
`)

	err := validateSchema(content, schemaPath)
	if err == nil {
		t.Fatal("validateSchema returned nil error for YAML content, want JSON unmarshal error")
	}

	if !regexp.MustCompile(`error unmarshalling document`).MatchString(err.Error()) {
		t.Fatalf("error = %q, want JSON unmarshal error", err.Error())
	}
}

func TestValidateSchemaMissingSchemaFile(t *testing.T) {

	content := []byte(`{"categories":[{"name":"News"}]}`)

	err := validateSchema(content, filepath.Join(t.TempDir(), "missing-schema.json"))
	if err == nil {
		t.Fatal("validateSchema returned nil error, want schema read error")
	}

	if !regexp.MustCompile(`error reading schema file`).MatchString(err.Error()) {
		t.Fatalf("error = %q, want schema read error", err.Error())
	}
}

func TestInsertCategoryInsertsCategoryAndSubcategories(t *testing.T) {

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New returned error: %v", err)
	}
	defer sqlDB.Close() //nolint:errcheck

	db := sqlx.NewDb(sqlDB, "postgres")

	query := regexp.QuoteMeta(`INSERT INTO Categories (name, description, parent_id, created_at)
              VALUES ($1, $2, $3, $4) RETURNING category_id`)

	mock.ExpectQuery(query).
		WithArgs("News", "News sources", nil, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"category_id"}).AddRow(int64(101)))

	mock.ExpectQuery(query).
		WithArgs("Cybersecurity", "Cybersecurity news", int64(101), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"category_id"}).AddRow(int64(102)))

	mock.ExpectQuery(query).
		WithArgs("Technology", "Technology news", int64(101), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"category_id"}).AddRow(int64(103)))

	insertCategory(db, Category{
		Name:        "News",
		Description: "News sources",
		Subcategories: []Subcategory{
			{
				Name:        "Cybersecurity",
				Description: "Cybersecurity news",
			},
			{
				Name:        "Technology",
				Description: "Technology news",
			},
		},
	}, nil)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestInsertCategoryStopsOnParentInsertError(t *testing.T) {

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New returned error: %v", err)
	}
	defer sqlDB.Close() //nolint:errcheck

	db := sqlx.NewDb(sqlDB, "postgres")

	query := regexp.QuoteMeta(`INSERT INTO Categories (name, description, parent_id, created_at)
              VALUES ($1, $2, $3, $4) RETURNING category_id`)

	mock.ExpectQuery(query).
		WithArgs("News", "News sources", nil, sqlmock.AnyArg()).
		WillReturnError(assertionError("insert failed"))

	insertCategory(db, Category{
		Name:        "News",
		Description: "News sources",
		Subcategories: []Subcategory{
			{
				Name:        "Music",
				Description: "Music news",
			},
		},
	}, nil)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func writeTempCategorySchema(t *testing.T) string {
	t.Helper()

	candidates := []string{
		filepath.Join("..", "..", "schemas", "crowler-source-categories-schema.json"),
		filepath.Join("schemas", "crowler-source-categories-schema.json"),
		filepath.Join(".", "schemas", "crowler-source-categories-schema.json"),
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	t.Fatalf("could not find crowler-source-categories-schema.json in candidates: %v", candidates)
	return ""
}

type int64PtrArg int64

func (a int64PtrArg) Match(v driver.Value) bool {
	ptr, ok := v.(*int64)
	if !ok || ptr == nil {
		return false
	}

	return *ptr == int64(a)
}

type assertionError string

func (e assertionError) Error() string {
	return string(e)
}
