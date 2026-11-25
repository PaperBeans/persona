package badcore // core-test-package

import "testing"

func TestWhiteBox(t *testing.T) {
	if (Words{words: "x"}).words != "x" {
		t.Fatal("a white-box test reads the field")
	}
}
