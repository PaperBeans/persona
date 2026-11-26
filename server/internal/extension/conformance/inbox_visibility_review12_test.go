package conformance_test

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxVisibilityReview12CanonicalCapabilityAndSDK(t *testing.T) {
	schemas := loadSchemas(t)
	definition := schemas.def(t, "ConnectorCapability")
	if !slices.Contains(asSlice(definition["enum"]), any(string(connector.CapInboxVisibility))) {
		t.Errorf("canonical ConnectorCapability enum %v omits %q", definition["enum"], connector.CapInboxVisibility)
	}
	if !slices.Contains(wire.ConnectorCapabilities, string(connector.CapInboxVisibility)) {
		t.Errorf("generated Go ConnectorCapabilities %v omits %q", wire.ConnectorCapabilities, connector.CapInboxVisibility)
	}
}

func TestInboxVisibilityReview12TypeScriptAndBundledSDKCapabilities(t *testing.T) {
	assertCapability := func(t *testing.T, artifact string, raw []byte) {
		t.Helper()
		declaration := ""
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "export type ConnectorCapability =") {
				declaration = line
				break
			}
		}
		if declaration == "" {
			t.Fatalf("%s has no TypeScript ConnectorCapability declaration", artifact)
		}
		var values []string
		quoted := strings.Split(declaration, "\"")
		for i := 1; i < len(quoted); i += 2 {
			values = append(values, quoted[i])
		}
		if !slices.Contains(values, string(connector.CapInboxVisibility)) {
			t.Errorf("%s ConnectorCapability declaration %q omits %q", artifact, declaration, connector.CapInboxVisibility)
		}
	}

	t.Run("TypeScript source", func(t *testing.T) {
		raw, err := os.ReadFile("../../../../sdk/ts/src/wire.ts")
		if err != nil {
			t.Fatal(err)
		}
		assertCapability(t, "sdk/ts/src/wire.ts", raw)
	})
	t.Run("Go SDK bundle", func(t *testing.T) {
		f, err := os.Open("../../../../sdk/go/bundle/sdk.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				t.Fatal("sdk/go/bundle/sdk.tar.gz has no ts/src/wire.ts entry")
			}
			if err != nil {
				t.Fatal(err)
			}
			if hdr.Name == "ts/src/wire.ts" {
				raw, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				assertCapability(t, "sdk/go/bundle/sdk.tar.gz:ts/src/wire.ts", raw)
				return
			}
		}
	})
}
