#!/bin/bash

# Docker cleanup script - removes all containers and images except alpine-qemu:3.22 and alpine:3.22
# Usage: ./cleanup-docker.sh

set -e

echo "Docker Cleanup: Removing all containers and images except alpine-qemu:3.22"
echo "=========================================================================="
echo ""

# Remove all containers (stopped and running)
echo "1. Removing all containers..."
CONTAINERS=$(docker ps -aq)
if [ -n "$CONTAINERS" ]; then
    echo "$CONTAINERS" | xargs docker rm -f 2>/dev/null || true
else
    echo "  No containers to remove"
fi

# Remove all images except alpine-qemu:3.22 and alpine:3.22
echo "2. Removing all images except alpine-qemu:3.22 and alpine:3.22..."
IMAGES_TO_REMOVE=$(docker images --format "{{.Repository}}:{{.Tag}}" | \
  grep -v "^alpine-qemu:3.22$" | \
  grep -v "^alpine:3.22$" | \
  grep -v "^REPOSITORY" | \
  grep -v "^$")
if [ -n "$IMAGES_TO_REMOVE" ]; then
    echo "$IMAGES_TO_REMOVE" | xargs docker rmi -f 2>/dev/null || true
else
    echo "  No images to remove"
fi

# Clean up dangling images
echo "3. Removing dangling images..."
docker image prune -af 2>/dev/null || echo "  No dangling images"

# Clean up volumes
echo "4. Removing unused volumes..."
docker volume prune -f 2>/dev/null || echo "  No unused volumes"

# Clean up build cache
echo "5. Removing build cache..."
docker builder prune -af 2>/dev/null || echo "  Build cache cleaned"

echo ""
echo "Cleanup complete! Remaining images:"
docker images

echo ""
echo "To rebuild the QEMU container:"
echo "  cd docker && docker build -t alpine-qemu:3.22 ."
