package passwordpake

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bytemare/opaque"
)

// Build with npm run pake:build, then run PERSONA_TEST_PASSWORD_WASM=1 go test ./internal/passwordpake.
func TestBrowserWasmInterop(t *testing.T) {
	if os.Getenv("PERSONA_TEST_PASSWORD_WASM") != "1" {
		t.Skip("browser WASM interoperability is opt-in")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "web/scripts/pake-client-test.mjs"), filepath.Join(root, "web/public/pake"))
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(out)
	call := func(op, password string, message []byte) []byte {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"op": op, "password": password, "message": base64.StdEncoding.EncodeToString(message), "identity": ServerIdentity("interop-hub")})
		if _, err := in.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			t.Fatal("WASM did not respond", scanner.Err())
		}
		var r struct {
			Message []byte
			Error   string
		}
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("%s: %v", scanner.Text(), err)
		}
		if r.Error != "" {
			t.Fatal(r.Error)
		}
		return r.Message
	}
	s, keys, err := NewServer("interop-hub")
	if err != nil {
		t.Fatal(err)
	}
	credential := opaque.RandomBytes(64)
	request, err := s.Deserialize.RegistrationRequest(call("register-start", "browser-only-password", nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := s.RegistrationResponse(request, credential, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.Deserialize.RegistrationRecord(call("register-finish", "", response.Serialize()))
	if err != nil {
		t.Fatal(err)
	}
	// Restore the persisted server keys before logging in, just as after a hub restart.
	s, err = Server(keys)
	if err != nil {
		t.Fatal(err)
	}
	ke1, err := s.Deserialize.KE1(call("login-start", "browser-only-password", nil))
	if err != nil {
		t.Fatal(err)
	}
	ke2, output, err := s.GenerateKE2(ke1, &opaque.ClientRecord{CredentialIdentifier: credential, ClientIdentity: ClientIdentity, RegistrationRecord: record})
	if err != nil {
		t.Fatal(err)
	}
	ke3, err := s.Deserialize.KE3(call("login-finish", "", ke2.Serialize()))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LoginFinish(ke3, output.ClientMAC); err != nil {
		t.Fatal(err)
	}
}
