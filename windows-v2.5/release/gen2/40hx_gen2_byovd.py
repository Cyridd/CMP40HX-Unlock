#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
40HX Gen2 Windows unlock — BYOVD edition (no test-signing, no WDAC).

Solves: with GSP on (required for compute unlock, EnableGpuFirmware=1), the GSP
firmware clears PRIV_MISC_1.GEN2_EN (bit5) + alters LINK_CONFIG_0 at devinit,
so the link stays Gen1 even though XVE_OVR=6 from the EFI unlock is preserved.
This tool re-writes the 4 PL0 registers via a physical-memory BYOVD driver
(ThrottleStop.sys, TechPowerUp EV-signed, loads in normal mode) then does an
UPGRADE-ONLY root-port retrain via WinRing0 (GlobalSign-signed) -> Gen2 x16.

Workflow per run (verified 2026-09-05 on ASUS Z170 + CMP 40HX, 616.56 driver):
  1. deploy + start ThrottleStop + WinRing0_1_2_0 (kernel services, demand)
  2. read GPU BAR0 phys base from PCI config (fallback 0xF6000000)
  3. ThrottleStop WRITE_MEM (ioctl 0x8000649C): XVE_OVR/LINK_CONFIG_0/
     PRIV_MISC_1/CYA_0  (each write read-back verified)
  4. WinRing0 PCI config: LNKCTL2 TLS=2 on GPU + root port
  5. root port retrain: CLEAR bit5 then SET bit5 (pulse!) -> Gen2
  6. verify LnkSta Gen2, retry root/GPU alternately (max 3)
  7. --selfclean: stop services + delete driver files (run-once hygiene)

Exit: 0 = Gen2 OK, 1 = still Gen1, 2 = driver/load failure.

Companion files (same folder):
  drivers/ThrottleStop.sys  - physical memory R/W  (\\.\ThrottleStop)
  drivers/WinRing0x64.sys   - PCI config space     (\\.\WinRing0_1_2_0)
"""
import ctypes, os, shutil, struct, subprocess, sys, time
from ctypes import wintypes, c_void_p

HERE = os.path.dirname(os.path.abspath(__file__))
LOG  = os.path.join(HERE, '40hx_gen2.log')
DRVDIR = os.path.join(HERE, 'drivers')

SVC_TS   = 'ThrottleStop'
SVC_WR   = 'WinRing0_1_2_0'
FILE_TS  = 'ThrottleStop.sys'
FILE_WR  = 'WinRing0x64.sys'
DEV_TS   = r'\\.\ThrottleStop'
DEV_WR   = r'\\.\WinRing0_1_2_0'

# ThrottleStop IOCTLs (CVE-2025-7771 surface)
IOCTL_TS_R = 0x80006498          # in: <Q addr>        out: <=8B data
IOCTL_TS_W = 0x8000649C          # in: <Q addr><data>  out: -

# WinRing0 IOCTLs (fn 0x851/0x852, device type 40000)
def WRCTL(fn, acc): return (40000 << 16) | (acc << 14) | (fn << 2)

BAR0_FALLBACK = 0xF6000000

# 40HX PL0 unlock registers (offsets in BAR0) — same values as win_gen2_unlock.py
PL0_WRITES = [
    (0x8872C, 0x00000006, 'XVE_OVR=6'),
    (0x8C040, 0x80085800, 'LINK_CONFIG_0 MAX_RATE=2'),
    (0x8841C, 0xE0B42D00, 'PRIV_MISC_1 GEN2_EN'),
    (0x8C2C0, 0x068731B3, 'CYA_0 DIS_G2=0'),
]


def log(msg):
    line = '[%s] %s' % (time.strftime('%H:%M:%S'), msg)
    print(line)
    try:
        with open(LOG, 'a') as f:
            f.write(line + '\n')
    except Exception:
        pass


# ---------------- service helpers ----------------
def svc_ensure_start(name, binfile):
    """Copy driver to System32\drivers if missing, create+start demand service."""
    sysdir = os.environ.get('SystemRoot', r'C:\Windows') + r'\System32\drivers'
    dst = os.path.join(sysdir, binfile)
    src = os.path.join(DRVDIR, binfile)
    if not os.path.exists(dst) and os.path.exists(src):
        try:
            shutil.copy(src, dst)
            log('  deploy %s' % binfile)
        except Exception as e:
            log('  deploy %s FAIL: %s' % (binfile, e))
    adv = ctypes.WinDLL('advapi32', use_last_error=True)
    adv.OpenSCManagerW.restype = c_void_p
    adv.OpenSCManagerW.argtypes = [wintypes.LPCWSTR, wintypes.LPCWSTR, wintypes.DWORD]
    adv.OpenServiceW.restype = c_void_p
    adv.OpenServiceW.argtypes = [c_void_p, wintypes.LPCWSTR, wintypes.DWORD]
    adv.CreateServiceW.restype = c_void_p
    adv.CreateServiceW.argtypes = [c_void_p, wintypes.LPCWSTR, wintypes.LPCWSTR,
        wintypes.DWORD, wintypes.DWORD, wintypes.DWORD, wintypes.DWORD,
        wintypes.LPCWSTR, wintypes.LPCWSTR, ctypes.POINTER(wintypes.DWORD),
        wintypes.LPCWSTR, wintypes.LPCWSTR, wintypes.LPCWSTR]
    adv.StartServiceW.argtypes = [c_void_p, wintypes.DWORD, c_void_p]
    adv.DeleteService.argtypes = [c_void_p]
    adv.CloseServiceHandle.argtypes = [c_void_p]
    scm = adv.OpenSCManagerW(None, None, 0xF003F)
    s = adv.OpenServiceW(scm, name, 0xF01FF)
    if not s:
        s = adv.CreateServiceW(scm, name, name, 0xF01FF, 1, 3, 1,
                               r'\SystemRoot\System32\drivers\\' + binfile,
                               None, None, None, None, None)
        if not s:
            log('  create service %s FAIL err=%d' % (name, ctypes.get_last_error()))
    if s:
        ok = adv.StartServiceW(s, 0, None)
        if not ok:
            err = ctypes.get_last_error()
            if err != 1056:   # 1056 = already running
                log('  start %s FAIL err=%d (577=blocklist/signature)' % (name, err))
                adv.CloseServiceHandle(s)
                adv.CloseServiceHandle(scm)
                return False
        adv.CloseServiceHandle(s)
    adv.CloseServiceHandle(scm)
    return True


def svc_stop_delete(name):
    adv = ctypes.WinDLL('advapi32', use_last_error=True)
    adv.OpenSCManagerW.restype = c_void_p
    adv.OpenSCManagerW.argtypes = [wintypes.LPCWSTR, wintypes.LPCWSTR, wintypes.DWORD]
    adv.OpenServiceW.restype = c_void_p
    adv.OpenServiceW.argtypes = [c_void_p, wintypes.LPCWSTR, wintypes.DWORD]
    adv.ControlService.argtypes = [c_void_p, wintypes.DWORD, c_void_p]
    adv.DeleteService.argtypes = [c_void_p]
    adv.CloseServiceHandle.argtypes = [c_void_p]
    class SS(ctypes.Structure):
        _fields_ = [('t', wintypes.DWORD)] * 7
    scm = adv.OpenSCManagerW(None, None, 0xF003F)
    s = adv.OpenServiceW(scm, name, 0xF01FF)
    if s:
        st = SS()
        try:
            adv.ControlService(s, 1, ctypes.byref(st))
        except Exception:
            pass
        time.sleep(0.4)
        adv.DeleteService(s)
        adv.CloseServiceHandle(s)
    adv.CloseServiceHandle(scm)


def cleanup_files():
    sysdir = os.environ.get('SystemRoot', r'C:\Windows') + r'\System32\drivers'
    for fn in (FILE_TS, FILE_WR):
        p = os.path.join(sysdir, fn)
        if os.path.exists(p):
            try:
                os.remove(p)
                log('  removed %s' % fn)
            except Exception:
                pass


# ---------------- WinRing0 PCI config ----------------
class WinRing:
    def __init__(self):
        self.k = ctypes.WinDLL('kernel32', use_last_error=True)
        self.h = self.k.CreateFileW(DEV_WR, 0xC0000000, 0, None, 3, 0x80, None)
        if self.h == c_void_p(-1).value:
            raise OSError('open %s failed' % DEV_WR)

    def rpci(self, bdf, r):
        inp = struct.pack('<II', bdf, r)
        v = wintypes.DWORD(0); n = wintypes.DWORD(0)
        if self.k.DeviceIoControl(self.h, WRCTL(0x851, 1), inp, 8,
                                  ctypes.byref(v), 4, ctypes.byref(n), None):
            return v.value
        return None

    def wpci(self, bdf, r, data):
        inp = struct.pack('<II', bdf, r) + data
        n = wintypes.DWORD(0)
        return bool(self.k.DeviceIoControl(self.h, WRCTL(0x852, 2), inp, len(inp),
                                           None, 0, ctypes.byref(n), None))


# ---------------- ThrottleStop physical memory ----------------
class TS:
    def __init__(self):
        self.k = ctypes.WinDLL('kernel32', use_last_error=True)
        self.h = self.k.CreateFileW(DEV_TS, 0xC0000000, 0, None, 3, 0x80, None)
        if self.h == c_void_p(-1).value:
            raise OSError('open %s failed (err=%d)' % (DEV_TS, ctypes.get_last_error()))

    def rd(self, addr):
        inp = struct.pack('<Q', addr)
        out = ctypes.create_string_buffer(4)
        n = wintypes.DWORD(0)
        if self.k.DeviceIoControl(self.h, IOCTL_TS_R, inp, 8, out, 4,
                                  ctypes.byref(n), None):
            return struct.unpack('<I', out.raw)[0]
        return None

    def wr(self, addr, val):
        inp = struct.pack('<QI', addr, val)
        n = wintypes.DWORD(0)
        return bool(self.k.DeviceIoControl(self.h, IOCTL_TS_W, inp, 12,
                                           None, 0, ctypes.byref(n), None))


# ---------------- PCIe helpers ----------------
def pcie_cap(w, bdf):
    hdr = w.rpci(bdf, 0x34)
    if hdr is None:
        return None
    cur = hdr & 0xFF
    for _ in range(20):
        if cur < 0x40 or cur > 0xFF:
            return None
        c = w.rpci(bdf, cur)
        if c is None:
            return None
        if (c & 0xFF) == 0x10:
            return cur
        cur = (c >> 8) & 0xFF
    return None


def link_gen(w, bdf):
    cap = pcie_cap(w, bdf)
    if cap is None:
        return None
    sts = w.rpci(bdf, cap + 0x12)
    return (sts & 0xF) if sts is not None else None


def find_root_port(w, gpu_bus):
    for d in range(0, 32):
        for f in range(0, 8):
            bdf = (d << 3) | f
            vid = w.rpci(bdf, 0)
            if vid is None or vid == 0xFFFFFFFF or (vid & 0xFFFF) == 0:
                continue
            cls = (w.rpci(bdf, 8) >> 16) & 0xFFFF
            if cls == 0x0604:                      # PCI-PCI bridge
                sec = w.rpci(bdf, 0x19)
                if sec is not None and (sec & 0xFF) == gpu_bus:
                    return bdf
    return None


def parse_gpu(s):
    p = s.split(':')
    bus = int(p[0], 16)
    dev = int(p[1].split('.')[0], 16)
    fn = int(p[1].split('.')[1], 16)
    return (bus << 8) | (dev << 3) | fn


def main():
    args = [a for a in sys.argv[1:]]
    selfclean = '--selfclean' in args
    gpu_arg = '01:00.0'
    if '--gpu' in args:
        gpu_arg = args[args.index('--gpu') + 1]

    log('=== 40HX Gen2 BYOVD unlock ===  gpu=%s' % gpu_arg)

    # 1) deploy + start services
    log('[1] start driver services')
    if not svc_ensure_start(SVC_TS, FILE_TS):
        log('FAIL: ThrottleStop driver not running (blocklisted?)')
        return 2
    if not svc_ensure_start(SVC_WR, FILE_WR):
        log('FAIL: WinRing0 driver not running')
        return 2
    time.sleep(0.5)

    gpu = parse_gpu(gpu_arg)
    try:
        w = WinRing()
        ts = TS()
    except OSError as e:
        log('FAIL: open device: %s' % e)
        return 2

    # 2) GPU BAR0 physical base
    bar = w.rpci(gpu, 0x10)
    bar0 = (bar & 0xFFFFFFF0) if bar is not None else BAR0_FALLBACK
    log('    BAR0 = 0x%08X (rc=%s)' % (bar0, 'ok' if bar is not None else 'fallback'))

    # 3) re-write 4 PL0 registers (fix GSP devinit overrides)
    log('[2] PL0 register re-write via ThrottleStop')
    ok_all = True
    for off, val, name in PL0_WRITES:
        wok = ts.wr(bar0 + off, val)
        rb = ts.rd(bar0 + off)
        st = 'OK' if (wok and rb == val) else 'MISMATCH/FAIL'
        if st != 'OK':
            ok_all = False
        log('    0x%06X %-24s -> 0x%08X rb=0x%s %s' %
            (off, name, val, '%08X' % rb if rb is not None else '????', st))
        time.sleep(0.03)
    if not ok_all:
        log('WARN: not all registers verified')

    # 4) TLS=2 on GPU + root
    root = find_root_port(w, (gpu >> 8) & 0xFF)
    log('    root port = %02x:%02x.%x' % (
        (root >> 8) & 0xFF, (root >> 3) & 0x1F, root & 7) if root else 'NONE')
    for bdf, tag in [(gpu, 'GPU'), (root, 'ROOT')]:
        if bdf is None:
            continue
        cap = pcie_cap(w, bdf)
        if not cap:
            continue
        lctl2 = w.rpci(bdf, cap + 0x30)
        if lctl2 is not None:
            w.wpci(bdf, cap + 0x30, struct.pack('<I', (lctl2 & 0xFFFFFFF0) | 2))
            log('    %s LNKCTL2 TLS=2 rb=0x%04X' % (tag, (w.rpci(bdf, cap + 0x30) or 0) & 0xFFFF))

    # 5) upgrade retrain: CLEAR then SET bit5 (pulse required!)
    log('[3] root/gpu upgrade retrain (clear->set pulse)')
    cur = link_gen(w, gpu)
    attempts = 0
    while (cur is None or cur < 2) and attempts < 4:
        bdf = root if (attempts % 2 == 0 and root) else gpu
        cap = pcie_cap(w, bdf)
        if cap:
            ctl = w.rpci(bdf, cap + 0x10)
            if ctl is not None:
                w.wpci(bdf, cap + 0x10, struct.pack('<I', ctl & ~0x20))   # clear
                time.sleep(0.3)
                ctl = w.rpci(bdf, cap + 0x10)
                w.wpci(bdf, cap + 0x10, struct.pack('<I', (ctl or 0) | 0x20))  # set
        time.sleep(2.2)
        cur = link_gen(w, gpu)
        attempts += 1
        log('    try %d: GPU LnkSta Gen%d' % (attempts, cur if cur is not None else -1))

    # 6) result
    if cur is not None and cur >= 2:
        log('*** GEN2 ACHIEVED (Gen%d) ***' % cur)
        rc = 0
    else:
        log('Still Gen%d — see log above' % (cur if cur is not None else -1))
        rc = 1

    # 7) self-clean (run-once hygiene: nothing left in kernel/image)
    if selfclean:
        log('[4] self-clean: stop services + remove driver files')
        svc_stop_delete(SVC_TS)
        svc_stop_delete(SVC_WR)
        cleanup_files()
    log('=== done rc=%d ===' % rc)
    return rc


if __name__ == '__main__':
    sys.exit(main())
