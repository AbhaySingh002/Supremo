package parser

import "strings"

// TagDeltaHandler is called for each parsed token delta.
// tag is "thought", "status", or "message".
// delta is the partial text content within that tag.
// complete is true when a closing tag (e.g. </thought>) is reached.
type TagDeltaHandler func(tag string, delta string, complete bool)

// StreamTagParser parses streaming chunks and extracts inline XML tags
// (<thought>, <status>, etc.) on the fly without waiting for the full response.
type StreamTagParser struct {
	handler    TagDeltaHandler
	inTag      bool
	tagBuffer  strings.Builder
	currentTag string
}

// NewStreamTagParser creates a new stream tag parser with the given callback.
func NewStreamTagParser(handler TagDeltaHandler) *StreamTagParser {
	return &StreamTagParser{handler: handler}
}

// Feed consumes a streaming text delta chunk and routes characters to the appropriate tag.
func (p *StreamTagParser) Feed(chunk string) {
	for i := 0; i < len(chunk); i++ {
		ch := chunk[i]
		if ch == '<' {
			p.inTag = true
			p.tagBuffer.Reset()
			p.tagBuffer.WriteByte(ch)
			continue
		}
		if p.inTag {
			p.tagBuffer.WriteByte(ch)
			if ch == '>' {
				p.inTag = false
				tagStr := p.tagBuffer.String()
				p.tagBuffer.Reset()

				if strings.HasPrefix(tagStr, "</") {
					closing := strings.ToLower(strings.Trim(tagStr, "</> "))
					if closing == "thinking" {
						closing = "thought"
					}
					if closing == p.currentTag {
						if p.handler != nil {
							p.handler(p.currentTag, "", true)
						}
						p.currentTag = ""
					} else {
						// Pass through unrecognized closing tags (e.g. </div>)
						if p.handler != nil {
							tag := p.currentTag
							if tag == "" {
								tag = "message"
							}
							p.handler(tag, tagStr, false)
						}
					}
				} else {
					opening := strings.ToLower(strings.Trim(tagStr, "<> "))
					switch opening {
					case "thought", "thinking":
						p.currentTag = "thought"
					case "status":
						p.currentTag = "status"
					case "message":
						p.currentTag = "message"
					default:
						// If not a recognized control tag, pass it through as content
						if p.handler != nil {
							tag := p.currentTag
							if tag == "" {
								tag = "message"
							}
							p.handler(tag, tagStr, false)
						}
					}
				}
			}
			continue
		}

		if p.handler != nil {
			tag := p.currentTag
			if tag == "" {
				tag = "message"
			}
			p.handler(tag, string(ch), false)
		}
	}
}

// Flush emits any trailing tag buffer or closes pending active tags.
func (p *StreamTagParser) Flush() {
	if p.inTag && p.tagBuffer.Len() > 0 {
		if p.handler != nil {
			tag := p.currentTag
			if tag == "" {
				tag = "message"
			}
			p.handler(tag, p.tagBuffer.String(), false)
		}
		p.inTag = false
		p.tagBuffer.Reset()
	}
	if p.currentTag != "" {
		if p.handler != nil {
			p.handler(p.currentTag, "", true)
		}
		p.currentTag = ""
	}
}
