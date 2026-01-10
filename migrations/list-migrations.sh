#!/bin/bash

# List all available migrations in the CLI-things project

echo "CLI-things Database Migrations"
echo "=============================="
echo

MIGRATIONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ ! -d "$MIGRATIONS_DIR" ]; then
    echo "Error: Migrations directory not found at $MIGRATIONS_DIR"
    exit 1
fi

echo "Migration files in $MIGRATIONS_DIR:"
echo

for file in "$MIGRATIONS_DIR"/*.sql; do
    if [ -f "$file" ]; then
        filename=$(basename "$file")
        echo "📄 $filename"

        # Extract first comment line for description
        first_comment=$(head -n 10 "$file" | grep "^--" | head -n 1 | sed 's/^-- *//')
        if [ -n "$first_comment" ] && [ "$first_comment" != "" ]; then
            echo "   $first_comment"
        fi

        # Show file size
        size=$(du -h "$file" | cut -f1)
        echo "   Size: $size"
        echo
    fi
done

echo "Total migrations: $(ls -1 "$MIGRATIONS_DIR"/*.sql 2>/dev/null | wc -l)"
echo
echo "These migrations are automatically applied by utilities that use database storage."
echo "See $MIGRATIONS_DIR/README.md for more information."
