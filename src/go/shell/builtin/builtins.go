// Package builtin — default builtin registration.
//
// NewDefaultBuiltinRegistry installs the 31 built-in commands ported
// from src/python/shell.py (the `_cmd_*` family). Callers wire this
// registry into an Evaluator via Evaluator.Builtins; the driver (M2.8)
// will use it directly.
package builtin

// NewDefaultBuiltinRegistry returns a BuiltinRegistry pre-loaded with
// the 31 built-in commands. The returned registry is independent — any
// mutation (Register / Unregister) affects only this instance.
func NewDefaultBuiltinRegistry() *BuiltinRegistry {
	r := NewBuiltinRegistry()

	// Filesystem / directory (12)
	r.Register("cat", BuiltinCat)
	r.Register("echo", BuiltinEcho)
	r.Register("pwd", BuiltinPwd)
	r.Register("cd", BuiltinCd)
	r.Register("ls", BuiltinLs)
	r.Register("find", BuiltinFind)
	r.Register("mkdir", BuiltinMkdir)
	r.Register("touch", BuiltinTouch)
	r.Register("cp", BuiltinCp)
	r.Register("rm", BuiltinRm)
	r.Register("stat", BuiltinStat)
	r.Register("tree", BuiltinTree)

	// Text processing (10)
	r.Register("grep", BuiltinGrep)
	r.Register("head", BuiltinHead)
	r.Register("tail", BuiltinTail)
	r.Register("wc", BuiltinWc)
	r.Register("sort", BuiltinSort)
	r.Register("uniq", BuiltinUniq)
	r.Register("cut", BuiltinCut)
	r.Register("tr", BuiltinTr)
	r.Register("sed", BuiltinSed)
	r.Register("tee", BuiltinTee)

	// JSON (1)
	r.Register("jq", BuiltinJq)

	// Env / assignment (2)
	r.Register("export", BuiltinExport)
	r.Register("printf", BuiltinPrintf)

	// Control (6)
	r.Register("test", BuiltinTest)
	r.Register("[", BuiltinBracket)
	r.Register("[[", BuiltinDoubleBracket)
	r.Register("true", BuiltinTrue)
	r.Register("false", BuiltinFalse)

	return r
}
