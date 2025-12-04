# Docker Maintenance and Setup Guide

This document explains how to maintain the Docker environment for testing the Mazarin kernel, including building the QEMU container image and cleaning up Docker resources.

## Overview

The kernel testing environment uses Docker to run QEMU in a reproducible container. This ensures all developers have the same testing environment regardless of their host OS.

**Key Components:**
- **Container Image**: `alpine-qemu:3.22` - Alpine Linux 3.22 with QEMU system emulator
- **Cleanup Script**: `docker/cleanup-docker.sh` - Removes unnecessary Docker resources

## Building the Docker Container

### Prerequisites

- Docker must be installed and running
- Internet connection (to download Alpine Linux base image)

### Building the Image

The Docker container image is built from the `docker/Dockerfile`. To build it:

```bash
cd docker
docker build -t alpine-qemu:3.22 .
```

**What this does:**
- Downloads Alpine Linux 3.22 base image
- Installs QEMU system emulator (`qemu-system-aarch64`) and dependencies
- Configures the container to run QEMU with Raspberry Pi 4B emulation
- Sets default entrypoint to load kernel from `/mnt/builtin/kernel.elf`

**Build time:** Typically 2-5 minutes depending on internet speed.

### Verifying the Build

After building, verify the image exists:

```bash
docker images | grep alpine-qemu
```

You should see:
```
alpine-qemu   3.22    <image-id>   <time>   <size>
```

### Rebuilding the Image

If you need to rebuild the image (e.g., after changing the Dockerfile):

```bash
cd docker
docker build -t alpine-qemu:3.22 . --no-cache
```

The `--no-cache` flag forces a complete rebuild, ignoring cached layers.

## Docker Maintenance

### Why Clean Up Docker?

Over time, Docker can accumulate:
- Stopped containers from previous test runs
- Unused images (intermediate build layers, old versions)
- Dangling images (untagged, left over from rebuilds)
- Unused volumes
- Build cache

These can consume significant disk space. Regular cleanup keeps your Docker environment clean and efficient.

### Using the Cleanup Script

The project includes a cleanup script at `docker/cleanup-docker.sh` that safely removes all Docker resources **except** the required images.

#### What It Removes

1. **All containers** (stopped and running)
2. **All images** except:
   - `alpine-qemu:3.22` (our custom QEMU container)
   - `alpine:3.22` (base image for alpine-qemu)
3. **Dangling images** (untagged images)
4. **Unused volumes**
5. **Build cache**

#### Running the Cleanup Script

```bash
docker/cleanup-docker.sh
```

The script will:
1. Show what will be cleaned up
2. Ask for confirmation (if you want to skip confirmation, see below)
3. Remove containers, images, volumes, and cache
4. Display remaining images for verification

#### Example Output

```
Docker Cleanup: Removing all containers and images except alpine-qemu:3.22
==========================================================================

1. Removing all containers...
2. Removing all images except alpine-qemu:3.22 and alpine:3.22...
3. Removing dangling images...
4. Removing unused volumes...
5. Removing build cache...

Cleanup complete! Remaining images:
REPOSITORY      TAG       IMAGE ID       CREATED        SIZE
alpine-qemu     3.22      abc123def456   2 hours ago    450MB
alpine          3.22      def456ghi789   2 days ago     8MB
```

### Manual Cleanup Commands

If you prefer to clean up manually, here are the individual commands:

#### Remove All Containers

```bash
docker ps -aq | xargs -r docker rm -f
```

#### Remove All Images Except Required Ones

```bash
docker images --format "{{.Repository}}:{{.Tag}}" | \
  grep -v "^alpine-qemu:3.22$" | \
  grep -v "^alpine:3.22$" | \
  grep -v "^REPOSITORY" | \
  xargs -r docker rmi -f
```

#### Clean Up Dangling Resources

```bash
# Remove dangling images
docker image prune -af

# Remove unused volumes
docker volume prune -f

# Remove build cache
docker builder prune -af
```

### Complete Cleanup (One-Liner)

For quick cleanup without using the script:

```bash
docker ps -aq | xargs -r docker rm -f && \
docker images --format "{{.Repository}}:{{.Tag}}" | grep -v "^alpine-qemu:3.22$" | grep -v "^alpine:3.22$" | grep -v "^REPOSITORY" | xargs -r docker rmi -f && \
docker image prune -af && \
docker volume prune -f && \
docker builder prune -af
```

## Maintenance Workflow

### First-Time Setup

1. **Build the Docker image:**
   ```bash
   cd docker
   docker build -t alpine-qemu:3.22 .
   ```

2. **Verify the build:**
   ```bash
   docker images | grep alpine-qemu
   ```

3. **Test the container:**
   ```bash
   cd src
   make push
   docker/runqemu
   ```

### Regular Maintenance

Run cleanup periodically (weekly or when Docker disk usage is high):

```bash
docker/cleanup-docker.sh
```

### After Changing Dockerfile

If you modify `docker/Dockerfile`, rebuild the image:

```bash
cd docker
docker build -t alpine-qemu:3.22 . --no-cache
```

Then run cleanup to remove the old image:

```bash
docker/cleanup-docker.sh
```

## Troubleshooting

### "Image not found" Error

If you get an error that `alpine-qemu:3.22` image is not found:

```bash
cd docker
docker build -t alpine-qemu:3.22 .
```

### Credential Helper Error

If you see an error like:
```
ERROR: failed to build: failed to solve: error getting credentials - err: exec: "docker-credential-desktop": executable file not found in $PATH
```

This happens when Docker can't find the credential helper. Since Alpine Linux is a public image, you can bypass this:

**Solution 1: Temporarily disable credential store (recommended)**

Edit `~/.docker/config.json` and temporarily remove or comment out the `credsStore` line:

```json
{
  "auths": {
    "https://index.docker.io/v1/": {}
  },
  "currentContext": "desktop-linux",
  ...
}
```

Then rebuild. You can restore `"credsStore": "desktop"` later if needed for private registries.

**Solution 2: Use Docker buildx (bypasses credential helper)**

```bash
cd docker
DOCKER_BUILDKIT=1 docker buildx build -t alpine-qemu:3.22 --load .
```

**Solution 3: Ensure credential helper is in PATH**

If you need the credential helper, make sure it's accessible:

```bash
which docker-credential-desktop
# Should show: /usr/local/bin/docker-credential-desktop

# If not found, add to PATH:
export PATH="/usr/local/bin:$PATH"
cd docker
docker build -t alpine-qemu:3.22 .
```

### Docker Disk Space Issues

If Docker is using too much disk space:

1. Check current usage:
   ```bash
   docker system df
   ```

2. Run the cleanup script:
   ```bash
   docker/cleanup-docker.sh
   ```

3. For more aggressive cleanup (removes ALL unused resources):
   ```bash
   docker system prune -a --volumes
   ```
   **Warning:** This removes ALL unused images, containers, volumes, and networks. You'll need to rebuild `alpine-qemu:3.22` afterwards.

### Container Won't Start

If the container won't start:

1. Verify the image exists:
   ```bash
   docker images | grep alpine-qemu
   ```

2. Check if `kernel.elf` is in the right place:
   ```bash
   ls -lh docker/builtin/kernel.elf
   ```

3. Try running the container manually:
   ```bash
   docker run --rm -it \
     -v "$(pwd)/docker/builtin:/mnt/builtin:ro" \
     alpine-qemu:3.22
   ```

### Cleanup Script Won't Run

If the cleanup script isn't executable:

```bash
chmod +x docker/cleanup-docker.sh
```

## Docker Image Details

### Image Specifications

- **Base Image**: `alpine:3.22`
- **Tag**: `alpine-qemu:3.22`
- **Size**: ~450 MB (after installation)
- **Architecture**: Supports both x86_64 and AArch64 emulation

### What's Installed

- `qemu-system-aarch64` - AArch64 system emulator (for Raspberry Pi 4)
- `qemu-system-x86_64` - x86_64 system emulator
- QEMU modules and tools
- All necessary dependencies

### Container Configuration

- **Default Entrypoint**: Runs QEMU with Raspberry Pi 4B emulation
- **Kernel Location**: Loads from `/mnt/builtin/kernel.elf`
- **Serial Output**: Redirected to stdout/stderr (headless mode)
- **Memory**: Uses default QEMU memory settings for Raspberry Pi 4

## Best Practices

1. **Regular Cleanup**: Run cleanup weekly to prevent disk space issues
2. **Rebuild After Changes**: Always rebuild the image after modifying Dockerfile
3. **Verify Before Cleanup**: Check `docker images` before cleanup to ensure you're not removing needed images
4. **Keep Base Image**: Don't remove `alpine:3.22` as it's required by `alpine-qemu:3.22`

## References

- [Docker Documentation](https://docs.docker.com/)
- [QEMU Documentation](https://www.qemu.org/documentation/)
- [Alpine Linux](https://www.alpinelinux.org/)
- Project `docker/Dockerfile` - Container configuration
- Project `docker/runqemu*` scripts - Testing scripts

## Summary

**Quick Reference:**

- **Build container**: `cd docker && docker build -t alpine-qemu:3.22 .`
- **Cleanup**: `docker/cleanup-docker.sh`
- **Check images**: `docker images`
- **Check disk usage**: `docker system df`

---

*Last updated: Based on project Docker setup as of implementation*

