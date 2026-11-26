"""The wire type generator behind scripts/gen/extension_schema.py, one module per language.

model reads the schemas into definitions; go, ts, arkts, swift and kotlin each turn definitions into one language's
source. scripts/gen/extension_schema.py stays the command and decides which schema goes to which file. Standard library
only.
"""
