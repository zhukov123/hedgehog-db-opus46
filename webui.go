package hedgehog

import "embed"

// WebDist embeds the built web UI.
//
//go:embed all:web/dist
var WebDist embed.FS
