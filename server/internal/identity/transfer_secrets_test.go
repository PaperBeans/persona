package identity

import "testing"

// An Agent's transfer refuses credentials by their name anywhere: Persona's own credential files and their backups,
// private keys and key stores, sign-in files, .env files other than the samples; a project's ordinary files pass,
// also ones whose name only starts like a credential's.
func TestTransferSecretName(t *testing.T) {
	for _, name := range []string{"hub.json", "HUB.JSON", "hub.json.bak", "hub.json~", "link.json", "relay.secret",
		"server.key", "tls.pem", "AuthKey_ABC.p8", "api.token", "db.password", "store.p12", "app.keystore", "vault.kdbx",
		"id_rsa", "id_ed25519", ".netrc", ".git-credentials", ".npmrc", "credentials", "credentials.json", ".env",
		".env.local", ".env.production", "login.keychain-db"} {
		if !TransferSecretName(name) {
			t.Errorf("%s passes", name)
		}
	}
	for _, name := range []string{"report.pdf", "main.go", "apns_client.go", "hubs.json", "hub.jsonl", "server.crt",
		"keyboard.txt", ".env.example", ".env.sample", ".env.template", "environment.md", "id_rsa.pub", "README", ""} {
		if TransferSecretName(name) {
			t.Errorf("%s is taken for a credential", name)
		}
	}
}

// A folder of credentials is refused whole wherever it is, and so is anything inside it.
func TestTransferSecretPath(t *testing.T) {
	for _, p := range []string{"proj/.ssh", "proj/.ssh/config", ".aws/credentials", "a/.gnupg/pubring.kbx", "deploy/.env",
		"/home/me/proj/keys/server.key"} {
		if !TransferSecretPath(p) {
			t.Errorf("%s passes", p)
		}
	}
	for _, p := range []string{"proj/src/main.go", "/home/me/proj", "ssh/notes.md", "proj/.envrc.md"} {
		if TransferSecretPath(p) {
			t.Errorf("%s is refused", p)
		}
	}
}
