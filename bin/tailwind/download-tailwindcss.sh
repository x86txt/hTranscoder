#!/bin/bash

# file name
TAILWIND_BIN="tailwindcss"
TAILWIND_BIN_PATH="${PWD}/${TAILWIND_BIN}"

echo "Downloading Tailwind CSS CLI..."

# OS detector
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
  curl -L https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64 -o "$TAILWIND_BIN"
elif [[ "$OSTYPE" == "darwin"* ]]; then
  curl -L https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-macos-arm64 -o "$TAILWIND_BIN"
else
  echo "OS not supported. Try to download manually"
  exit 1
fi

# Set permission to make the file executable
echo "Setting the file as executable..."
echo "$PWD"
chmod +x "$TAILWIND_BIN_PATH"

echo "Tailwind CSS CLI is Installed!"
echo "Executable path: $TAILWIND_BIN_PATH" 