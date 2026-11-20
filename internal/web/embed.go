package web

import "embed"

// Assets holds the static test frontend served by the API.
//
//go:embed index.html app.js style.css
var Assets embed.FS
