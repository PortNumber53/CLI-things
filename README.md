# CLI-things

## env-anonymizer

A CLI utility for generating `.env.example` files while preserving comments and file structure. 

### Features
- Reads from `.env` and optional `.env.local` files
- Anonymizes sensitive environment variable values
- Preserves comments and blank lines from the original files
- Supports custom input and output file paths

### Usage
```bash
go run env-anonymizer.go [flags]
```

#### Flags
- `-env`: Path to the main .env file (default: `.env`)
- `-local`: Path to the local .env override file (default: `.env.local`)
- `-output`: Path for the generated .env.example file (default: `.env.example`)

### Example
Given a `.env` file:
```
DATABASE_URL=postgresql://user:password@localhost/mydb
API_KEY=secret123
```

The generated `.env.example` will look like:
```
DATABASE_URL=<DATABASE_URL_VALUE>
API_KEY=<API_KEY_VALUE>
```

This helps developers share environment configuration templates without exposing sensitive information.