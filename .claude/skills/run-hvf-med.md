---
description: Build and run ARM64 HVF with a 75 second timeout
user-invocable: true
---

Build and run the ARM64 HVF version of the code with a 75 second timeout. Use the safe serial reader to view output.

```bash
export GOTOOLCHAIN=auto GO=/Users/iansmith/sdk/go1.25.5/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
$GO tool task run-arm64-hvf TIMEOUT=75
```

After the run completes, read the output with:
```bash
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```
