# Use Alpine Linux 3.22 as base image
# Note: If 3.22 is not available, use alpine:latest or alpine:3.20
FROM alpine:3.22

# Update package index and install QEMU with all dependencies
# Installing qemu-system-x86_64 will pull in most core dependencies
RUN apk update && \
    apk add --no-cache \
    qemu-system-x86_64 \
    qemu-system-aarch64 \
    qemu-img \
    qemu-modules \
    qemu-ui-gtk \
    qemu-ui-sdl \
    qemu-ui-spice-app \
    qemu-ui-spice-core \
    qemu-audio-alsa \
    qemu-audio-pa \
    qemu-audio-sdl \
    qemu-chardev-spice \
    qemu-hw-display-virtio-gpu-gl \
    qemu-hw-display-virtio-gpu-pci \
    qemu-hw-display-virtio-vga \
    qemu-hw-usb-redirect \
    qemu-pr-helper \
    qemu-tools \
    && rm -rf /var/cache/apk/*

# Verify QEMU installation
RUN qemu-system-x86_64 --version && \
    qemu-img --version

# Set hostname
RUN echo "qemu-container" > /etc/hostname

# Set working directory
WORKDIR /workspace

# Set default entrypoint to run QEMU with virt machine type
# -M virt: QEMU virt machine type (better UART compatibility for bare-metal)
# -cpu cortex-a72: Use Cortex-A72 CPU (same as Raspberry Pi 4)
# -m 1G: Allocate 1GB RAM
# -kernel: Load kernel from /mnt/builtin/kernel.elf
# -serial stdio: Redirect serial output to stdout/stderr
# -semihosting: Enable semihosting for clean program exit
# -display none: Run headless (no graphics)
# -no-reboot: Exit on shutdown instead of rebooting
# Note: No -append flag for bare-metal kernels (that's for Linux kernels)
# Note: To enable GDB debugging, override with: -gdb tcp::1234 -S
ENTRYPOINT ["qemu-system-aarch64", "-M", "virt", "-cpu", "cortex-a72", "-m", "1G", "-kernel", "/mnt/builtin/kernel.elf", "-serial", "stdio", "-semihosting", "-display", "none", "-no-reboot"]

