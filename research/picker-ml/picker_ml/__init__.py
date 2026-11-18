"""Offline study of a learned quality model for the worker picker."""

import os

# One thread: the study must stay a modest single-core job on shared machines.
for _v in ("OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS"):
    os.environ.setdefault(_v, "1")
