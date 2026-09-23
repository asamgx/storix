package classify

import (
	"fmt"
	"path"
	"strings"
)

// The pattern grammar.
//
// A Match is an anchored display path: "/Applications/{name}.app",
// "~/Library/Caches/{bundleid}", "/opt/homebrew/Cellar/{formula}". It is split
// on "/" and every segment is one of
//
//	literal        Library            matched exactly
//	*              *                  exactly one segment, any name
//	glob           *.ShipIt           path.Match against the whole segment
//	capture        {name}             one segment, captured under "name"
//	capture+glob   {name:v*}          captured, and the capture must match v*
//	template       {name}.app         literal text around a capture
//
// There is no "**". Every rule is anchored at the volume root, so matching a
// node costs one step per segment and the set of live trie states stays tiny.
// A pattern that means "anywhere under X" belongs to a detector, which can
// walk the tree itself, rather than to the catalog.
//
// A leading "~" is the scan user's home. It is expanded at compile time
// against Context.Home, and a second copy anchored at "Users/*" is always
// generated so another user's home classifies the same way.

// segKind is what one pattern segment matches.
type segKind uint8

const (
	// segLiteral matches one exact name.
	segLiteral segKind = iota
	// segAny matches any single segment.
	segAny
	// segGlob matches the whole segment against a path.Match pattern.
	segGlob
	// segTemplate matches literal text around a capture.
	segTemplate
)

// segment is one compiled pattern segment.
type segment struct {
	kind segKind
	// text is the literal for segLiteral and the glob for segGlob.
	text string
	// name is the capture name for segTemplate.
	name string
	// prefix and suffix are the literal text around a segTemplate capture.
	prefix, suffix string
	// glob constrains a segTemplate capture; empty accepts anything.
	glob string
	// literal records whether the segment carries literal text, which is
	// what specificity counts.
	literal bool
}

// key identifies a segment for trie edge deduplication. Two edges with the
// same key match the same names, so they share a child.
func (s segment) key() string {
	switch s.kind {
	case segAny:
		return "*"
	case segGlob:
		return "g\x00" + s.text
	case segTemplate:
		return "t\x00" + s.prefix + "\x00" + s.glob + "\x00" + s.suffix + "\x00" + s.name
	default:
		return "l\x00" + s.text
	}
}

// match reports whether name matches the segment, and returns the captured
// text when the segment captures.
func (s segment) match(name string) (capture string, ok bool) {
	switch s.kind {
	case segLiteral:
		return "", name == s.text
	case segAny:
		return "", true
	case segGlob:
		m, err := path.Match(s.text, name)
		return "", err == nil && m
	default:
		if len(name) < len(s.prefix)+len(s.suffix)+1 {
			return "", false
		}
		if !strings.HasPrefix(name, s.prefix) || !strings.HasSuffix(name, s.suffix) {
			return "", false
		}
		mid := name[len(s.prefix) : len(name)-len(s.suffix)]
		if s.glob != "" {
			m, err := path.Match(s.glob, mid)
			if err != nil || !m {
				return "", false
			}
		}
		return mid, true
	}
}

// pattern is a compiled Match: its segments and its specificity.
type pattern struct {
	segs     []segment
	literals uint16
}

// depth is the number of segments, the first term of specificity.
func (p pattern) depth() uint16 { return uint16(len(p.segs)) }

// parsePattern compiles one anchored pattern. The leading "~" must already
// have been expanded; see variants.
func parsePattern(s string) (pattern, error) {
	clean := strings.Trim(s, "/")
	if clean == "" {
		return pattern{}, fmt.Errorf("pattern %q matches the root, which no rule may claim", s)
	}
	parts := strings.Split(clean, "/")
	p := pattern{segs: make([]segment, 0, len(parts))}
	for _, part := range parts {
		seg, err := parseSegment(part)
		if err != nil {
			return pattern{}, fmt.Errorf("pattern %q: %w", s, err)
		}
		if seg.literal {
			p.literals++
		}
		p.segs = append(p.segs, seg)
	}
	return p, nil
}

// parseSegment compiles one segment.
func parseSegment(part string) (segment, error) {
	switch {
	case part == "":
		return segment{}, fmt.Errorf("empty segment")
	case part == "*":
		return segment{kind: segAny}, nil
	case strings.ContainsRune(part, '{'):
		return parseTemplate(part)
	case strings.ContainsRune(part, '}'):
		return segment{}, fmt.Errorf("segment %q closes a capture that was never opened", part)
	case strings.ContainsAny(part, "*?["):
		if _, err := path.Match(part, ""); err != nil {
			return segment{}, fmt.Errorf("segment %q is not a valid glob: %w", part, err)
		}
		return segment{kind: segGlob, text: part, literal: true}, nil
	default:
		return segment{kind: segLiteral, text: part, literal: true}, nil
	}
}

// parseTemplate compiles a segment holding exactly one capture.
func parseTemplate(part string) (segment, error) {
	open := strings.IndexByte(part, '{')
	closeAt := strings.IndexByte(part, '}')
	if closeAt < open {
		return segment{}, fmt.Errorf("segment %q has an unclosed capture", part)
	}
	inner := part[open+1 : closeAt]
	rest := part[closeAt+1:]
	if strings.ContainsAny(rest, "{}") {
		return segment{}, fmt.Errorf("segment %q holds more than one capture", part)
	}
	seg := segment{
		kind:   segTemplate,
		prefix: part[:open],
		suffix: rest,
	}
	if name, glob, found := strings.Cut(inner, ":"); found {
		seg.name, seg.glob = name, glob
		if _, err := path.Match(glob, ""); err != nil {
			return segment{}, fmt.Errorf("segment %q: capture glob %q is invalid: %w", part, glob, err)
		}
	} else {
		seg.name = inner
	}
	if seg.name == "" {
		return segment{}, fmt.Errorf("segment %q has an unnamed capture", part)
	}
	if strings.ContainsAny(seg.name, "*?[]/") {
		return segment{}, fmt.Errorf("segment %q has an invalid capture name %q", part, seg.name)
	}
	seg.literal = seg.prefix != "" || seg.suffix != ""
	return seg, nil
}

// captureNames lists the captures a pattern binds, in order.
func (p pattern) captureNames() []string {
	var out []string
	for _, s := range p.segs {
		if s.kind == segTemplate {
			out = append(out, s.name)
		}
	}
	return out
}

// variants expands one Match into the anchored patterns it stands for. A
// pattern without "~" stands for itself; one with "~" is anchored once at the
// scan user's home, once at every other home the tree showed, and once at
// "Users/*" so a home nobody named still classifies.
func variants(match string, ctx Context) []string {
	if match != "~" && !strings.HasPrefix(match, "~/") {
		return []string{match}
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(match, "~"), "/")
	homes := ctx.homePrefixes()
	out := make([]string, 0, len(homes))
	for _, h := range homes {
		out = append(out, path.Join(h, rest))
	}
	return out
}

// capture is one bound capture, kept as a linked list so that the common case
// of a node under no capture at all costs no allocation.
type capture struct {
	name, value string
	prev        *capture
}

// lookup returns the value bound to name.
func (c *capture) lookup(name string) (string, bool) {
	for ; c != nil; c = c.prev {
		if c.name == name {
			return c.value, true
		}
	}
	return "", false
}

// subst replaces every "{name}" in s with the captured value. A reference to
// an unbound capture is left as it is, which cannot happen after New has
// validated the rule and is visible rather than silent if it ever does.
func subst(s string, caps *capture) string {
	if caps == nil || !strings.ContainsRune(s, '{') {
		return s
	}
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			break
		}
		closeAt := strings.IndexByte(s[open:], '}')
		if closeAt < 0 {
			break
		}
		closeAt += open
		v, ok := caps.lookup(s[open+1 : closeAt])
		if !ok {
			b.WriteString(s[:closeAt+1])
		} else {
			b.WriteString(s[:open])
			b.WriteString(v)
		}
		s = s[closeAt+1:]
	}
	b.WriteString(s)
	return b.String()
}

// references lists the capture names a template string mentions.
func references(s string) []string {
	var out []string
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return out
		}
		closeAt := strings.IndexByte(s[open:], '}')
		if closeAt < 0 {
			return out
		}
		closeAt += open
		if name := s[open+1 : closeAt]; name != "" {
			out = append(out, name)
		}
		s = s[closeAt+1:]
	}
}
