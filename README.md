# CMP 40HX Windows Unlock

UEFI and Windows tools for NVIDIA CMP 40HX (TU106, PCI ID `10de:1f0b`). The
project restores the compute/Tensor path and brings the endpoint to PCIe Gen2
when the platform and firmware allow it.

This is a volatile boot-time unlock. It does not flash the VBIOS and must be
applied again after a full GPU reset or power cycle.

## Results

| Area | Observed result |
| --- | --- |
| Compute | `SS0=0x88888888`, `SS1=0x00000008`; about 50 TFLOPS FP16 on the reference card |
| PCIe | Gen2 x16 when the platform accepts the retrain |
| NVIDIA driver | GSP remains enabled; no Code 43 on the tested systems |

PCIe Gen3 and RT-core unlocks are research topics and are not advertised as
working features. This repository targets CMP 40HX only.

## ReBAR / 8 GiB BAR1

The forked EFI can optionally activate the CMP 40HX's standard PCIe
Resizable BAR control for the full 8 GiB framebuffer. It opens the TU106 XVE
gate, selects ReBAR selector `7`, sizes BAR1, preserves the existing BAR3
window, and programs the upstream bridge's 64-bit prefetchable window before
reenabling decode. A successful run is reported by `40HXCheck.exe` and by
GPU-Z as an 8192 MiB Resizable BAR.

The EFI no longer assumes a fixed address such as `0x800000000`. It first
parses the upstream root bridge's ACPI resource descriptors, prefers an
8-GiB-aligned span immediately after the current bridge window, and otherwise
uses the top of the reported 64-bit MMIO aperture. Some desktop firmware does
not implement a useful `RootBridgeIo->Configuration()` resource list; on
those systems it uses a bridge-relative fallback and still requires BAR
readback plus VRAM aperture verification. If any check fails, all GPU and
bridge registers are restored and the machine continues with stock BAR size.

This is deliberately best-effort rather than a promise of universal PCI
resource allocation. Enable **Above 4G Decoding**, keep the motherboard's
Resizable BAR support enabled, and test the actual topology. An 8 GiB BAR may
be rejected when the root complex has no sufficiently large 64-bit MMIO
aperture or when another bridge already occupies the candidate range.

## Package Contents

The ready-to-use package is in [`windows-v3.0/release`](windows-v3.0/release/):

- `40HXInstaller.exe` - GUI and command-line installer.
- `40HXUninstaller.exe` - component-level cleanup and restore.
- `40HXCheck.exe` - read-mostly status and diagnostic tool.
- `files/40HXUNLK.EFI` - the UEFI unlock application.
- `make_usb_efi.bat` - copies the EFI application to a FAT32 recovery USB.

The source EFI application is
[`windows-v3.0/tools/unlock40x`](windows-v3.0/tools/unlock40x). Fork-specific
changes and research boundaries are documented in
[`FORK_CHANGES.md`](FORK_CHANGES.md).

## Before Installing

Use a UEFI/GPT installation and keep a Windows installer USB or another boot
entry available for recovery. In firmware setup:

1. Enable **Above 4G Decoding**.
2. Disable **Secure Boot** and **CSM**.
3. Disable **Fast Boot** while testing.
4. Prefer the CPU-connected PCIe x16 slot for the CMP 40HX.
5. Leave Resizable BAR on Auto or Enabled when available.

The installer checks the boot mode and reports when an MBR/Legacy conversion
is required. Do not flash the VBIOS as part of this project.

## Installation

Run `40HXInstaller.exe` as Administrator. The GUI scans the current state and
preselects only missing components. A complete installation normally enables
GSP, installs the EFI entry, deploys the Gen2 helper, registers the login
task, and disables Windows Fast Startup and PCIe ASPM.

After installation, reboot. The EFI screen should appear briefly before
Windows starts. After login, the Gen2 task performs the link operation and
normally removes its helper driver when finished.

The installer UI and log support three languages:

```text
40HXInstaller.exe -lang en   # English (default)
40HXInstaller.exe -lang ru   # Russian
40HXInstaller.exe -lang zh   # Chinese
```

`CMP40HX_LANG=en|ru|zh` can be used instead of the command-line option. The
selection applies to the GUI and translated legacy log messages.

## Command Line

```text
40HXInstaller.exe              # open the management GUI
40HXInstaller.exe -task        # register the Gen2 login task
40HXInstaller.exe -gen2        # run Gen2 once now
40HXInstaller.exe -gen2 -hard  # allow the Link Disable fallback
40HXInstaller.exe -status      # print the current status
40HXInstaller.exe -uninstall   # uninstall the components
```

The default Gen2 strategy is use-and-remove. The optional retry and resident
strategies are configurable in the GUI or through `HKLM\SOFTWARE\40HXUnlock`:

| Value | Meaning |
| --- | --- |
| `DriverStrategy=0` | Load the helper only for the operation, then remove it. |
| `DriverStrategy=1` | Retry failed Gen2 bring-up according to the retry settings. |
| `DriverStrategy=2` | Keep the helper and let the login task monitor the link. |
| `Gen2AutoHard=0` | Disable the Link Disable fallback. |
| `Gen2RetryCount` | Number of automatic retries. |
| `Gen2RetryIntervalMin` | Delay between retries in minutes. |

## Verify the Unlock

After Windows login, run `40HXCheck.exe` as Administrator. A healthy result
shows:

- compute selectors `SS0=0x88888888` and `SS1=0x00000008`;
- a Gen2 target/link result when the platform completed retraining;
- GSP and ReBAR status without Code 43.

The diagnostic bundle is collected under
`%LOCALAPPDATA%\40HXUnlock\logs\`. The EFI application writes
`40hx_log.txt` to the ESP. Send those files when reporting a hardware-specific
failure.

An idle GPU can temporarily report Gen1 because of link power management. The
target link speed and a sustained-load measurement are more useful than one
idle snapshot.

## Recovery

If the firmware entry hangs, boot a Windows installer or recovery USB, mount
the EFI System Partition, and remove `EFI\40HX`. Rebuild the Windows boot
entry with the normal Microsoft recovery tools. The release package also
contains `EFI应急修复指南.md` with a step-by-step recovery procedure and
`make_usb_efi.bat` for a one-time USB launch.

## Limine (Experimental)

The normal EFI build chainloads Windows directly so firmware does not perform a
second POST and clear the volatile unlock. An experimental build can return to
the parent EFI boot manager, which is useful for a Limine menu:

```bash
cd windows-v3.0/tools/unlock40x
NO_AUTO_CHAINLOAD=1 \
  EFI_OUT=unlock40x_limine.efi \
  OBJ=unlock40x_limine.o \
  OUT=unlock40x_limine.so \
  ./build_v70.sh
```

Test this mode on the target board before replacing the normal release EFI.
If the boot manager performs a reset after the application returns, the unlock
state is lost.

## Build From Source

The Go tools require Go 1.26 or newer. From `windows-v3.0`:

```bat
cd tools\inst40hx
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXInstaller.exe .
cd ..\uninstall40x
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXUninstaller.exe .
cd ..\check40x
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXCheck.exe .
```

For a packaged fork with a hash manifest, run
`windows-v3.0/tools/build_release.bat`. The EFI build uses gnu-efi and the
binary blobs present in `windows-v3.0/tools/unlock40x`; see `build_v70.sh`.

## Scope and Safety

- This project is for CMP 40HX hardware and the tested TU106 firmware path.
- The unlock is not permanent and can be cleared by POST, FLR, driver reset,
  or a power cycle.
- The Gen2 helper uses signed low-level drivers. They are loaded on demand by
  default; keep Secure Boot/HVCI and the vulnerable-driver blocklist policy in
  mind for your system.
- Do not use the installer on another GPU family without a separate port and
  hardware validation.

Use the code for hardware research and comply with local law, vendor terms,
and the license files included in this repository.
