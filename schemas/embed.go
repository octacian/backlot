// Package schemas supplies the exact generated editor schemas shipped with Backlot.
package schemas

import "embed"

// Documents contains the generated Draft 2020-12 documents.
//
//go:embed *-v1.schema.json
var Documents embed.FS
