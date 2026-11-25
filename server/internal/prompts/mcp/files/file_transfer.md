Copy a file or a folder between this computer and another of the user's computers through Persona, in one call. Use it whenever a file you need is on another of the user's computers, or a file you made is needed on one: do not use ssh, scp, rsync or a chat upload for that, since the computers often cannot reach each other directly. Not for this computer itself: use your own file tools there. `send` copies `path` on this computer to `machine`; `fetch` copies `path` on `machine` to this computer; `list` shows the computers you can use and whether they are online, and copies nothing. Paths are absolute or start with `~/` (the home of the computer they are on). Without `dest` the copy lands in the receiving computer's receive folder under its own name, numbered when the name is taken. With `dest`, an existing folder takes it under its own name and any other path becomes its new name, with missing folders made on the way. Only the receiving computer's home is writable, and an existing name is refused unless `overwrite` is true (a folder is then merged, its files of the same name replaced). A folder is packed, sent and unpacked; links inside it are not carried. Credentials are never sent: SSH and other private keys, Persona's own credential files, sign-in files, `.env` files and the like are refused by name, and left out of a folder (the answer counts them). At most 2 GB, a folder at most 20,000 files. Both computers must be online. The call returns once the copy is complete and its SHA-256 is checked on the receiving computer, with where it is.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["direction"],
  "properties": {
    "direction": {
      "type": "string",
      "enum": ["send", "fetch", "list"],
      "description": "send: from this computer to machine; fetch: from machine to this computer; list: the computers you can use (copies nothing)"
    },
    "machine": {
      "type": "string",
      "description": "the other computer's id or name, as list shows it"
    },
    "path": {
      "type": "string",
      "description": "the file or folder to copy, on this computer for send or on machine for fetch: absolute or ~/…"
    },
    "dest": {
      "type": "string",
      "description": "where it goes on the receiving computer: absolute or ~/…; an existing folder takes it under its own name. Leave it out for the receive folder"
    },
    "overwrite": {
      "type": "boolean",
      "description": "replace what is already at dest (a folder is merged into); off by default, when a taken name is refused"
    }
  }
}
```
