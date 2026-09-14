# CMP 40HX Windows Unlock

This directory contains the Windows source tree for the CMP 40HX unlock. The
project targets NVIDIA TU106 CMP 40HX (`10de:1f0b`) and provides:

- compute/Tensor unlock through the UEFI application;
- optional PCIe Gen2 bring-up after the NVIDIA driver loads;
- GUI, command-line, check, and uninstall tools;
- English, Russian, and Chinese installer messages (`-lang en|ru|zh`).

For the complete user guide, BIOS requirements, recovery procedure, Limine
experiment, and current limitations, see the repository
[README](../README.md).

## Source Layout

| Path | Purpose |
| --- | --- |
| `tools/inst40hx` | Installer GUI and command-line entry point |
| `tools/check40x` | Status and diagnostic utility |
| `tools/uninstall40x` | Component-level uninstaller |
| `tools/40hxcore` | Shared Windows operations and Gen2 logic |
| `tools/unlock40x` | EFI source, embedded firmware blobs, and build script |
| `release` | Ready-to-use release package |

## Build the Go Tools

Use Go 1.26 or newer on Windows:

```bat
cd tools\inst40hx
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXInstaller.exe .
cd ..\uninstall40x
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXUninstaller.exe .
cd ..\check40x
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXCheck.exe .
```

Run `tools/build_release.bat` to create a fork package with a SHA-256
manifest. The source verification helper is `tools/verify_source.ps1`.

## EFI Build

`tools/unlock40x/build_v70.sh` requires gcc, binutils, gnu-efi headers and
libraries, and the binary blobs in the same directory. The normal build
chainloads Windows after the unlock. `NO_AUTO_CHAINLOAD=1` builds the
experimental variant that returns to a parent EFI boot manager such as Limine.

The production path is CMP 40HX/TU106. Legacy GA102 helpers remain in the
source only as historical research code and are not a claim of support for
another CMP model. PCIe Gen3 and RT-core unlocks remain unverified research.
