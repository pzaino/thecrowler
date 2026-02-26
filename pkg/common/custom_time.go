// Package common package is used to store common functions and variables
package common

import "time"

// CustomTime wraps time.Time to provide custom parsing.
type CustomTime struct {
	time.Time
}

// UnmarshalYAML parses date strings from the YAML file.
func (ct *CustomTime) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var dateStr string
	if err := unmarshal(&dateStr); err != nil {
		return err
	}

	// Parse date string in RFC3339 format, if it fails, try with the "2006-01-02" format
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		t, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return err
		}
	}

	ct.Time = t
	return nil
}

// IsEmpty checks if the CustomTime is empty.
func (ct *CustomTime) IsEmpty() bool {
	return ct.Time.IsZero()
}
