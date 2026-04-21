// Package builtin — AST node types for the second pass of the shell
// interpreter.
//
// This file defines the concrete nodes produced by Parse() in parser.go.
// The evaluator (M2.6) walks these nodes directly; the expander (M2.5)
// walks Word.Parts.
//
// Design notes
// ------------
//
// The Go parser matches the Python reference (src/python/shell.py) with two
// structural differences documented inline:
//
//   - AndOr is flat (Children + Ops) rather than a left-leaning tree of
//     AndNode/OrNode. The two representations carry the same information
//     and are equivalent under a left-associative interpretation, but the
//     flat form is friendlier for the evaluator.
//   - SimpleCommand keeps Assignments as a first-class []Assignment slice
//     rather than a flat args list. The Python reference only splits a
//     single leading NAME=VALUE into AssignmentNode when it is the whole
//     command; here, leading assignments are ALWAYS extracted, and a bare
//     assignment (no command) is still a SimpleCommand with no Words.
//
// Both parsers collapse "pipelines of one" and "lists of one": a single
// command is not wrapped in a Pipeline, and a single and-or chain is not
// wrapped in a List. This keeps the AST compact without losing information.
// Parse() itself is the sole exception: its return value is always a List
// (or an empty List for an empty program) so callers have a uniform top
// level to iterate.
package builtin

// Node is the AST node root interface. Every concrete AST node type
// implements it. The marker method keeps the set of nodes closed without
// forcing callers to type-switch on an anonymous interface.
type Node interface{ astNode() }

// SimpleCommand is the canonical "one program + args" form.
//
// Examples:
//
//	ls -la            → Words=[ls, -la]
//	FOO=1 ls          → Assignments=[FOO=1], Words=[ls]
//	FOO=1             → Assignments=[FOO=1], Words=nil
//	> out.txt         → Words=nil, Redirections=[>out.txt]
//
// Either Words or Assignments or Redirections must be non-empty; a truly
// empty SimpleCommand is never produced by the parser.
type SimpleCommand struct {
	Assignments  []Assignment
	Words        []Word
	Redirections []Redirection
}

func (*SimpleCommand) astNode() {}

// Pipeline is two-or-more commands joined by `|`. A one-command "pipeline"
// is unwrapped by the parser, so you will only ever see a Pipeline with
// len(Commands) >= 2.
type Pipeline struct {
	Commands []Node
}

func (*Pipeline) astNode() {}

// List is two-or-more statements separated by `;` or newline. A single
// statement is unwrapped. The top-level return of Parse() is always a List
// (possibly empty) so callers have a uniform shape.
type List struct {
	Statements []Node
}

func (*List) astNode() {}

// AndOrOp enumerates the two short-circuit operators.
type AndOrOp int

const (
	// OpAnd is `&&`. Right is evaluated only if Left succeeded.
	OpAnd AndOrOp = iota
	// OpOr is `||`. Right is evaluated only if Left failed.
	OpOr
)

// String returns the operator text.
func (o AndOrOp) String() string {
	switch o {
	case OpAnd:
		return "&&"
	case OpOr:
		return "||"
	}
	return "AndOrOp(?)"
}

// AndOr is a flat short-circuit chain. Children are interleaved with Ops —
// there is always exactly one fewer Op than Child. A one-element chain is
// unwrapped to just that element, so len(Children) >= 2 when you see one.
//
// Example: `a && b || c` yields Children=[a, b, c], Ops=[OpAnd, OpOr].
//
// The chain is evaluated left-to-right (left-associative) which matches
// POSIX shell semantics: `a && b || c` = `(a && b) || c`.
type AndOr struct {
	Children []Node
	Ops      []AndOrOp
}

func (*AndOr) astNode() {}

// If is the full if/elif/else/fi construct.
type If struct {
	Cond  Node
	Then  Node
	Elifs []ElifClause
	Else  Node // nil if no else-branch
}

func (*If) astNode() {}

// ElifClause is one `elif COND then BODY` segment.
type ElifClause struct {
	Cond Node
	Then Node
}

// For is `for VAR in items; do ...; done`. If Items is nil, the loop is
// the positional-parameters form `for x; do ...`. An explicit empty list
// (`for x in; do ...`) yields a non-nil zero-length Items slice — the
// distinction matters because the positional form iterates "$@" at
// evaluation time.
type For struct {
	Var   string
	Items []Word
	Body  Node
}

func (*For) astNode() {}

// While is `while|until COND; do BODY; done`. Until=true inverts the loop
// condition.
type While struct {
	Cond  Node
	Body  Node
	Until bool
}

func (*While) astNode() {}

// Case is `case WORD in CLAUSES esac`.
type Case struct {
	Word    Word
	Clauses []CaseClause
}

func (*Case) astNode() {}

// CaseClause is one `PATTERNS) BODY ;;` entry. Patterns has at least one
// element; alternatives are written with `|` in source and produce
// additional Patterns entries.
type CaseClause struct {
	Patterns []Word
	Body     Node // may be nil for an empty clause body
}

// Subshell is `( LIST )`. This VM does not fork, so the evaluator runs Body
// in a clone of the current context — no mutations escape.
type Subshell struct {
	Body Node
}

func (*Subshell) astNode() {}

// Word is a single word from the tokenizer carried through unchanged.
// Raw is the flattened Value (what the expander would see with quoting
// erased); Parts preserves the quoted-segment breakdown the expander needs.
type Word struct {
	Raw   string
	Parts []WordPart
	Pos   int
}

// Assignment is a `NAME=VALUE` prefix. NAME always matches
// [A-Za-z_][A-Za-z0-9_]*. VALUE is a Word (possibly empty).
type Assignment struct {
	Name  string
	Value Word
}

// RedirKind enumerates supported redirection operators.
type RedirKind int

const (
	// RedirOut is `>`: truncate-and-write stdout to Target.
	RedirOut RedirKind = iota
	// RedirAppend is `>>`: append stdout to Target.
	RedirAppend
	// RedirIn is `<`: read stdin from Target.
	RedirIn
	// RedirErr is `2>`: truncate-and-write stderr to Target.
	RedirErr
	// RedirErrAppend is `2>>`: append stderr to Target.
	RedirErrAppend
	// RedirErrOut is `2>&1`: duplicate stderr onto stdout. Target is
	// ignored.
	RedirErrOut
	// RedirBoth is `&>` / `>&`: send both stdout and stderr to Target.
	RedirBoth
)

// String returns the operator text.
func (k RedirKind) String() string {
	switch k {
	case RedirOut:
		return ">"
	case RedirAppend:
		return ">>"
	case RedirIn:
		return "<"
	case RedirErr:
		return "2>"
	case RedirErrAppend:
		return "2>>"
	case RedirErrOut:
		return "2>&1"
	case RedirBoth:
		return "&>"
	}
	return "RedirKind(?)"
}

// Redirection is a single redirection operator + target. For RedirErrOut,
// Target is zero-valued and ignored by the evaluator.
type Redirection struct {
	Kind   RedirKind
	Target Word
}
