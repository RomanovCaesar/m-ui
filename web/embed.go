// Package webassets embeds the panel frontend in the m-ui executable.
package webassets

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed *.html *.js *.css media/*
var embedded embed.FS

// Files preserves the historical "web/<name>" lookup contract used by the
// application while keeping the frontend in its own package.
type Files struct{}

func (Files) Open(name string) (fs.File, error) {
	return embedded.Open(strings.TrimPrefix(name, "web/"))
}

func (Files) ReadDir(name string) ([]fs.DirEntry, error) {
	return embedded.ReadDir(strings.TrimPrefix(name, "web/"))
}

func (Files) ReadFile(name string) ([]byte, error) {
	return embedded.ReadFile(strings.TrimPrefix(name, "web/"))
}

var FS Files
