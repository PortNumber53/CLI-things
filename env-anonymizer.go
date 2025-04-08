package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultEnvFile      = ".env"
	defaultEnvLocalFile = ".env.local"
	defaultExampleFile  = ".env.example"
	anonymizedValueTpl  = "<%s_VALUE>" // Template for anonymized value
	permissionReadWrite = 0644         // Standard file permissions
)

// Represents a line in the env file (either a variable, comment, or blank)
type envLine struct {
	rawLine    string // Original line content for comments/blanks
	key        string // Parsed key if it's a variable line
	isVariable bool   // Flag indicating if this line is a key=value pair
}

func main() {
	// --- Command Line Flags ---
	envFilePath := flag.String("env", defaultEnvFile, "Path to the main .env file")
	localEnvFilePath := flag.String("local", defaultEnvLocalFile, "Path to the local .env override file")
	outputFilePath := flag.String("output", defaultExampleFile, "Path for the generated .env.example file")
	flag.Parse()

	fmt.Printf("Reading base config from: %s\n", *envFilePath)
	if _, err := os.Stat(*localEnvFilePath); err == nil {
		fmt.Printf("Reading local overrides from: %s\n", *localEnvFilePath)
	} else if !os.IsNotExist(err) {
		// Only log error if it's something other than 'file not found'
		fmt.Fprintf(os.Stderr, "Warning: Could not stat local env file %s: %v\n", *localEnvFilePath, err)
	}
	fmt.Printf("Generating example file: %s\n", *outputFilePath)

	// --- Process Files ---
	err := generateExampleFile(*envFilePath, *localEnvFilePath, *outputFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nSuccessfully generated %s\n", *outputFilePath)
}

// generateExampleFile orchestrates the reading, processing, and writing.
func generateExampleFile(envPath, localPath, outputPath string) error {
	// Keep track of keys we've already added to the example to handle overrides
	// and ensure uniqueness.
	seenKeys := make(map[string]struct{}) // Using struct{} as a zero-memory value

	// Store the final lines for the .env.example file, preserving order.
	var outputLines []string

	// --- Process the main .env file ---
	err := processEnvFile(envPath, seenKeys, &outputLines, true) // Process comments/blanks
	if err != nil && !os.IsNotExist(err) {                       // It's okay if .env doesn't exist, but error otherwise
		return fmt.Errorf("failed to process base env file %s: %w", envPath, err)
	} else if os.IsNotExist(err) {
		fmt.Printf("Warning: Base env file %s not found, proceeding without it.\n", envPath)
	}

	// --- Process the .env.local file (optional overrides/additions) ---
	err = processEnvFile(localPath, seenKeys, &outputLines, false) // Don't process comments/blanks from local
	if err != nil && !os.IsNotExist(err) {                         // It's okay if .env.local doesn't exist
		// Only warn if we couldn't process it for reasons other than not existing
		fmt.Fprintf(os.Stderr, "Warning: Failed to process local env file %s: %v\n", localPath, err)
	}

	// --- Write the .env.example file ---
	outputContent := strings.Join(outputLines, "\n")
	// Ensure the output directory exists
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	err = os.WriteFile(outputPath, []byte(outputContent), permissionReadWrite)
	if err != nil {
		return fmt.Errorf("failed to write example file %s: %w", outputPath, err)
	}

	return nil
}

// processEnvFile reads a single env file, parses it, and updates the seenKeys and outputLines.
// If includeNonVariables is true, comments and blank lines are added to outputLines.
func processEnvFile(filePath string, seenKeys map[string]struct{}, outputLines *[]string, includeNonVariables bool) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err // Return error to be handled by caller (might be os.ErrNotExist)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		originalLine := scanner.Text() // Keep original for comments/blanks

		// Handle Comments and Blank Lines
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			if includeNonVariables {
				*outputLines = append(*outputLines, originalLine)
			}
			continue // Move to the next line
		}

		// Handle Key-Value Pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			// Line doesn't contain '=', treat as malformed or just a key without value?
			// For safety/simplicity, we'll skip lines without '='.
			// If includeNonVariables, maybe add as a comment? For now, skip.
			if includeNonVariables {
				fmt.Fprintf(os.Stderr, "Warning: Skipping malformed line in %s: %s\n", filePath, originalLine)
				*outputLines = append(*outputLines, "# "+originalLine+" # Skipped Malformed Line")
			}
			continue
		}

		key := strings.TrimSpace(parts[0])
		// Basic validation: Ensure key is not empty and doesn't contain problematic chars (optional)
		if key == "" {
			if includeNonVariables {
				fmt.Fprintf(os.Stderr, "Warning: Skipping line with empty key in %s: %s\n", filePath, originalLine)
				*outputLines = append(*outputLines, "# "+originalLine+" # Skipped Empty Key")
			}
			continue
		}

		// If we haven't seen this key before, add it to the output
		if _, found := seenKeys[key]; !found {
			seenKeys[key] = struct{}{} // Mark key as seen
			anonymizedValue := fmt.Sprintf(anonymizedValueTpl, strings.ToUpper(key))
			*outputLines = append(*outputLines, fmt.Sprintf("%s=%s", key, anonymizedValue))
		}
		// If key was already seen (from .env), we don't add it again when processing .env.local
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	return nil
}
