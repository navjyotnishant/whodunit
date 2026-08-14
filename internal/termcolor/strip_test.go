// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Measuring styled text by what appears on screen.

package termcolor

import "testing"

// Anything drawing a box or aligning a column has to measure the visible
// text. A styled string is several bytes longer than what it renders as, so
// padding the styled form draws a box wider than its contents by exactly the
// length of the escape sequences inside.
func TestStripMeasuresWhatIsOnScreen(t *testing.T) {
	// The sequences are written literally rather than through New(): a
	// writer that is not a terminal disables colour, so building the input
	// with the styler would produce a string with nothing to strip and a
	// test that passes without testing anything.
	styled := string(Good) + "ok" + string(reset) + " and " +
		string(Bad) + "not ok" + string(reset)

	got := Strip(styled)
	if got != "ok and not ok" {
		t.Fatalf("Strip = %q, want %q", got, "ok and not ok")
	}
	if len(styled) <= len(got) {
		t.Errorf("the styled input carried no escapes to strip (%d vs %d bytes)",
			len(styled), len(got))
	}
}

func TestStripLeavesPlainTextAlone(t *testing.T) {
	const plain = "no escapes here — just text"
	if got := Strip(plain); got != plain {
		t.Errorf("Strip altered plain text: %q", got)
	}
}

// A truncated escape sequence must not swallow the rest of the line: a
// corrupt byte in a log should cost one glyph, not the whole entry.
func TestStripSurvivesAnUnterminatedEscape(t *testing.T) {
	if got := Strip("before\x1b[38;5;34"); got != "before" {
		t.Errorf("Strip = %q, want %q", got, "before")
	}
}
