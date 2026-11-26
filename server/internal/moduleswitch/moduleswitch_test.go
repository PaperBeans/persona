package moduleswitch

import (
	"slices"
	"testing"
)

func TestSet(t *testing.T) {
	var zero Set
	if !zero.On("github") {
		t.Fatal("the zero Set switches a module off")
	}
	s := Off("mail", " github ", "", "mail")
	if s.On("github") || s.On("mail") || !s.On("imessage") {
		t.Fatalf("Off(mail, github): On = %v %v %v", s.On("github"), s.On("mail"), s.On("imessage"))
	}
	if got := s.IDs(); !slices.Equal(got, []string{"github", "mail"}) {
		t.Fatalf("IDs = %v", got)
	}
}

func TestFromFeatures(t *testing.T) {
	known := []string{"imessage", "screen-share"}
	if s := FromFeatures([]string{"module.imessage"}, known); !s.On("imessage") || !s.On("screen-share") {
		t.Fatal("a hub without the modules marker switched a module off")
	}
	s := FromFeatures([]string{Marker, "module.imessage"}, known)
	if !s.On("imessage") || s.On("screen-share") {
		t.Fatalf("FromFeatures = %v", s.IDs())
	}
	if s := FromFeatures([]string{Marker}, known); s.On("imessage") || s.On("screen-share") {
		t.Fatalf("every flag gone: %v", s.IDs())
	}
}
