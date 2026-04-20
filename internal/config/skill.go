package config

import (
	"bytes"
	"fmt"

	"sigs.k8s.io/yaml"
)

type Skill struct {
	Frontmatter SkillFrontmatter
	Body        string
}

type SkillFrontmatter struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	ModelHint   string                    `json:"model_hint"`
	MaxTokens   int                       `json:"max_tokens"`
	Writeback   SkillWritebackFrontmatter `json:"writeback"`
}

type SkillWritebackFrontmatter struct {
	BlockFormat string `json:"block_format"`
}

var fence = []byte("---")

// ParseSkillFile extracts the YAML frontmatter and prompt body.
// Frontmatter is delimited by `---\n` on its own line at the top of the file
// and a matching `---\n` closing line.
func ParseSkillFile(data []byte) (*Skill, error) {
	if !bytes.HasPrefix(data, append(fence, '\n')) && !bytes.HasPrefix(data, append(fence, '\r', '\n')) {
		return nil, fmt.Errorf("frontmatter required: file must start with --- on its own line")
	}
	rest := data[len(fence):]
	if len(rest) > 0 && rest[0] == '\r' {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	end := -1
	for i := range len(rest) {
		lineStart := i == 0 || rest[i-1] == '\n'
		if !lineStart {
			continue
		}
		remaining := rest[i:]
		if bytes.HasPrefix(remaining, append(fence, '\n')) || bytes.HasPrefix(remaining, append(fence, '\r', '\n')) {
			end = i
			break
		}
		if bytes.Equal(remaining, fence) {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter: closing --- not found")
	}
	fmBytes := rest[:end]
	body := rest[end+len(fence):]
	if len(body) > 0 && body[0] == '\r' {
		body = body[1:]
	}
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}

	var fm SkillFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	return &Skill{Frontmatter: fm, Body: string(body)}, nil
}
