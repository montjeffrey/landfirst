package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// Everything landfirst shows comes out of a repository, and a repository is
// somebody else's data: a commit subject, an author name, a ref name or a git
// error message may carry raw ESC and BEL bytes. Written to a terminal, an OSC
// sequence can retitle the window or - where the terminal allows it - replace
// the clipboard, and a CSI sequence can clear or reposition the screen to
// spoof a verdict or a confirmation prompt. Bidi overrides can make the text
// on screen read differently from what it is.
//
// Sanitize is the one place that decides what is safe to print. Every
// repo-derived string goes through it at render time (pad and clip apply it
// themselves; the rest call it directly), in the TUI and in the plain report.

// Sanitize returns s with every character a terminal could act on removed:
// C0 and C1 controls, DEL, and the bidi and other invisible format characters.
// Each removed character becomes "?" so tampering stays visible; tabs and line
// breaks become a space so multi-line git messages stay on one row. Invalid
// UTF-8 is replaced the same way.
func Sanitize(s string) string {
	if isClean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteByte('?')
		case r == '\t' || r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029':
			b.WriteByte(' ')
		case unsafeRune(r):
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isClean(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || unsafeRune(r) || r == '\t' || r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' {
			return false
		}
		i += size
	}
	return true
}

// unsafeRune: unicode.Cc is C0, DEL and C1; Cf is the format class, which
// holds the bidi overrides and isolates (U+202A-202E, U+2066-2069, U+200E/F,
// U+061C) along with zero-width and tag characters.
func unsafeRune(r rune) bool {
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// EncodeJSON is the -json output: the snapshot exactly as data, indented.
// encoding/json already escapes C0 controls, but writes DEL, the C1 range and
// the bidi and format characters as raw bytes, which a terminal showing piped
// output would still act on. They are rewritten as \uXXXX escapes, so the
// decoded strings are unchanged and the stream holds no raw control bytes.
func EncodeJSON(snap *protocol.Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return nil, err
	}
	return escapeJSONControls(buf.Bytes()), nil
}

func writeU(out *bytes.Buffer, hex string, r rune) {
	out.WriteString(`\u`)
	out.WriteByte(hex[r>>12&0xf])
	out.WriteByte(hex[r>>8&0xf])
	out.WriteByte(hex[r>>4&0xf])
	out.WriteByte(hex[r&0xf])
}

// escapeJSONControls works on the encoded bytes. Raw characters can only be
// inside string literals here - JSON syntax is ASCII - so replacing them is
// always a valid escape.
func escapeJSONControls(in []byte) []byte {
	const hex = "0123456789abcdef"
	var out bytes.Buffer
	out.Grow(len(in))
	for i := 0; i < len(in); {
		r, size := utf8.DecodeRune(in[i:])
		if size > 1 || r == 0x7f {
			if unsafeRune(r) {
				// Above U+FFFF JSON needs a surrogate pair; the low 16 bits alone
				// name a different character.
				if r > 0xffff {
					hi, lo := utf16.EncodeRune(r)
					writeU(&out, hex, hi)
					writeU(&out, hex, lo)
				} else {
					writeU(&out, hex, r)
				}
				i += size
				continue
			}
		}
		out.Write(in[i : i+size])
		i += size
	}
	return out.Bytes()
}
