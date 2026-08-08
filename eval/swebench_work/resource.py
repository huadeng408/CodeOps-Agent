"""Dummy resource module for Windows.

The swebench package uses resource.setrlimit(resource.RLIMIT_NOFILE, ...)
before launching Docker containers.  On Windows the resource module does
not exist; this stub provides a no-op implementation so swebench can be
imported and run on Windows.
"""

RLIMIT_NOFILE = 7
RLIM_INFINITY = -1


def getrlimit(resource):
    return (1024, 1024)


def setrlimit(resource, limits):
    pass  # no-op on Windows


def getpagesize():
    return 4096
