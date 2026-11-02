"""Owned Windows Job / POSIX process-group lifecycle shared by engineering and eval."""
from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from typing import Any


class OwnedProcessTree:
    def __init__(self, process: subprocess.Popen[str]) -> None:
        self.process = process
        self.job: Any = None
        if os.name != "nt":
            return
        import ctypes
        from ctypes import wintypes

        class BasicLimits(ctypes.Structure):
            _fields_ = [("process_time", ctypes.c_longlong), ("job_time", ctypes.c_longlong), ("flags", wintypes.DWORD), ("min_working_set", ctypes.c_size_t), ("max_working_set", ctypes.c_size_t), ("active_limit", wintypes.DWORD), ("affinity", ctypes.c_size_t), ("priority", wintypes.DWORD), ("scheduling", wintypes.DWORD)]

        class ExtendedLimits(ctypes.Structure):
            _fields_ = [("basic", BasicLimits), ("io", ctypes.c_uint64 * 6), ("memory", ctypes.c_size_t * 4)]

        class Accounting(ctypes.Structure):
            _fields_ = [("times", ctypes.c_longlong * 4), ("counts", wintypes.DWORD * 4)]

        self.ctypes, self.accounting_type = ctypes, Accounting
        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        signatures = {
            "CreateJobObjectW": ([ctypes.c_void_p, wintypes.LPCWSTR], wintypes.HANDLE),
            "SetInformationJobObject": ([wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD], wintypes.BOOL),
            "AssignProcessToJobObject": ([wintypes.HANDLE, wintypes.HANDLE], wintypes.BOOL),
            "QueryInformationJobObject": ([wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.c_void_p], wintypes.BOOL),
            "TerminateJobObject": ([wintypes.HANDLE, wintypes.UINT], wintypes.BOOL),
            "CloseHandle": ([wintypes.HANDLE], wintypes.BOOL),
        }
        for name, (arguments, result) in signatures.items():
            function = getattr(self.kernel, name)
            function.argtypes, function.restype = arguments, result
        self.job = self.kernel.CreateJobObjectW(None, None)
        if not self.job:
            raise ctypes.WinError(ctypes.get_last_error())
        limits = ExtendedLimits()
        limits.basic.flags = 0x2000  # KILL_ON_JOB_CLOSE; no child may break away.
        if not self.kernel.SetInformationJobObject(self.job, 9, ctypes.byref(limits), ctypes.sizeof(limits)) or not self.kernel.AssignProcessToJobObject(self.job, int(getattr(process, "_handle"))):
            error = ctypes.WinError(ctypes.get_last_error())
            self.kernel.CloseHandle(self.job)
            self.job = None
            raise error

    def _active(self) -> int:
        accounting = self.accounting_type()
        if not self.kernel.QueryInformationJobObject(self.job, 1, self.ctypes.byref(accounting), self.ctypes.sizeof(accounting), None):
            raise self.ctypes.WinError(self.ctypes.get_last_error())
        return int(accounting.counts[2])

    def stop(self) -> None:
        if self.job is not None:
            if not self.kernel.TerminateJobObject(self.job, 1):
                raise self.ctypes.WinError(self.ctypes.get_last_error())
            deadline = time.monotonic() + 10
            while self._active():
                if time.monotonic() > deadline:
                    raise TimeoutError("owned process tree did not terminate")
                time.sleep(0.01)
        elif sys.platform != "win32":
            try:
                os.killpg(self.process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        self.process.wait(timeout=10)

    def close(self) -> None:
        if self.job is not None:
            try:
                if self._active():
                    self.stop()
            finally:
                self.kernel.CloseHandle(self.job)
                self.job = None
        elif sys.platform != "win32":
            self.stop()
