# QEMU Functional Changes for Framebuffer Support

## Summary

Our custom QEMU build (based on v10.1.2) includes ONE critical functional change that enables framebuffer initialization to work:

## The Critical Change: Restore fw_cfg_write()

### File: `hw/nvram/fw_cfg.c`

**The Problem:**
In QEMU v2.4+, the traditional fw_cfg write interface was completely removed. The function body was replaced with a comment:

```c
static void fw_cfg_write(FWCfgState *s, uint8_t value)
{
    /* nothing, write support removed in QEMU v2.4+ */
}
```

**The Solution:**
We restored the full implementation of `fw_cfg_write()`:

```c
static void fw_cfg_write(FWCfgState *s, uint8_t value)
{
    /* Traditional interface write support - attempt to restore functionality */
    int arch;
    FWCfgEntry *e;
    
    if (s->cur_entry == FW_CFG_INVALID) {
        return;
    }
    
    arch = !!(s->cur_entry & FW_CFG_ARCH_LOCAL);
    e = &s->entries[arch][s->cur_entry & FW_CFG_ENTRY_MASK];
    
    // Try to write if entry allows it and has data buffer
    if (e->allow_write && e->data && s->cur_offset < e->len) {
        e->data[s->cur_offset] = value;
        s->cur_offset++;
        
        // CRITICAL: Trigger write callback when all bytes written
        if (s->cur_offset == e->len && e->write_cb) {
            e->write_cb(e->callback_opaque, 0, e->len);
        }
    }
}
```

### Why This Matters

Our kernel writes the ramfb configuration byte-by-byte using the traditional fw_cfg interface:
1. Write selector (0x25 for etc/ramfb)
2. Write 28 bytes of configuration data via `mmio_write()` calls
3. QEMU's `fw_cfg_write()` handler processes each byte
4. After all 28 bytes are written, the write callback triggers
5. The callback in QEMU's ramfb device processes the configuration

**Without this function, steps 3-5 never happen**, and the framebuffer is never initialized.

## Why Homebrew QEMU Doesn't Work

Homebrew's QEMU 10.1.2 is vanilla upstream without this restoration, so it has the empty `fw_cfg_write()` function and cannot process traditional interface writes.

## Secondary Change: macOS Compatibility

### File: `util/cpuinfo-aarch64.c`

```c
-    assert(errno == ENOENT);
+    // assert(errno == ENOENT); // Disabled for macOS compatibility
```

This allows the QEMU binary to run on macOS without assertion failures related to sysctlbyname behavior differences.

## Debug Output

All `fprintf(stderr, "*** FW_CFG_DEBUG: ...")` statements were added for troubleshooting and are NOT functional requirements. They can be removed without affecting operation.

## Conclusion

The framebuffer works on our custom QEMU because we restored the traditional fw_cfg write interface that upstream QEMU removed in v2.4+. This is the single functional change that enables ramfb configuration via writes.
