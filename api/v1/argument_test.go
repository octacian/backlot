package v1

import (
	"encoding/json"
	"testing"
)

func TestArgumentHistoricalEncoding(t *testing.T) {
	for _, source := range []string{`"a b %s $HOME"`, `{"literal":"a b %s $HOME"}`, `{"format":{"template":"%s","values":[{"literal":"x"}]}}`} {
		var a Argument
		if err := json.Unmarshal([]byte(source), &a); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(a)
		if err != nil || string(encoded) != source {
			t.Fatalf("changed encoding: %s %v", encoded, err)
		}
	}
	for _, source := range []string{`null`, `23`, `{"literal":"private-canary","unknown":true}`, `{"format":{"template":"%s","values":[{"literal":"private-canary","unknown":true}]}}`} {
		var a Argument
		if err := json.Unmarshal([]byte(source), &a); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
}

func TestArgumentMutationPreservesSensitivity(t *testing.T) {
	argument := StringArguments("private-canary")[0]
	argument.Secret = true
	data, err := json.Marshal(argument)
	if err != nil || string(data) != `{"literal":"private-canary","secret":true}` {
		t.Fatalf("sensitivity lost: %s %v", data, err)
	}
}
