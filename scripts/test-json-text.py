#!/usr/bin/env python3
"""Print go test -json output as the plain text go test would have printed.

Usage: scripts/test-json-text.py FILE
Lines that are not JSON (build noise) are printed as they are.
"""
import json
import sys

with open(sys.argv[1]) as source:
    for line in source:
        try:
            event = json.loads(line)
        except ValueError:
            sys.stdout.write(line)
            continue
        sys.stdout.write(event.get("Output", ""))
