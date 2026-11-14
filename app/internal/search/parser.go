// Package search implements the versioned, bounded tag expression language.
// It does not depend on HTTP, SQLite or a storage SDK.
package search

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

const (
	ParserVersion  = 1
	MaxQueryLength = 4096
	MaxTerms       = 256
	MaxNesting     = 32
)

type Operator string

const (
	All Operator = "all"
	Tag Operator = "tag"
	And Operator = "and"
	Or  Operator = "or"
)

// Term retains an unresolved source name independently of bound IDs. Renaming
// a different tag into this name must not silently bind the unresolved term.
type Term struct {
	SourceName string  `json:"sourceName"`
	IDs        []int64 `json:"tagIds,omitempty"`
	Negated    bool    `json:"negated,omitempty"`
}

type Expr struct {
	Op    Operator `json:"op"`
	Term  *Term    `json:"term,omitempty"`
	Left  *Expr    `json:"left,omitempty"`
	Right *Expr    `json:"right,omitempty"`
}

type SyntaxError struct {
	Code     string `json:"code"`
	Position int    `json:"position"`
	Message  string `json:"message"`
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("%s at position %d", e.Message, e.Position) }

// .NET char.IsLetterOrDigit operates on UTF-16 code units: supplementary
// letters are surrogate pairs and were never valid legacy tag characters.
// IsDigit is decimal Nd, not the broader Unicode Number category.
func tagRune(r rune) bool {
	return r <= 0xffff && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-')
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func ValidateTagName(name string) error {
	if !utf8.ValidString(name) || utf16Length(name) < 1 || utf16Length(name) > 50 {
		return fmt.Errorf("Tag name must contain 1 to 50 characters")
	}
	for _, r := range name {
		if !tagRune(r) {
			return fmt.Errorf("Tag name may contain only letters, decimal digits and hyphens")
		}
	}
	return nil
}

type parser struct {
	input      []rune
	pos, terms int
}

func Parse(query string) (*Expr, error) {
	if !utf8.ValidString(query) {
		position := 0
		for _, r := range query {
			if r == utf8.RuneError {
				break
			}
			position++
			if r > 0xffff {
				position++
			}
		}
		return nil, &SyntaxError{Code: "invalid_query", Position: position, Message: "Invalid UTF-8"}
	}
	if utf16Length(query) > MaxQueryLength {
		return nil, &SyntaxError{Code: "query_limit", Position: MaxQueryLength, Message: "Query exceeds 4096 characters"}
	}
	p := parser{input: []rune(query)}
	p.space()
	if p.pos == len(p.input) {
		return &Expr{Op: All}, nil
	}
	e, err := p.or(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.input) {
		return nil, p.fail("Expected an operator")
	}
	return e, nil
}

func (p *parser) space() {
	for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
		p.pos++
	}
}
func (p *parser) fail(message string) *SyntaxError {
	position := 0
	for _, r := range p.input[:p.pos] {
		position++
		if r > 0xffff {
			position++
		}
	}
	return &SyntaxError{Code: "invalid_query", Position: position, Message: message}
}
func (p *parser) take(op string) bool {
	p.space()
	r := []rune(op)
	if p.pos+len(r) > len(p.input) {
		return false
	}
	for i, c := range r {
		if p.input[p.pos+i] != c {
			return false
		}
	}
	p.pos += len(r)
	return true
}
func (p *parser) or(depth int) (*Expr, error) {
	left, err := p.and(depth)
	if err != nil {
		return nil, err
	}
	for p.take("||") {
		right, err := p.and(depth)
		if err != nil {
			return nil, err
		}
		left = &Expr{Op: Or, Left: left, Right: right}
	}
	return left, nil
}
func (p *parser) and(depth int) (*Expr, error) {
	left, err := p.term(depth)
	if err != nil {
		return nil, err
	}
	for p.take("&&") {
		right, err := p.term(depth)
		if err != nil {
			return nil, err
		}
		left = &Expr{Op: And, Left: left, Right: right}
	}
	return left, nil
}
func (p *parser) term(depth int) (*Expr, error) {
	p.space()
	if p.take("(") {
		if depth >= MaxNesting {
			e := p.fail("Query exceeds 32 nested groups")
			e.Code = "query_limit"
			return nil, e
		}
		e, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		if !p.take(")") {
			return nil, p.fail("Expected a closing parenthesis")
		}
		return e, nil
	}
	negated := p.take("!")
	p.space()
	start := p.pos
	for p.pos < len(p.input) && tagRune(p.input[p.pos]) {
		p.pos++
	}
	if start == p.pos {
		return nil, p.fail("Expected a tag name")
	}
	p.terms++
	if p.terms > MaxTerms {
		e := p.fail("Query exceeds 256 terms")
		e.Code = "query_limit"
		return nil, e
	}
	return &Expr{Op: Tag, Term: &Term{SourceName: string(p.input[start:p.pos]), Negated: negated}}, nil
}
