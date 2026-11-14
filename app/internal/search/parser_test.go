package search

import (
	"errors"
	"strings"
	"testing"
)

func evaluate(e *Expr, tags map[string]bool) bool {
	switch e.Op {
	case All:
		return true
	case And:
		return evaluate(e.Left, tags) && evaluate(e.Right, tags)
	case Or:
		return evaluate(e.Left, tags) || evaluate(e.Right, tags)
	case Tag:
		if e.Term.Negated {
			return !tags[e.Term.SourceName]
		}
		return tags[e.Term.SourceName]
	default:
		panic("unknown operator")
	}
}

func TestGoldenGrammar(t *testing.T) {
	fixtures := []map[string]bool{{}, {"cat": true}, {"dog": true}, {"cat": true, "dog": true}, {"cat": true, "portrait": true}, {"кошки-猫": true}, {"Cat": true}}
	for _, tc := range []struct {
		query    string
		expected string
	}{
		{"", "1111111"}, {"cat", "0101100"}, {"cat && dog", "0001000"},
		{"cat || dog", "0111100"}, {"cat && !dog", "0100100"},
		{"(cat || dog) && !portrait", "0111000"}, {"unknown-name", "0000000"},
		{"!unknown-name", "1111111"}, {"cat || dog && !cat", "0111100"},
		{"кошки-猫", "0000010"}, {"Cat", "0000001"}, {" (( cat )) ", "0101100"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			e, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			var got strings.Builder
			for _, f := range fixtures {
				if evaluate(e, f) {
					got.WriteByte('1')
				} else {
					got.WriteByte('0')
				}
			}
			if got.String() != tc.expected {
				t.Fatalf("got %s want %s", got.String(), tc.expected)
			}
		})
	}
}

func TestInvalidGrammarReportsUTF16Position(t *testing.T) {
	for _, tc := range []struct {
		q   string
		pos int
	}{
		{"cat dog", 4}, {"cat & dog", 4}, {"cat &&", 6}, {"!(cat)", 1},
		{"!!cat", 1}, {"cat | dog", 4}, {"cat$", 3}, {"(cat", 4},
		{"cat)", 3}, {"()", 1}, {"cat && || dog", 7}, {"cat;DROP", 3},
		{"cat_", 3}, {"é\u0301", 1}, {"\xff", 0}, {"𐐀", 0}, {"!", 1},
	} {
		t.Run(tc.q, func(t *testing.T) {
			_, err := Parse(tc.q)
			var syntax *SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("expected syntax error: %v", err)
			}
			if syntax.Position != tc.pos {
				t.Fatalf("position %d want %d: %v", syntax.Position, tc.pos, err)
			}
		})
	}
}

func TestBoundedGrammar(t *testing.T) {
	for _, q := range []string{strings.Repeat("a", 4097), strings.Repeat("(", 33) + "a" + strings.Repeat(")", 33), strings.Repeat("a || ", 256) + "a"} {
		_, err := Parse(q)
		var syntax *SyntaxError
		if !errors.As(err, &syntax) || syntax.Code != "query_limit" {
			t.Fatalf("limit not enforced: %v", err)
		}
	}
	if _, err := Parse(strings.Repeat("(", 32) + "a" + strings.Repeat(")", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(strings.Repeat("a || ", 255) + "a"); err != nil {
		t.Fatal(err)
	}
}

func TestTagNameKeepsDotNetCharacterAndLengthRules(t *testing.T) {
	for _, s := range []string{"cat", "Cat", "кошки-猫", "é", "-", strings.Repeat("a", 50), "١"} {
		if err := ValidateTagName(s); err != nil {
			t.Fatalf("valid tag %q: %v", s, err)
		}
	}
	for _, s := range []string{"", strings.Repeat("a", 51), "a_b", "a b", "e\u0301", "²", "Ⅷ", "𐐀", "🙂", "\xff"} {
		if err := ValidateTagName(s); err == nil {
			t.Fatalf("invalid tag %q accepted", s)
		}
	}
}
