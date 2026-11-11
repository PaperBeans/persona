{{.Description}}

For a secret (an API key, a token, a password, a private key), pass file instead of text: the absolute path of a text file on the computer you run on. Persona reads the file and sends its text straight to the phone, so the secret never passes through you. Do not read the file yourself, never put a secret in text, and do not read it back with phone_clipboard_get. The result gives only its size and, when it is at least {{.TailMin}} characters long, its last 4 characters. The file must be your own, inside your home directory, and UTF-8 text of at most {{.MaxKiB}} KiB.
