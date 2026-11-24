# Search

The library search and categories use one tag expression language
(`app/internal/search`, parser version 1).

| Syntax | Meaning |
|---|---|
| `cat` | Items tagged `cat` |
| `cat && dog` | Both tags; `&&` binds tighter than `\|\|` |
| `cat \|\| dog` | Either tag |
| `!cat` | Items without `cat`; `!` applies to a single tag, not to `( … )` |
| `(cat \|\| dog) && park` | Grouping, up to 32 levels |
| empty | All items |

Rules:

- Tag names are Unicode letters, decimal digits and `-` (Basic Multilingual
  Plane). There is no quoting; two names separated only by a space are an error.
- Matching is exact and case sensitive. A name matches every tag with that name.
- An unknown name matches nothing; a negated unknown name matches everything.
- A query is at most 4096 UTF-16 code units and 256 terms. Errors carry a code
  and a position.
- The type filter (Photos, Videos) and sort apply on top of the expression.
