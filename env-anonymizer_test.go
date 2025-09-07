package main

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

// Helper function to create a temporary file with given content
func createTempFile(t *testing.T, content string) string {
    t.Helper()
    tmpfile, err := os.CreateTemp("", "testenv")
    if err != nil {
        t.Fatal(err)
    }
    if _, err := tmpfile.Write([]byte(content)); err != nil {
        t.Fatal(err)
    }
    if err := tmpfile.Close(); err != nil {
        t.Fatal(err)
    }
    return tmpfile.Name()
}

// Test processEnvFile with different scenarios
func TestProcessEnvFile(t *testing.T) {
	testCases := []struct {
		name                   string
		fileContent            string
		includeNonVariables    bool
		expectedOutputContains []string
		expectedOutputExcludes []string
	}{
		{
			name:                "Basic env file with variables",
			fileContent:         "DB_HOST=localhost\nAPI_KEY=secret123\n",
			includeNonVariables: false,
			expectedOutputContains: []string{
				"DB_HOST=<DB_HOST_VALUE>",
				"API_KEY=<API_KEY_VALUE>",
			},
		},
		{
			name:                "File with comments and blank lines",
			fileContent:         "# Database settings\n\nDB_HOST=localhost\n# API settings\nAPI_KEY=secret123\n",
			includeNonVariables: true,
			expectedOutputContains: []string{
				"# Database settings",
				"DB_HOST=<DB_HOST_VALUE>",
				"# API settings",
				"API_KEY=<API_KEY_VALUE>",
			},
		},
		{
			name:                "Duplicate keys",
			fileContent:         "DB_HOST=localhost\nDB_HOST=different\n",
			includeNonVariables: false,
			expectedOutputContains: []string{
				"DB_HOST=<DB_HOST_VALUE>",
			},
			expectedOutputExcludes: []string{
				"DB_HOST=different",
			},
		},
		{
			name:                "Malformed lines",
			fileContent:         "INVALID_LINE\nDB_HOST=localhost\n=value\n",
			includeNonVariables: true,
			expectedOutputContains: []string{
				"DB_HOST=<DB_HOST_VALUE>",
				"# INVALID_LINE # Skipped Malformed Line",
				"# =value # Skipped Empty Key",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temp file with test content
			tmpfile := createTempFile(t, tc.fileContent)
			defer os.Remove(tmpfile)

			// Setup variables for processEnvFile
			seenKeys := make(map[string]struct{})
			var outputLines []string

			// Call the function
			err := processEnvFile(tmpfile, seenKeys, &outputLines, tc.includeNonVariables)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check output lines
			outputStr := strings.Join(outputLines, "\n")
			for _, expected := range tc.expectedOutputContains {
				if !strings.Contains(outputStr, expected) {
					t.Errorf("Expected output to contain '%s', but it did not. Full output: %s", expected, outputStr)
				}
			}

			// Check exclusions
			for _, excluded := range tc.expectedOutputExcludes {
				if strings.Contains(outputStr, excluded) {
					t.Errorf("Expected output to NOT contain '%s', but it did. Full output: %s", excluded, outputStr)
				}
			}
		})
	}
}

// Test generateExampleFile integration
func TestGenerateExampleFile(t *testing.T) {
	// Create a temporary directory for our test files
    tmpDir := t.TempDir()

	// Paths for test files
	baseEnvPath := filepath.Join(tmpDir, ".env")
	localEnvPath := filepath.Join(tmpDir, ".env.local")
	outputPath := filepath.Join(tmpDir, ".env.example")

	// Create base .env file
	baseEnvContent := "DB_HOST=localhost\nDB_PORT=5432\n"
    if err := os.WriteFile(baseEnvPath, []byte(baseEnvContent), 0644); err != nil {
        t.Fatal(err)
    }

	// Create local .env file with override and new key
	localEnvContent := "DB_HOST=production\nAPI_KEY=secret123\n"
    if err := os.WriteFile(localEnvPath, []byte(localEnvContent), 0644); err != nil {
        t.Fatal(err)
    }

    // Call generateExampleFile
    if err := generateExampleFile(baseEnvPath, localEnvPath, outputPath); err != nil {
        t.Fatalf("Unexpected error: %v", err)
    }

	// Read the generated .env.example file
    content, err := os.ReadFile(outputPath)
    if err != nil {
        t.Fatalf("Failed to read output file: %v", err)
    }

	outputStr := string(content)

	// Check expected content
	expectedLines := []string{
		"DB_PORT=<DB_PORT_VALUE>",
		"API_KEY=<API_KEY_VALUE>",
		"DB_HOST=<DB_HOST_VALUE>", // From local env, overriding base
	}

	for _, expected := range expectedLines {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Expected output to contain '%s', but it did not. Full output: %s", expected, outputStr)
		}
	}
}

// Test error handling scenarios
func TestErrorHandling(t *testing.T) {
	// Test non-existent base env file
    tmpDir := t.TempDir()

	baseEnvPath := filepath.Join(tmpDir, ".env")
	localEnvPath := filepath.Join(tmpDir, ".env.local")
	outputPath := filepath.Join(tmpDir, ".env.example")

	// No base .env file, but local .env file exists
	localEnvContent := "API_KEY=secret123\n"
    if err := os.WriteFile(localEnvPath, []byte(localEnvContent), 0644); err != nil {
        t.Fatal(err)
    }

    // This should work without throwing an error
    if err := generateExampleFile(baseEnvPath, localEnvPath, outputPath); err != nil {
        t.Fatalf("Unexpected error when base .env is missing: %v", err)
    }

	// Verify output file was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created when base .env is missing")
	}
}
