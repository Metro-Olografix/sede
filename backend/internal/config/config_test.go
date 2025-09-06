package config

import (
	"testing"
)

func TestValidateAndSetDefaults(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		shouldPanic bool
		expected    Config
	}{
		{
			name: "valid config with all fields",
			config: Config{
				Port:              "8080",
				APIKey:            "supersecretapikey123",
				Debug:             false,
				AllowedOriginsStr: "https://example.com,http://localhost:3000",
				DatabasePath:      "custom/path.db",
			},
			shouldPanic: false,
			expected: Config{
				Port:              "8080",
				APIKey:            "supersecretapikey123",
				Debug:             false,
				AllowedOriginsStr: "https://example.com,http://localhost:3000",
				AllowedOrigins:    []string{"https://example.com", "http://localhost:3000"},
				DatabasePath:      "custom/path.db",
			},
		},
		{
			name: "valid config with default database path",
			config: Config{
				Port:   "3000",
				APIKey: "validapikey123456",
				Debug:  false,
			},
			shouldPanic: false,
			expected: Config{
				Port:           "3000",
				APIKey:         "validapikey123456",
				Debug:          false,
				AllowedOrigins: []string{},
				DatabasePath:   "database/sede.db",
			},
		},
		{
			name: "debug mode with short API key should not panic",
			config: Config{
				Port:   "8080",
				APIKey: "short",
				Debug:  true,
			},
			shouldPanic: false,
			expected: Config{
				Port:           "8080",
				APIKey:         "short",
				Debug:          true,
				AllowedOrigins: []string{},
				DatabasePath:   "database/sede.db",
			},
		},
		{
			name: "invalid port should panic",
			config: Config{
				Port:   "invalid",
				APIKey: "supersecretapikey123",
			},
			shouldPanic: true,
		},
		{
			name: "short API key in production should panic",
			config: Config{
				Port:   "8080",
				APIKey: "short",
				Debug:  false,
			},
			shouldPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("Expected panic but none occurred")
					}
				}()
				ValidateAndSetDefaults(tt.config)
			} else {
				result := ValidateAndSetDefaults(tt.config)

				if result.Port != tt.expected.Port {
					t.Errorf("Expected Port %s, got %s", tt.expected.Port, result.Port)
				}
				if result.APIKey != tt.expected.APIKey {
					t.Errorf("Expected APIKey %s, got %s", tt.expected.APIKey, result.APIKey)
				}
				if result.Debug != tt.expected.Debug {
					t.Errorf("Expected Debug %v, got %v", tt.expected.Debug, result.Debug)
				}
				if result.DatabasePath != tt.expected.DatabasePath {
					t.Errorf("Expected DatabasePath %s, got %s", tt.expected.DatabasePath, result.DatabasePath)
				}

				if len(result.AllowedOrigins) != len(tt.expected.AllowedOrigins) {
					t.Errorf("Expected %d allowed origins, got %d", len(tt.expected.AllowedOrigins), len(result.AllowedOrigins))
				} else {
					for i, origin := range result.AllowedOrigins {
						if origin != tt.expected.AllowedOrigins[i] {
							t.Errorf("Expected origin %s at index %d, got %s", tt.expected.AllowedOrigins[i], i, origin)
						}
					}
				}
			}
		})
	}
}

func TestParseAndValidateOrigins(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single valid origin",
			input:    "https://example.com",
			expected: []string{"https://example.com"},
		},
		{
			name:     "multiple valid origins",
			input:    "https://example.com,http://localhost:3000,https://api.test.com",
			expected: []string{"https://example.com", "http://localhost:3000", "https://api.test.com"},
		},
		{
			name:     "mixed valid and invalid origins",
			input:    "https://example.com,invalid-url,http://localhost:3000",
			expected: []string{"https://example.com", "http://localhost:3000"},
		},
		{
			name:     "all invalid origins",
			input:    "invalid,another-invalid,not-a-url",
			expected: []string{},
		},
		{
			name:     "origins with spaces are invalid",
			input:    " https://example.com , http://localhost:3000 ",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAndValidateOrigins(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d origins, got %d", len(tt.expected), len(result))
				return
			}

			for i, origin := range result {
				if origin != tt.expected[i] {
					t.Errorf("Expected origin %s at index %d, got %s", tt.expected[i], i, origin)
				}
			}
		})
	}
}
