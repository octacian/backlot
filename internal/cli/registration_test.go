package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestChooseRegistrationPath(t *testing.T) {
	for _, choice := range []struct{ input, want string }{{"1\n", "/copy"}, {"2\n", "/original"}, {"0\n", ""}} {
		var out bytes.Buffer
		path, err := chooseRegistrationPath(strings.NewReader(choice.input), &out, "/copy", "/original")
		if choice.want == "" {
			if err == nil {
				t.Fatal("invalid choice accepted")
			}
		} else if err != nil || path != choice.want {
			t.Fatal(path, err)
		}
	}
}
