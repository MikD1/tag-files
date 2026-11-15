package migrate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mikd1/tag-files/app/internal/search"
)

// Match the actual C# TagQueryConverter.cs Regex.Matches boundaries. In
// particular, ! belongs to a term only when immediately adjacent to its name.
// The legacy converter silently skips everything outside these tokens. Keep
// skipped non-whitespace as a blocking owner exception rather than guessing
// intent, including separated/extra ! and lone or extra & / | characters.
var legacyCategoryToken = regexp.MustCompile(`!?[a-zA-Z0-9\-]+|[()]|&&|\|\|`)

func legacyCategory(q string, tags []search.NamedTag) (*search.Expr, error) {
	if len(q) > search.MaxQueryLength {
		return nil, fmt.Errorf("legacy query exceeds target length limit")
	}
	p := legacyCategoryParser{}
	end := 0
	for _, span := range legacyCategoryToken.FindAllStringIndex(q, -1) {
		if strings.Trim(q[end:span[0]], " \t\r\n\v\f") != "" {
			return nil, fmt.Errorf("legacy tokenizer discards query characters at byte %d", end)
		}
		p.tokens = append(p.tokens, q[span[0]:span[1]])
		end = span[1]
	}
	if strings.Trim(q[end:], " \t\r\n\v\f") != "" {
		return nil, fmt.Errorf("legacy tokenizer discards query characters at byte %d", end)
	}
	if len(p.tokens) == 0 {
		return &search.Expr{Op: search.All}, nil
	}
	// Parse the converter's boolean token stream independently of the runtime
	// text parser, whose whitespace/negation/Unicode rules are different.
	ast, err := p.or(0)
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("legacy query expects an operator at token %d", p.pos)
	}
	return search.Bind(ast, tags)
}

type legacyCategoryParser struct {
	tokens     []string
	pos, terms int
}

func (p *legacyCategoryParser) take(token string) bool {
	if p.pos < len(p.tokens) && p.tokens[p.pos] == token {
		p.pos++
		return true
	}
	return false
}

func (p *legacyCategoryParser) or(depth int) (*search.Expr, error) {
	left, err := p.and(depth)
	if err != nil {
		return nil, err
	}
	for p.take("||") {
		right, err := p.and(depth)
		if err != nil {
			return nil, err
		}
		left = &search.Expr{Op: search.Or, Left: left, Right: right}
	}
	return left, nil
}

func (p *legacyCategoryParser) and(depth int) (*search.Expr, error) {
	left, err := p.term(depth)
	if err != nil {
		return nil, err
	}
	for p.take("&&") {
		right, err := p.term(depth)
		if err != nil {
			return nil, err
		}
		left = &search.Expr{Op: search.And, Left: left, Right: right}
	}
	return left, nil
}

func (p *legacyCategoryParser) term(depth int) (*search.Expr, error) {
	if p.take("(") {
		if depth >= search.MaxNesting {
			return nil, fmt.Errorf("legacy query exceeds target nesting limit")
		}
		expr, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		if !p.take(")") {
			return nil, fmt.Errorf("legacy query expects closing parenthesis at token %d", p.pos)
		}
		return expr, nil
	}
	if p.pos == len(p.tokens) {
		return nil, fmt.Errorf("legacy query expects a tag at end")
	}
	token := p.tokens[p.pos]
	if token == ")" || token == "&&" || token == "||" {
		return nil, fmt.Errorf("legacy query expects a tag at token %d", p.pos)
	}
	p.pos++
	p.terms++
	if p.terms > search.MaxTerms {
		return nil, fmt.Errorf("legacy query exceeds target term limit")
	}
	return &search.Expr{Op: search.Tag, Term: &search.Term{SourceName: strings.TrimPrefix(token, "!"), Negated: strings.HasPrefix(token, "!")}}, nil
}
