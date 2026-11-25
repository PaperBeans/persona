package builtinext

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/moduleswitch"
)

func TestProgramsAndVolc(t *testing.T) {
	savedVolc, savedVoice := volc, voice
	volc, voice = moduleswitch.NewPort[Volc]("builtinext.test"), moduleswitch.NewPort[Voice]("builtinext.test")
	t.Cleanup(func() { programs, volc, voice = map[string]program{}, savedVolc, savedVoice })
	if _, ok := LookupProgram("x"); ok {
		t.Fatal("an unknown program was found")
	}
	if _, ok := VolcSpeech(moduleswitch.Set{}); ok {
		t.Fatal("the Volcengine extension is there without its module")
	}
	if ProgramUsage("personad") != "" {
		t.Fatal("usage lines without a program")
	}
	if ProgramOff(moduleswitch.Off("xm"), "x") {
		t.Fatal("a program no module provides is off")
	}
	AddProgram("xm", "x", "the x extension", func(context.Context, string) error { return nil })
	if ProgramOff(moduleswitch.Set{}, "x") || !ProgramOff(moduleswitch.Off("xm"), "x") || ProgramOff(moduleswitch.Off("xm"), "personad-core") {
		t.Fatal("ProgramOff does not follow the program's module")
	}
	if got, want := ProgramUsage("personad"), "  personad x                the x extension\n"; got != want {
		t.Fatalf("usage %q, want %q", got, want)
	}
	if run, ok := LookupProgram("x"); !ok || run(context.Background(), "") != nil {
		t.Fatal("the program provided is not found")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a program provided twice did not panic")
			}
		}()
		AddProgram("xm", "x", "", nil)
	}()
	ProvideVolc("volc", Volc{ExtensionID: "volc", Voices: []string{"a"}})
	if v, ok := VolcSpeech(moduleswitch.Set{}); !ok || v.ExtensionID != "volc" || v.Voices[0] != "a" {
		t.Fatalf("%+v %v", v, ok)
	}
	if _, ok := VolcSpeech(moduleswitch.Off("volc")); ok {
		t.Fatal("the Volcengine extension is there with its module switched off")
	}
	if _, ok := VoiceExtension(moduleswitch.Set{}); ok {
		t.Fatal("the voice extension is there without its module")
	}
	ProvideVoice("voice", Voice{TranscriptionContribution: "gpt-live"})
	if v, ok := VoiceExtension(moduleswitch.Set{}); !ok || v.TranscriptionContribution != "gpt-live" {
		t.Fatalf("%+v %v", v, ok)
	}
	if _, ok := VoiceExtension(moduleswitch.Off("voice")); ok {
		t.Fatal("the voice extension is there with its module switched off")
	}
}
