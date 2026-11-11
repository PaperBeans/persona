// Package passwordpake pins the OPAQUE suite shared by the hub and its browser WASM client.
// The protocol itself is implemented by github.com/bytemare/opaque (RFC 9807).
package passwordpake

import "github.com/bytemare/opaque"

const Suite = "opaque-ristretto255-sha512-argon2id-v1"

var ClientIdentity = []byte("persona-client")

func ServerIdentity(hub string) string { return "persona-hub:" + hub }

func Configuration() *opaque.Configuration {
	c := opaque.DefaultConfiguration()
	c.Context = []byte("persona/client-password-login/v1")
	return c
}

func NewServer(identity string) (*opaque.Server, []byte, error) {
	c := Configuration()
	sk, pk := c.KeyGen()
	k := &opaque.ServerKeyMaterial{Identity: []byte(ServerIdentity(identity)), PrivateKey: sk,
		PublicKeyBytes: pk.Encode(), OPRFGlobalSeed: c.GenerateOPRFSeed()}
	s, err := c.Server()
	if err != nil {
		return nil, nil, err
	}
	if err = s.SetKeyMaterial(k); err != nil {
		return nil, nil, err
	}
	return s, k.Encode(), nil
}

func Server(keys []byte) (*opaque.Server, error) {
	c := Configuration()
	k, err := c.DecodeServerKeyMaterial(keys)
	if err != nil {
		return nil, err
	}
	s, err := c.Server()
	if err != nil {
		return nil, err
	}
	if err = s.SetKeyMaterial(k); err != nil {
		return nil, err
	}
	return s, nil
}
