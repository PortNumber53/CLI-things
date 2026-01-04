"""Example file with a rule violation for PYFMT001."""

import sys


def ok():
    try:
        import json  # allowed: inside try
    except Exception:
        import time  # allowed: inside try/except
    return json, time


def not_ok():
    import os
    from pathlib import Path
    return os, Path


