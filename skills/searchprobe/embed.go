// Package searchprobeskill holds the single canonical, release-bundled skill.
package searchprobeskill

import _ "embed"

// Content is embedded so installation never depends on a checkout or network.
//
//go:embed SKILL.md
var Content string
