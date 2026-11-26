"""Machine load fixtures that belong to a mock domain, not mock_hub.py's frozen switches.

MOCK_LOAD_QUEUE=<id>[,<id>...] makes the named computers queue on some cores: about 1.4 per core of load while CPU
stays near 40%, so the load-per-core display can be checked against the moderate CPU number (#12519).
"""
import os


LOAD_QUEUE = set(filter(None, os.environ.get("MOCK_LOAD_QUEUE", "").split(",")))


def register(d):
    d.help("MOCK_LOAD_QUEUE=<id>[,<id>...]  queue on some cores while CPU stays near 40% (#12519)")
    # The metric generator in machine_alerts reads LOAD_QUEUE at sample time; this domain owns the switch and its help.
    # No route is needed: the existing machine inventory and metrics carry the fixture.
