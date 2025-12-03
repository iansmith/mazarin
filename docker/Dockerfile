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

# Set default entrypoint to run QEMU emulating Raspberry Pi 4
# -M raspi4b: Raspberry Pi 4B machine type
# -kernel: Load kernel from /mnt/builtin/kernel.elf
# -append: Kernel command line arguments
# -serial stdio: Redirect serial output to stdout/stderr
# -display none: Run headless (no graphics)
# -no-reboot: Exit on shutdown instead of rebooting
# Note: To enable GDB debugging, override with: -gdb tcp::1234 -S
ENTRYPOINT ["qemu-system-aarch64", "-M", "raspi4b", "-kernel", "/mnt/builtin/kernel.elf", "-append", "console=ttyAMA0,115200 earlyprintk", "-serial", "stdio", "-display", "none", "-no-reboot"]

