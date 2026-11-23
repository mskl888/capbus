// Package dshbundle holds the static half of the DeepSeek Harness bundle that
// capbus installs: the plugin manifest, the Host half that answers
// /capbus/* on the Harness origin, and the sidebar panel.
//
// The bundle patch (cordis.patch.yml) is NOT here: it is generated on every
// sync, because it carries one row per declared capability.
package dshbundle

import "embed"

// FS is the embedded bundle. Paths are relative to the bundle root.
//
//go:embed package.json index.js client.js icon.svg docs locale
var FS embed.FS
