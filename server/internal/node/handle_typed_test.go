package node

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Node.handle's methods decode their request into a struct of their own (#8089). Each case decodes one request sample
// two ways: into the struct the method has now, and into the fields as the shared parameter struct declared them before
// (the tags copied here, so a changed tag fails). The two must give the same values. The samples carry fields of other
// methods, which both ignore.
func TestTypedHandleParamsDecodeAsTheSharedStructDid(t *testing.T) {
	cases := []struct {
		name   string
		sample string
		legacy any // the old fields, with the old tags
		typed  any // the method's struct now
		same   func(legacy, typed any) bool
	}{
		{
			name:   methodFileRead,
			sample: `{"path":"a/b.txt","cwd":"/work","offset":4096,"stream":true,"session_id":"s1","tab_id":"t","limit":5}`,
			legacy: &struct {
				Path   string `json:"path"`
				Cwd    string `json:"cwd"`
				Offset int64  `json:"offset"`
				Stream bool   `json:"stream"`
			}{},
			typed: &fileReadParams{},
			same: func(l, n any) bool {
				return reflect.ValueOf(l).Elem().Convert(reflect.TypeOf(fileReadParams{})).Interface() == *n.(*fileReadParams)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(c.sample), c.legacy); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(c.sample), c.typed); err != nil {
				t.Fatal(err)
			}
			if !c.same(c.legacy, c.typed) {
				t.Fatalf("legacy %+v, typed %+v", c.legacy, c.typed)
			}
			if reflect.ValueOf(c.typed).Elem().IsZero() {
				t.Fatalf("sample decoded to nothing: %s", c.sample)
			}
		})
	}
}
