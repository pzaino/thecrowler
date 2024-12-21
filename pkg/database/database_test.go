package database

import (
	"fmt"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestBuildConnectionString(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		config   cfg.Config
		expected string
	}{
		{
			name: "Test case 1: Default values",
			config: cfg.Config{
				Database: cfg.Database{},
			},
			expected: "host=localhost port=5432 user=crowler password= dbname=SitesIndex sslmode=disable",
		},
		{
			name: "Test case 2: Custom values",
			config: cfg.Config{
				Database: cfg.Database{
					Port:     5433,
					Host:     "example.com",
					User:     "customuser",
					Password: "custompassword",
					DBName:   "customdb",
					SSLMode:  "require",
				},
			},
			expected: "host=example.com port=5433 user=customuser password=custompassword dbname=customdb sslmode=require",
		},
	}

	// Run tests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := buildConnectionString(test.config)
			if result != test.expected {
				t.Errorf("expected '%s', got '%s'", test.expected, result)
			}
		})
	}
}

func TestNewHandler(t *testing.T) {
	// Test cases
	tests := []struct {
		name         string
		config       cfg.Config
		expectedType interface{}
		expectedErr  error
	}{
		{
			name: "Test case 1: Postgres",
			config: cfg.Config{
				Database: cfg.Database{
					Type: DBPostgresStr,
				},
			},
			expectedType: &PostgresHandler{},
			expectedErr:  nil,
		},
		{
			name: "Test case 2: SQLite",
			config: cfg.Config{
				Database: cfg.Database{
					Type: DBSQLiteStr,
				},
			},
			expectedType: &SQLiteHandler{},
			expectedErr:  nil,
		},
		{
			name: "Test case 3: Unsupported database type",
			config: cfg.Config{
				Database: cfg.Database{
					Type: "mysql",
				},
			},
			expectedType: nil,
			expectedErr:  fmt.Errorf("unsupported database type: 'mysql'"),
		},
	}

	// Run tests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := NewHandler(test.config)

			if (err != nil && test.expectedErr == nil) || (err == nil && test.expectedErr != nil) || (err != nil && err.Error() != test.expectedErr.Error()) {
				t.Errorf("expected error '%v', got '%v'", test.expectedErr, err)
			}

			if test.expectedType != nil {
				switch test.expectedType.(type) {
				case *PostgresHandler:
					if _, ok := handler.(*PostgresHandler); !ok {
						t.Errorf("expected type *PostgresHandler, got %T", handler)
					}
				case *SQLiteHandler:
					if _, ok := handler.(*SQLiteHandler); !ok {
						t.Errorf("expected type *SQLiteHandler, got %T", handler)
					}
				}
			} else if handler != nil {
				t.Errorf("expected nil handler, got %T", handler)
			}
		})
	}
}
