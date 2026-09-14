#!/bin/bash
# build_v70.sh — 40HX (TU106) v90 诊断构建
# =============================================================================
# v90 = v88（当前部署, SEC2 falcon_dma_transfer 装载, 已证明不黑屏）+ 只读前置诊断
#   - 不改装载引擎 / 不改 SEC2 状态操作 → 黑屏行为与 v88 完全一致
#   - 仅新增 SEC2 CPUCTL/DMATRFCMD/BCR/HWCFG2/RESET_PLM/WPR2 只读打印（v90 pre-flight）
# 工具链：MSYS2 便携版 D:\Code\40HX\.scratch\msys64（PATH=/mingw64/bin:/usr/bin）
# =============================================================================
set -euo pipefail
SRCDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SRCDIR"

# Prefer the native Arch toolchain.  If this script is run under MSYS2
# MINGW64, keep the original /mingw64/bin toolchain available.
if [ -d /mingw64/bin ]; then
  export PATH=/mingw64/bin:/usr/bin:$PATH
else
  export PATH=/usr/bin:$PATH
fi

EFI_INC="${EFI_INC:-/usr/include/efi}"
EFI_LIB="${EFI_LIB:-/usr/lib}"

SRC=unlock40x_v70.c
OBJ="${OBJ:-unlock40x_v70.o}"
OUT="${OUT:-unlock40x_v70.so}"
EFIOUT="${EFI_OUT:-unlock40x_v70.efi}"

# Do not let a failed link or conversion reuse an output from an earlier run.
rm -f -- "$OBJ" "$OUT" "$EFIOUT"

# The normal release keeps its proven Windows chainload behaviour.  Set
# NO_AUTO_CHAINLOAD=1 only when building the experimental Limine variant; it
# returns to the parent EFI boot manager after the unlock instead of starting
# bootmgfw.efi itself.
EXTRA_DEFS=()
if [ "${NO_AUTO_CHAINLOAD:-0}" = "1" ]; then
  EXTRA_DEFS+=("-DNO_AUTO_CHAINLOAD")
fi

echo "=== 1. compile $SRC ==="
gcc -c -O2 -fno-stack-protector -fno-pie -ffreestanding \
    -fno-asynchronous-unwind-tables -fno-unwind-tables \
    -fshort-wchar -mno-red-zone -maccumulate-outgoing-args \
    -fno-builtin -fno-strict-aliasing -Wno-unused-function \
    -I "$EFI_INC" -I "$EFI_INC/x86_64" \
    -DDIRECT_SEC2 -DRELEASE_BUILD -DVBIOS_DUMP "${EXTRA_DEFS[@]}" \
    -o "$OBJ" "$SRC"

echo "=== 2. embed blobs ==="
embed() {
  local f="$1" tgt="$2"
  local base="${f//./_}"
  objcopy --input-target binary --output-target pe-x86-64 \
    --binary-architecture i386:x86-64 \
    --redefine-sym "_binary_${base}_start=${tgt}" \
    --redefine-sym "_binary_${base}_end=${tgt}_end" \
    --redefine-sym "_binary_${base}_size=${tgt}_size" \
    "$f" "${tgt}.o"
  echo "embedded $f -> $tgt"
}
embed v67_payload.bin         v67_payload_bin
embed booter_ucode_dbg.bin    booter_ucode_dbg
embed booter_ucode_prod.bin   booter_ucode_prod
embed gsp_rm_boot_dbg.bin     gsp_rm_boot_dbg
embed fwsec_ga102.bin         fwsec_ga102_bin
embed fwsec_ga102_sig.bin     fwsec_ga102_sig
embed fwsec_40hx_prod.bin     fwsec_40hx_prod_bin
embed fwsec_40hx_dbg.bin      fwsec_40hx_dbg_bin
embed sec2_ucode_vbios_49.bin sec2_ucode_vbios_49
embed sec2_ucode_vbios_89.bin sec2_ucode_vbios_89
embed bl_gsp_tu102.bin        gsp_bl_tu102

OBJS="$OBJ v67_payload_bin.o booter_ucode_dbg.o booter_ucode_prod.o \
gsp_rm_boot_dbg.o fwsec_ga102_bin.o fwsec_ga102_sig.o \
fwsec_40hx_prod_bin.o fwsec_40hx_dbg_bin.o \
sec2_ucode_vbios_49.o sec2_ucode_vbios_89.o gsp_bl_tu102.o"

echo "=== 3. link ==="
# Keep the full linker diagnostics.  With set -euo pipefail, a failed link
# must abort the build instead of leaving a stale/partial EFI behind.
ld -mi386pep \
  --subsystem 10 \
  --image-base 0 \
  -e u40x_entry \
  -o "$OUT" \
  $OBJS "$EFI_LIB/libefi.a"

echo "=== 4. convert ==="
if [ ! -f "$OUT" ]; then
  echo "LINK_FAIL: $OUT was not produced" >&2
  exit 1
fi

# IMPORTANT: do NOT extract only .text/.data/.rdata/.reloc here.
# Native Arch GCC/gnu-efi emits additional loadable PE sections such as:
#   .rodata, .rodata.str2.*, .data.rel.local, .data.rel.ro.local
# Dropping them produces a PE that can look structurally valid but is missing
# referenced constants/global data.  The original MSYS2 build did not expose
# this because its section layout differs.
# The linker output is already a PE32+ EFI application (subsystem 10), so just
# strip debug/non-loadable comment data while preserving all loadable sections.
objcopy --strip-debug \
  --remove-section=.comment \
  --output-target=efi-app-x86_64 "$OUT" "$EFIOUT"

if [ ! -f "$EFIOUT" ]; then
  echo "CONVERT_FAIL: $EFIOUT was not produced" >&2
  exit 1
fi
ls -la "$EFIOUT"
echo v70_EFI_OK
echo DONE
