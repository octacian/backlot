package schema

import (
	"os"
	"strings"
	"testing"
)

func TestBuildImageSchemaParity(t *testing.T) {
	data, err := os.ReadFile("../../examples/build-images/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := compile(t, "manifest-v1.schema.json")
	checkManifestParity(t, data, true, schema.Validate(yamlValue(t, data)))
	for _, change := range [][2]string{{"name: app, field: id", "name: app, field: tag"}, {"name: app, field: tag", "name: app, field: id"}, {"name: app, field: id", "name: app, field: id, port: wrong"}} {
		invalid := []byte(strings.Replace(string(data), change[0], change[1], 1))
		checkManifestParity(t, invalid, false, schema.Validate(yamlValue(t, invalid)))
	}
}
