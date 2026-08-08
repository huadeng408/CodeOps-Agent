import os, sys
print("Python:", sys.version)
try:
    import datasets
    print("datasets:", datasets.__version__)
except ImportError as e:
    print("datasets NOT INSTALLED:", e)
    sys.exit(1)
