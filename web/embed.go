package web

import "embed"

// FS contains the browser assets served by the Go server.
//
//go:embed index.html editor.html assets/*
var FS embed.FS
