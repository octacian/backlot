package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPathValueWireShape(t *testing.T) {
	for _, constructor := range []string{"url_path", "file_path"} {
		for _, tc := range []struct {
			payload string
			valid   bool
		}{
			{`{"base":{"literal":"base"},"parts":[]}`, true},
			{`{"base":{"literal":"base"},"parts":[{"literal":"a"},{"literal":"b"}]}`, true},
			{`{"base":{"literal":"base"}}`, false},
			{`{"parts":[]}`, false},
			{`{"base":null,"parts":[]}`, false},
			{`{"base":{"literal":"base"},"parts":null}`, false},
			{`{"base":{"literal":"base"},"parts":{}}`, false},
			{`{"base":{"literal":"base"},"parts":[],"path":{"literal":"old"}}`, false},
		} {
			source := `{"` + constructor + `":` + tc.payload + `}`
			var arg Argument
			err := json.Unmarshal([]byte(source), &arg)
			if (err == nil) != tc.valid {
				t.Fatalf("%s: %v", source, err)
			}
			if err != nil {
				continue
			}
			data, err := json.Marshal(arg)
			if err != nil || !strings.Contains(string(data), `"parts":[`) {
				t.Fatal("lost explicit parts", string(data), err)
			}
			var again Argument
			if err := json.Unmarshal(data, &again); err != nil {
				t.Fatal("round trip", err)
			}
			if len(arg.Operands()) != len(again.Operands()) {
				t.Fatal("lost operands")
			}
		}
	}
}
