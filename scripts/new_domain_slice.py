#!/usr/bin/env python3
# Compatibility entry point; implementation lives under scripts/gen/.
import os
import runpy

runpy.run_path(os.path.join(os.path.dirname(__file__), "gen", "new_domain_slice.py"), run_name="__main__")
