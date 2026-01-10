#!/bin/bash

# Installation script for internalip utility
# This script is for local development use. For production deployment, use the Jenkins pipeline.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BINARY_NAME="internalip"
INSTALL_DIR="/usr/local/bin"

echo "Installing internalip utility..."

# Build the binary
echo "Building binary..."
cd "$PROJECT_ROOT"
go build -o "$BINARY_NAME" utility/internalip/main.go

# Install binary
echo "Installing to $INSTALL_DIR..."
sudo cp "$BINARY_NAME" "$INSTALL_DIR/"
sudo chmod 755 "$INSTALL_DIR/$BINARY_NAME"

# Clean up
rm -f "$BINARY_NAME"

echo "Installation complete!"
echo ""
echo "Usage examples:"
echo "  $BINARY_NAME                    # Get preferred IP"
echo "  $BINARY_NAME -all              # Get all IPs"
echo "  $BINARY_NAME -json             # JSON output"
echo "  $BINARY_NAME -store            # Store in database"
echo "  $BINARY_NAME -list             # List stored IPs"
echo ""
echo "For production deployment with Jenkins:"
echo "  The utility will be built and deployed automatically to:"
echo "  - /opt/cli-things/bin/internalip (binary)"
echo "  - /etc/systemd/system/internalip-capture.service (systemd)"
echo "  - /etc/systemd/system/internalip-capture.timer (systemd)"
echo ""
echo "For manual systemd setup (local testing):"
echo "  sudo cp utility/internalip/internalip-capture.service /etc/systemd/system/"
echo "  sudo cp utility/internalip/internalip-capture.timer /etc/systemd/system/"
echo "  sudo systemctl daemon-reload"
echo "  sudo systemctl enable --now internalip-capture.timer"
