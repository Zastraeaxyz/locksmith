package main

import _ "embed"

// changelogMarkdown is the curated changelog shown by /changelog. Editing
// CHANGELOG.md changes the command output.
//
//go:embed CHANGELOG.md
var changelogMarkdown string
