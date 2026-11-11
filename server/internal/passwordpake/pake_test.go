package passwordpake

import "testing"

func TestServerKeyRoundTrip(t *testing.T) {
	_, keys, err := NewServer("hub-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Server(keys); err != nil {
		t.Fatal(err)
	}
}
