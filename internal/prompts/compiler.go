package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/protocol"
)

type Compilation struct {
	Control  string
	Metadata models.PromptMetadata
}

// Compile accepts one of the production profiles; illegal mode/role/contract
// combinations are therefore unrepresentable.
func Compile(profile protocol.Profile) (Compilation, error) {
	if profile == "" {
		profile = protocol.Conversational
	}
	if err := protocol.Validate(profile); err != nil {
		return Compilation{}, err
	}
	base, templates, err := stableControl()
	if err != nil {
		return Compilation{}, err
	}
	control := base
	if id, content := profileTemplate(profile); content != "" {
		control += "\n\n# Profile: " + string(profile) + "\n" + content
		templates = append(templates, template(id, "2", content))
	}
	metadata := models.PromptMetadata{Profile: string(profile), Templates: templates}
	metadata.Sections = []models.PromptSection{{Name: "stable_control", Tokens: estimate(control)}}
	return Compilation{Control: strings.TrimSpace(control), Metadata: metadata}, nil
}

func stableControl() (string, []models.PromptTemplate, error) {
	names := []string{"system", "tools"}
	var control strings.Builder
	templates := make([]models.PromptTemplate, 0, len(names))
	for index, name := range names {
		content, err := templateFiles.ReadFile("templates/" + name + ".md")
		if err != nil {
			return "", nil, err
		}
		if index > 0 {
			control.WriteString("\n\n")
		}
		control.Write(content)
		templates = append(templates, template("control."+name, "2", string(content)))
	}
	return control.String(), templates, nil
}

func profileTemplate(profile protocol.Profile) (string, string) {
	switch profile {
	case protocol.Execution:
		return "profile.execution", sweProfile
	case protocol.SideAnswer:
		return "profile.side_answer", sideAnswer
	default:
		return "", ""
	}
}

func template(id, version, content string) models.PromptTemplate {
	hash := sha256.Sum256([]byte(content))
	return models.PromptTemplate{ID: id, Version: version, Hash: hex.EncodeToString(hash[:])}
}

func estimate(text string) int {
	fields, inWord := 0, false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if inWord {
				fields++
				inWord = false
			}
			continue
		}
		inWord = true
	}
	if inWord {
		fields++
	}
	return max(1, (fields+2)/3)
}
