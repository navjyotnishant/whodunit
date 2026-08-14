package linehash

import "testing"

func TestSameLineSameFileHashesEqual(t *testing.T) {
	a := Of("/repo/main.go", "return fmt.Errorf(\"boom\")")
	b := Of("/repo/main.go", "return fmt.Errorf(\"boom\")")
	if a != b {
		t.Errorf("identical lines hashed differently: %d vs %d", a, b)
	}
}

func TestSameLineDifferentFileHashesDiffer(t *testing.T) {
	// A common line — an import, a closing brace, a log call — must not
	// match across unrelated files and manufacture attribution.
	a := Of("/repo/a.go", "return nil")
	b := Of("/repo/b.go", "return nil")
	if a == b {
		t.Error("the same line in two files produced the same hash")
	}
}

func TestIndentationDoesNotChangeTheHash(t *testing.T) {
	// Reindenting during review should not sever the link to the line an
	// agent wrote; indentation is not the part anyone would call authorship.
	a := Of("/repo/main.go", "count++")
	b := Of("/repo/main.go", "\t\tcount++")
	c := Of("/repo/main.go", "    count++   ")
	if a != b || a != c {
		t.Errorf("indentation changed the hash: %d, %d, %d", a, b, c)
	}
}

func TestDifferentLinesHashDifferently(t *testing.T) {
	a := Of("/repo/main.go", "count++")
	b := Of("/repo/main.go", "count--")
	if a == b {
		t.Error("different lines collided")
	}
}

func TestOfTextSkipsInsubstantialLines(t *testing.T) {
	// Blank lines and lone braces appear in nearly every file. Counting
	// them would inflate every ratio toward the share of boilerplate two
	// files happen to have in common.
	text := "func main() {\n\n\t}\n\tdoWork()\n)\n"
	hashes := OfText("/repo/main.go", text)
	if len(hashes) != 2 {
		t.Fatalf("want 2 substantive lines (the func line and doWork), got %d", len(hashes))
	}
}

func TestOfTextOnEmptyInput(t *testing.T) {
	if got := OfText("/repo/main.go", ""); len(got) != 0 {
		t.Errorf("OfText on empty input = %v, want none", got)
	}
}

func TestSubstantive(t *testing.T) {
	cases := map[string]bool{
		"":                 false,
		"   ":              false,
		"}":                false,
		"\t}":              false,
		")":                false,
		"end":              false, // 3 chars, below the threshold
		"doWork()":         true,
		"return nil":       true,
		"x := 1":           true,
		"\timport \"fmt\"": true,
	}
	for line, want := range cases {
		if got := Substantive(line); got != want {
			t.Errorf("Substantive(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestSetOperations(t *testing.T) {
	s := Set{}
	h := Of("/repo/main.go", "return nil")
	if s.Has(h) {
		t.Error("empty set reported a hash")
	}
	s.Add(h)
	if !s.Has(h) {
		t.Error("added hash not found")
	}
}

func TestOfNormalizesSeparators(t *testing.T) {
	// The two sides of a match assemble the path differently: the staged
	// side joins with filepath.Join (backslashes on Windows), the agent side
	// takes what the transcript recorded (forward slashes, because the
	// agents are Node programs). Hashing the raw string made them disagree,
	// so on Windows no staged line matched a recorded one and every commit
	// silently degraded to observed — attribution quietly worse, with
	// nothing to say why.
	windows := Of(`C:\repo\main.go`, "doWork()")
	posix := Of("C:/repo/main.go", "doWork()")
	if windows != posix {
		t.Errorf("Of(%q) = %d and Of(%q) = %d; the same file must hash alike "+
			"however the path was built", `C:\repo\main.go`, windows,
			"C:/repo/main.go", posix)
	}
}

func TestOfIgnoresLineEndings(t *testing.T) {
	// git hands back CRLF on a Windows checkout with core.autocrlf on, while
	// a transcript records the same line with a bare newline.
	if Of("/repo/x.go", "doWork()") != Of("/repo/x.go", "doWork()\r") {
		t.Error("a trailing carriage return changes the hash")
	}

	crlf := OfText("/repo/x.go", "alpha()\r\nbeta()\r\n")
	lf := OfText("/repo/x.go", "alpha()\nbeta()\n")
	if len(crlf) != len(lf) {
		t.Fatalf("CRLF text produced %d hashes, LF text %d", len(crlf), len(lf))
	}
	for i := range lf {
		if crlf[i] != lf[i] {
			t.Errorf("line %d hashes differently under CRLF", i)
		}
	}
}
