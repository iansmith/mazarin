# GDB script to walk ARM64 page tables for address 0x48000000
# Usage: gdb -x debug_walk_page_tables.gdb build/mazboot/mazboot.elf

# Connect to QEMU
target remote :1234

# Load symbol file
file build/mazboot/mazboot.elf

# Helper function to print page table entry
define print_pte
  set $pte = $arg0
  printf "  PTE: 0x%016lx\n", $pte

  # Check valid bit (bit 0)
  if ($pte & 0x1) == 0
    printf "    INVALID (bit 0 not set)\n"
  else
    printf "    VALID\n"

    # Check if this is a block/page (bit 1)
    if ($pte & 0x2) == 0
      printf "    TABLE descriptor (points to next level)\n"
    else
      printf "    BLOCK/PAGE descriptor (final mapping)\n"
    end

    # Extract output address (bits 47:12 for 4KB granule)
    set $pa = ($pte & 0x0000FFFFFFFFF000)
    printf "    Physical Address: 0x%016lx\n", $pa

    # Memory attributes (bits 4:2 - AttrIndx)
    set $attr = ($pte >> 2) & 0x7
    printf "    AttrIndx: %d ", $attr
    if $attr == 0
      printf "(Device-nGnRnE)\n"
    else
      if $attr == 1
        printf "(Normal Cacheable)\n"
      else
        printf "(Unknown)\n"
      end
    end

    # Access permissions (bits 7:6 - AP)
    set $ap = ($pte >> 6) & 0x3
    printf "    AP: %d ", $ap
    if $ap == 0
      printf "(RW EL1 only)\n"
    else
      if $ap == 1
        printf "(RW EL1+EL0)\n"
      else
        if $ap == 2
          printf "(RO EL1 only)\n"
        else
          printf "(RO EL1+EL0)\n"
        end
      end
    end

    # Execute Never (bit 54 - UXN/XN)
    if ($pte & (1 << 54)) != 0
      printf "    XN: Execute Never\n"
    else
      printf "    XN: Executable\n"
    end
  end
end

# Walk page tables for a given virtual address
define walk_tables
  set $va = $arg0
  printf "\n=== Walking page tables for VA 0x%016lx ===\n\n", $va

  # Read TTBR0_EL1 (contains L0 table base address)
  set $ttbr0 = $TTBR0_EL1
  printf "TTBR0_EL1: 0x%016lx\n\n", $ttbr0

  # Extract table base (bits 47:12 for 4KB granule)
  set $l0_base = $ttbr0 & 0x0000FFFFFFFFF000
  printf "L0 Table Base: 0x%016lx\n", $l0_base

  # Extract L0 index (bits 47:39 for 4-level, 4KB granule)
  set $l0_idx = ($va >> 39) & 0x1FF
  printf "L0 Index: %d (bits 47:39 of VA)\n", $l0_idx

  # Read L0 entry
  set $l0_entry_addr = $l0_base + ($l0_idx * 8)
  set $l0_entry = *(unsigned long*)$l0_entry_addr
  printf "L0 Entry Address: 0x%016lx\n", $l0_entry_addr
  print_pte $l0_entry

  # Check if L0 entry is valid
  if ($l0_entry & 0x1) == 0
    printf "\n*** L0 entry invalid - VA not mapped! ***\n"
  else
    # L0 points to L1 table
    set $l1_base = $l0_entry & 0x0000FFFFFFFFF000
    printf "\nL1 Table Base: 0x%016lx\n", $l1_base

    # Extract L1 index (bits 38:30)
    set $l1_idx = ($va >> 30) & 0x1FF
    printf "L1 Index: %d (bits 38:30 of VA)\n", $l1_idx

    # Read L1 entry
    set $l1_entry_addr = $l1_base + ($l1_idx * 8)
    set $l1_entry = *(unsigned long*)$l1_entry_addr
    printf "L1 Entry Address: 0x%016lx\n", $l1_entry_addr
    print_pte $l1_entry

    # Check if L1 entry is a block mapping (1GB) or table
    if ($l1_entry & 0x2) == 0
      # Table descriptor - points to L2
      if ($l1_entry & 0x1) != 0
        set $l2_base = $l1_entry & 0x0000FFFFFFFFF000
        printf "\nL2 Table Base: 0x%016lx\n", $l2_base

        # Extract L2 index (bits 29:21)
        set $l2_idx = ($va >> 21) & 0x1FF
        printf "L2 Index: %d (bits 29:21 of VA)\n", $l2_idx

        # Read L2 entry
        set $l2_entry_addr = $l2_base + ($l2_idx * 8)
        set $l2_entry = *(unsigned long*)$l2_entry_addr
        printf "L2 Entry Address: 0x%016lx\n", $l2_entry_addr
        print_pte $l2_entry

        # Check if L2 entry is a block mapping (2MB) or table
        if ($l2_entry & 0x2) == 0
          # Table descriptor - points to L3
          if ($l2_entry & 0x1) != 0
            set $l3_base = $l2_entry & 0x0000FFFFFFFFF000
            printf "\nL3 Table Base: 0x%016lx\n", $l3_base

            # Extract L3 index (bits 20:12)
            set $l3_idx = ($va >> 12) & 0x1FF
            printf "L3 Index: %d (bits 20:12 of VA)\n", $l3_idx

            # Read L3 entry (final page)
            set $l3_entry_addr = $l3_base + ($l3_idx * 8)
            set $l3_entry = *(unsigned long*)$l3_entry_addr
            printf "L3 Entry Address: 0x%016lx\n", $l3_entry_addr
            print_pte $l3_entry

            # Extract final physical address
            if ($l3_entry & 0x1) != 0
              set $page_base = $l3_entry & 0x0000FFFFFFFFF000
              set $offset = $va & 0xFFF
              set $final_pa = $page_base + $offset
              printf "\n*** Final Physical Address: 0x%016lx ***\n", $final_pa
            else
              printf "\n*** L3 entry invalid - Page not mapped! ***\n"
            end
          else
            printf "\n*** L2 entry invalid - VA not mapped! ***\n"
          end
        else
          # L2 block mapping (2MB)
          printf "\n*** L2 block mapping (2MB) ***\n"
          set $block_base = $l2_entry & 0x0000FFFFFE00000
          set $offset = $va & 0x1FFFFF
          set $final_pa = $block_base + $offset
          printf "Final Physical Address: 0x%016lx\n", $final_pa
        end
      else
        printf "\n*** L1 entry invalid - VA not mapped! ***\n"
      end
    else
      # L1 block mapping (1GB)
      printf "\n*** L1 block mapping (1GB) ***\n"
      set $block_base = $l1_entry & 0x0000FFFFC0000000
      set $offset = $va & 0x3FFFFFFF
      set $final_pa = $block_base + $offset
      printf "Final Physical Address: 0x%016lx\n", $final_pa
    end
  end
  printf "\n"
end

# Break after first mmap completes
break main.SyscallMmap
commands
  silent
  # Use finish to get return value
  finish
  # Check if this is our target address
  if $x0 == 0x48000000
    printf "\n=== main.SyscallMmap returned 0x48000000 - checking page tables ===\n"
    walk_tables 0x48000000
  end
  continue
end

printf "GDB script loaded.\n"
printf "This will walk page tables for address 0x48000000 after mmap returns.\n"
printf "\nYou can also manually walk tables with: walk_tables 0xADDRESS\n\n"

# Start execution
continue
