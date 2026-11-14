// Package plugin is bx's Claude Code and Codex plugin: its manifests, MCP
// config and skill. The binary embeds the skill from here too, so
// `bx skill install` and the plugin ship the same text.
package plugin

import _ "embed"

// SkillMD is the bx agent skill.
//
//go:embed skills/bx/SKILL.md
var SkillMD []byte
