#!/bin/bash

APP_NAME="bsky-mcp"
OUTPUT_DIR="build"

mkdir -p ${OUTPUT_DIR}

PLATFORMS=(
    "linux/amd64"
    "windows/amd64"
    "darwin/amd64"
    "darwin/arm64"
)

for platform in "${PLATFORMS[@]}"
do
    GOOS=$(echo $platform | cut -f1 -d'/')
    GOARCH=$(echo $platform | cut -f2 -d'/')

    OUTPUT_NAME="${APP_NAME}-${GOOS}-${GOARCH}"

    if [ "$GOOS" = "windows" ]; then
        OUTPUT_NAME+=".exe"
    fi

    echo "Building for ${GOOS}/${GOARCH}..."
    env GOOS=${GOOS} GOARCH=${GOARCH} go build -o "${OUTPUT_DIR}/${OUTPUT_NAME}" .

    if [ $? -ne 0 ]; then
        echo "Failed to build for ${GOOS}/${GOARCH}"
        exit 1
    fi
done

echo "All builds completed successfully!"
