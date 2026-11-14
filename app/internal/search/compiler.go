package search

import "encoding/json"

// Compile returns a predicate for the fixed outer item alias i. Every bound
// group is a single JSON parameter, so many duplicate IDs cannot exhaust
// SQLite's placeholder limit. No user text is interpolated into SQL.
func Compile(e *Expr) (string, []any, error) {
	if err := Validate(e); err != nil {
		return "", nil, err
	}
	args := []any{}
	var compile func(*Expr) string
	compile = func(e *Expr) string {
		switch e.Op {
		case All:
			return "1"
		case Tag:
			if len(e.Term.IDs) == 0 {
				if e.Term.Negated {
					return "1"
				}
				return "0"
			}
			ids, _ := json.Marshal(e.Term.IDs)
			args = append(args, string(ids))
			predicate := `EXISTS (SELECT 1 FROM item_tags AS it WHERE it.item_id=i.id AND it.tag_id IN (SELECT CAST(value AS INTEGER) FROM json_each(?)))`
			if e.Term.Negated {
				return "NOT " + predicate
			}
			return predicate
		case And, Or:
			left, right := compile(e.Left), compile(e.Right)
			op := " AND "
			if e.Op == Or {
				op = " OR "
			}
			return "(" + left + op + right + ")"
		}
		panic("validated operator")
	}
	return compile(e), args, nil
}
