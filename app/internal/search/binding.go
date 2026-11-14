package search

import (
	"fmt"
	"sort"
	"strings"
)

const MaxSafeID int64 = 9007199254740991

type NamedTag struct {
	ID   int64
	Name string
}
type DisplayTerm struct {
	TagID       *int64 `json:"tagId,omitempty"`
	CurrentName string `json:"currentName"`
	SourceName  string `json:"sourceName,omitempty"`
}

func Validate(e *Expr) error {
	nodes, terms := 0, 0
	var visit func(*Expr, int) error
	visit = func(e *Expr, depth int) error {
		nodes++
		if e == nil || nodes > 2*MaxTerms+1 || depth > MaxTerms+MaxNesting {
			return fmt.Errorf("Invalid or oversized query AST")
		}
		switch e.Op {
		case All:
			if e.Term != nil || e.Left != nil || e.Right != nil {
				return fmt.Errorf("Invalid all node")
			}
		case Tag:
			terms++
			if terms > MaxTerms || e.Term == nil || e.Left != nil || e.Right != nil {
				return fmt.Errorf("Invalid term node")
			}
			if e.Term.SourceName == "" || utf16Length(e.Term.SourceName) > MaxQueryLength {
				return fmt.Errorf("Invalid source name")
			}
			for _, r := range e.Term.SourceName {
				if !tagRune(r) {
					return fmt.Errorf("Invalid source name")
				}
			}
			seen := map[int64]bool{}
			for _, id := range e.Term.IDs {
				if id < 1 || id > MaxSafeID || seen[id] {
					return fmt.Errorf("Invalid bound tag ID")
				}
				seen[id] = true
			}
		case And, Or:
			if e.Term != nil {
				return fmt.Errorf("Invalid binary node")
			}
			if err := visit(e.Left, depth+1); err != nil {
				return err
			}
			return visit(e.Right, depth+1)
		default:
			return fmt.Errorf("Unknown query AST operator")
		}
		return nil
	}
	return visit(e, 0)
}

func tagIndex(tags []NamedTag) (map[int64]string, map[string][]int64, error) {
	names, ids := map[int64]string{}, map[string][]int64{}
	for _, tag := range tags {
		if tag.ID < 1 || tag.ID > MaxSafeID {
			return nil, nil, fmt.Errorf("Invalid tag ID")
		}
		if _, ok := names[tag.ID]; ok {
			return nil, nil, fmt.Errorf("Repeated tag ID")
		}
		names[tag.ID] = tag.Name
		ids[tag.Name] = append(ids[tag.Name], tag.ID)
	}
	for name := range ids {
		sort.Slice(ids[name], func(i, j int) bool { return ids[name][i] < ids[name][j] })
	}
	return names, ids, nil
}

func copyExpr(e *Expr) *Expr {
	if e == nil {
		return nil
	}
	c := *e
	if e.Term != nil {
		term := *e.Term
		term.IDs = append([]int64(nil), e.Term.IDs...)
		c.Term = &term
	}
	c.Left, c.Right = copyExpr(e.Left), copyExpr(e.Right)
	return &c
}

func eachTerm(e *Expr, fn func(*Term)) {
	if e.Term != nil {
		fn(e.Term)
	}
	if e.Left != nil {
		eachTerm(e.Left, fn)
	}
	if e.Right != nil {
		eachTerm(e.Right, fn)
	}
}

// Bind is used only for an explicit text create/edit or an ad-hoc query.
// A saved search is executed by its stored AST, never by rebinding its labels.
func Bind(e *Expr, tags []NamedTag) (*Expr, error) {
	if err := Validate(e); err != nil {
		return nil, err
	}
	_, ids, err := tagIndex(tags)
	if err != nil {
		return nil, err
	}
	bound := copyExpr(e)
	eachTerm(bound, func(t *Term) { t.IDs = append([]int64(nil), ids[t.SourceName]...) })
	return bound, nil
}

func ExpandOnCreate(e *Expr, name string, tags []NamedTag) (*Expr, bool, error) {
	if err := Validate(e); err != nil {
		return nil, false, err
	}
	names, ids, err := tagIndex(tags)
	if err != nil {
		return nil, false, err
	}
	copy := copyExpr(e)
	changed := false
	eachTerm(copy, func(t *Term) {
		matches := len(t.IDs) == 0 && t.SourceName == name
		seen := map[int64]bool{}
		for _, id := range t.IDs {
			seen[id] = true
			if names[id] == name {
				matches = true
			}
		}
		if !matches {
			return
		}
		for _, id := range ids[name] {
			if !seen[id] {
				t.IDs = append(t.IDs, id)
				seen[id] = true
				changed = true
			}
		}
		sort.Slice(t.IDs, func(i, j int) bool { return t.IDs[i] < t.IDs[j] })
	})
	return copy, changed, nil
}

func UsesTag(e *Expr, id int64) bool {
	found := false
	eachTerm(e, func(t *Term) {
		for _, bound := range t.IDs {
			if id == bound {
				found = true
			}
		}
	})
	return found
}

func ValidationState(e *Expr) string {
	state := "valid"
	eachTerm(e, func(t *Term) {
		if len(t.IDs) == 0 {
			state = "unresolved"
		}
	})
	return state
}

func DisplayTerms(e *Expr, tags []NamedTag) ([]DisplayTerm, error) {
	if err := Validate(e); err != nil {
		return nil, err
	}
	names, _, err := tagIndex(tags)
	if err != nil {
		return nil, err
	}
	terms := []DisplayTerm{}
	eachTerm(e, func(t *Term) {
		if len(t.IDs) == 0 {
			terms = append(terms, DisplayTerm{CurrentName: t.SourceName})
			return
		}
		for _, id := range t.IDs {
			name, ok := names[id]
			if !ok {
				err = fmt.Errorf("Bound tag %d is missing", id)
				return
			}
			display := DisplayTerm{TagID: &id, CurrentName: name}
			if name != t.SourceName {
				display.SourceName = t.SourceName
			}
			terms = append(terms, display)
		}
	})
	return terms, err
}

func Render(e *Expr, tags []NamedTag) (string, error) {
	if err := Validate(e); err != nil {
		return "", err
	}
	names, _, err := tagIndex(tags)
	if err != nil {
		return "", err
	}
	var render func(*Expr, int) (string, error)
	render = func(e *Expr, parentPrecedence int) (string, error) {
		switch e.Op {
		case All:
			return "", nil
		case Tag:
			labels := []string{}
			seen := map[string]bool{}
			if len(e.Term.IDs) == 0 {
				labels = append(labels, e.Term.SourceName)
			}
			for _, id := range e.Term.IDs {
				name, ok := names[id]
				if !ok {
					return "", fmt.Errorf("Bound tag %d is missing", id)
				}
				if !seen[name] {
					labels = append(labels, name)
					seen[name] = true
				}
			}
			join := " || "
			if e.Term.Negated {
				join = " && "
				for i := range labels {
					labels[i] = "!" + labels[i]
				}
			}
			text := strings.Join(labels, join)
			if len(labels) > 1 {
				text = "(" + text + ")"
			}
			return text, nil
		case And, Or:
			precedence := 2
			if e.Op == Or {
				precedence = 1
			}
			left, err := render(e.Left, precedence)
			if err != nil {
				return "", err
			}
			right, err := render(e.Right, precedence)
			if err != nil {
				return "", err
			}
			op := " && "
			if e.Op == Or {
				op = " || "
			}
			text := left + op + right
			if precedence < parentPrecedence {
				text = "(" + text + ")"
			}
			return text, nil
		}
		panic("validated operator")
	}
	return render(e, 0)
}
