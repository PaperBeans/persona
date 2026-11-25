package identity

import (
	"path"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// What an Agent's file transfer between computers never reads (docs/protocol/file-transfer.md "Agents and the
// assistant"), on top of the node's path guard (node/pathguard.go). The guard refuses credentials where they are kept
// (~/.ssh, the data folder's hub.json, the Agents' own sign-ins); a transfer an Agent starts also refuses them by
// name wherever they are: a copy of hub.json in a project, a .pem beside the code, a project's .env. A named file
// like that is refused, and inside a folder it is left out and counted.

// transferSecretNames are credential files by their whole name (folded), wherever they are.
var transferSecretNames = []string{
	".netrc", ".git-credentials", ".npmrc", ".pypirc", ".pgpass", ".htpasswd", ".vault-token",
	"credentials", "credentials.json", "credentials.toml", "credentials.db", "access_tokens.db",
	"secring.gpg", "trustdb.gpg",
	".credentials.json", "auth.json", ".claude.json", ".claude-custom-oauth.json",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "id_ecdsa_sk", "id_ed25519_sk",
}

// transferSecretExts are credential files by their extension (folded): private keys, key stores, password vaults.
var transferSecretExts = []string{".p12", ".pfx", ".jks", ".keystore", ".ppk", ".kdbx", ".keychain", ".keychain-db"}

// transferCredentialEnd is the data folder's credential endings (DataCredentialPattern) without .crt, a certificate
// being public: a private key (.key, .pem, the APNs .p8), a token, a secret or a password file, backups included.
var transferCredentialEnd = lazyre.New(`\.(key|p8|pem|token|secret|password)([.~-][^/]*)?$`)

// personaSecret says whether n (folded) is one of Persona's own credential files (DataSecrets) by its whole name, or
// a backup of one beside it (hub.json.bak, hub.json~): not by its start alone, which elsewhere than the data folder
// would take a project's apns_client.go for the APNs key.
func personaSecret(n string) bool {
	for _, s := range DataSecrets {
		if s == "apns" {
			continue // the APNs key is a .p8 (transferCredentialEnd)
		}
		if n == s || strings.HasPrefix(n, s) && strings.ContainsAny(n[len(s):len(s)+1], ".~-") {
			return true
		}
	}
	return false
}

// transferSecretDirs are folders refused whole, by their name, wherever they are.
var transferSecretDirs = []string{".ssh", ".gnupg", ".aws", ".azure", ".kube", ".password-store", "keychains"}

// envSamples are the .env files that hold no values by convention, which a project does share.
var envSamples = []string{".env.example", ".env.sample", ".env.template", ".env.dist", ".env.defaults"}

// TransferSecretName says whether a file named name (a base name) is a credential an Agent's transfer must not
// read: one of Persona's own credential files (hub.json and the rest of DataSecrets), a private key or key store, a
// sign-in file, or a .env file other than a sample.
func TransferSecretName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if personaSecret(n) || transferCredentialEnd.MatchString(n) || slices.Contains(transferSecretNames, n) {
		return true
	}
	if n == ".env" || strings.HasPrefix(n, ".env.") && !slices.Contains(envSamples, n) {
		return true
	}
	return slices.Contains(transferSecretExts, path.Ext(n))
}

// TransferSecretDir says whether a folder named name (a base name) is refused whole by an Agent's transfer.
func TransferSecretDir(name string) bool {
	return slices.Contains(transferSecretDirs, strings.ToLower(strings.TrimSpace(name)))
}

// TransferSecretPath says whether any part of p (slash-separated, as a zip names its entries, or a path with the
// system's separators already turned into slashes) is a credential an Agent's transfer must not carry: a folder
// TransferSecretDir refuses on the way, or a last part TransferSecretName refuses.
func TransferSecretPath(p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, part := range parts {
		if i < len(parts)-1 && TransferSecretDir(part) {
			return true
		}
	}
	last := parts[len(parts)-1]
	return TransferSecretDir(last) || TransferSecretName(last)
}
