package schema

import (
	"strings"
	"testing"
)

func TestTaskSchemaParity(t *testing.T) {
	schema := compile(t, "manifest-v1.schema.json")
	base := `version: backlot/v1
project: tasks
tools: {shell: sh}
components:
  job: {kind: job, runtime: native, policy: each-start, command: {tool: shell}}
scenes:
  dev: {lifetime: persistent, components: [job], tasks: [job]}
`
	for _, test := range []struct {
		old, new string
		valid    bool
	}{{"", "", true}, {"tasks: [job]", "tasks: [job, job]", false}, {"tasks: [job]", "tasks: [Bad]", false}, {"lifetime: persistent", "lifetime: disposable, terminal_job: job", false}} {
		data := []byte(strings.Replace(base, test.old, test.new, 1))
		checkManifestParity(t, data, test.valid, schema.Validate(yamlValue(t, data)))
	}
}
