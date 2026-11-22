// Package configs provides the checked-in toolchain contract to development diagnostics.
package configs

import _ "embed"

//go:embed toolchain.json
var ToolchainJSON []byte
