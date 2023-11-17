package main

// Config represents the structure of the configuration file
type Config struct {
	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
	} `yaml:"database"`
	Workers  int `yaml:"workers"`
	Selenium struct {
		Path       string `yaml:"path"`
		DriverPath string `yaml:"driver_path"`
		Type       string `yaml:"type"`
		Port       int    `yaml:"port"`
		Host       string `yaml:"host"`
		Headless   bool   `yaml:"headless"`
	} `yaml:"selenium"`
	OS string `yaml:"os"`
}

var (
	config Config
)

type SearchResult struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Summary string `json:"summary"`
		Snippet string `json:"snippet"`
	} `json:"items"`
}
