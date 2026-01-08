# GDB script to verify memory mappings for kmazarin's g0 location
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-check-mappings.gdb
#
# This script will:
# 1. Connect to QEMU
# 2. Break after kmazarin is loaded but before it starts
# 3. Check if 0xFFFFFFFF4197BF20 (g0 location) is mapped
# 4. Verify page table entries for that address

# Connect to QEMU
target remote localhost:1234

# Disable pagination
set pagination off
set print pretty on
set architecture aarch64

echo \n=== Checking memory mappings for g0 location ===\n\n

# Break right before jumping to kmazarin (at the JMP instruction in Cardinal)
# This is after all page tables are set up
echo Setting breakpoint at Cardinal jump to kmazarin...\n
# The jump is at the end of jump_to_kmazarin function
# We need to find the exact address - let's break at kmazarin entry instead
break *0xFFFFFFFF41871CB0

echo \nContinuing to breakpoint...\n
continue

echo \n=== Stopped at kmazarin entry ===\n
info register pc ttbr0_el1 ttbr1_el1 tcr_el1

echo \n=== Checking if g0 address is accessible ===\n
set $g0_addr = 0xFFFFFFFF4197BF20
printf "\nTrying to read from g0 address: 0x%016lx\n", $g0_addr

# Try to read memory
echo Reading 8 bytes from g0:\n
x/2gx $g0_addr

echo \n=== Walking TTBR1 page tables for g0 address ===\n

# Get TTBR1_EL1
set $ttbr1 = $ttbr1_el1
printf "TTBR1_EL1:        0x%016lx\n", $ttbr1

# Extract page table base (clear lower 12 bits)
set $l0_table = $ttbr1 & ~0xFFF
printf "L0 table base:    0x%016lx\n", $l0_table

# Calculate indices for 0xFFFFFFFF4197BF20
# 48-bit VA split: [47:39]=L0, [38:30]=L1, [29:21]=L2, [20:12]=L3, [11:0]=offset
set $va = $g0_addr
set $l0_idx = ($va >> 39) & 0x1FF
set $l1_idx = ($va >> 30) & 0x1FF
set $l2_idx = ($va >> 21) & 0x1FF
set $l3_idx = ($va >> 12) & 0x1FF
set $offset = $va & 0xFFF

printf "\nVA:               0x%016lx\n", $va
printf "L0 index:         0x%03x (%d)\n", $l0_idx, $l0_idx
printf "L1 index:         0x%03x (%d)\n", $l1_idx, $l1_idx
printf "L2 index:         0x%03x (%d)\n", $l2_idx, $l2_idx
printf "L3 index:         0x%03x (%d)\n", $l3_idx, $l3_idx
printf "Page offset:      0x%03x (%d)\n", $offset, $offset

# Walk L0
set $l0_entry_addr = $l0_table + ($l0_idx * 8)
set $l0_entry = *(uint64_t*)$l0_entry_addr
printf "\nL0[0x%03x] @ 0x%016lx = 0x%016lx\n", $l0_idx, $l0_entry_addr, $l0_entry

if (($l0_entry & 0x3) == 0x3)
    echo   Valid table descriptor\n
    set $l1_table = $l0_entry & ~0xFFF
    printf "  L1 table:       0x%016lx\n", $l1_table

    # Walk L1
    set $l1_entry_addr = $l1_table + ($l1_idx * 8)
    set $l1_entry = *(uint64_t*)$l1_entry_addr
    printf "\nL1[0x%03x] @ 0x%016lx = 0x%016lx\n", $l1_idx, $l1_entry_addr, $l1_entry

    if (($l1_entry & 0x3) == 0x3)
        echo   Valid table descriptor\n
        set $l2_table = $l1_entry & ~0xFFF
        printf "  L2 table:       0x%016lx\n", $l2_table

        # Walk L2
        set $l2_entry_addr = $l2_table + ($l2_idx * 8)
        set $l2_entry = *(uint64_t*)$l2_entry_addr
        printf "\nL2[0x%03x] @ 0x%016lx = 0x%016lx\n", $l2_idx, $l2_entry_addr, $l2_entry

        if (($l2_entry & 0x3) == 0x3)
            echo   Valid table descriptor\n
            set $l3_table = $l2_entry & ~0xFFF
            printf "  L3 table:       0x%016lx\n", $l3_table

            # Walk L3
            set $l3_entry_addr = $l3_table + ($l3_idx * 8)
            set $l3_entry = *(uint64_t*)$l3_entry_addr
            printf "\nL3[0x%03x] @ 0x%016lx = 0x%016lx\n", $l3_idx, $l3_entry_addr, $l3_entry

            if (($l3_entry & 0x3) == 0x3)
                echo   Valid page descriptor\n
                set $phys_page = $l3_entry & ~0xFFF
                set $phys_addr = $phys_page | $offset
                printf "  Physical page:  0x%016lx\n", $phys_page
                printf "  Physical addr:  0x%016lx\n", $phys_addr

                # Check permissions
                set $ap = ($l3_entry >> 6) & 0x3
                set $pxn = ($l3_entry >> 53) & 0x1
                set $uxn = ($l3_entry >> 54) & 0x1
                printf "  AP bits:        %d ", $ap
                if ($ap == 0)
                    echo (RW @ EL1+EL0)
                else
                    if ($ap == 1)
                        echo (RW @ EL1)
                    else
                        if ($ap == 2)
                            echo (RO @ EL1+EL0)
                        else
                            echo (RO @ EL1)
                        end
                    end
                end
                printf "  PXN:            %d\n", $pxn
                printf "  UXN:            %d\n", $uxn

                echo \n*** g0 address IS MAPPED ***\n
            else
                echo   INVALID page descriptor!\n
                echo \n*** g0 address NOT MAPPED (no L3 entry) ***\n
            end
        else
            echo   INVALID L2 descriptor!\n
            echo \n*** g0 address NOT MAPPED (no L2 table) ***\n
        end
    else
        echo   INVALID L1 descriptor!\n
        echo \n*** g0 address NOT MAPPED (no L1 table) ***\n
    end
else
    echo   INVALID L0 descriptor!\n
    echo \n*** g0 address NOT MAPPED (no L0 table) ***\n
end

echo \nSession complete. Type 'continue' to run or 'quit' to exit.\n
