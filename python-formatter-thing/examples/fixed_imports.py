"""Example file after running `python -m python_formatter_thing fix` with the example rules."""

import sys

import os
from pathlib import Path


def ok():
    try:
        import json  # allowed: inside try
    except Exception:
        import time  # allowed: inside try/except
    return json, time


def not_ok():
    return os, Path


