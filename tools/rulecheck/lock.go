package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// LockAnalyzer reports a call that waits on the gateway, another daemon, or a
// process while a mutex of internal/daemon or internal/gateway may be held. The
// v0.4.163 self-deadlock had this shape: it called the gateway while d.mu was
// held (B12).
//
// The check is intra-procedural. It walks each function in source order, keeps
// the set of locks that may be held, and treats a deferred Unlock as holding
// the lock until return. A helper that expects its caller to hold the lock is
// not seen, and a labelled break or continue loses the state it carries.
var LockAnalyzer = &analysis.Analyzer{
	Name: "lockcall",
	Doc:  "reports a gateway or process call while a daemon or gateway mutex is held (B12)",
	Run:  runLock,
}

// maxLoopPasses bounds the passes over a loop body. The state only grows, so
// it settles after a few passes; the cap keeps a surprise from looping.
const maxLoopPasses = 16

// inLockScope reports a package whose mutexes are checked. A sub-package such as
// internal/daemon/claude is not in scope.
func inLockScope(path string) bool {
	return path == "internal/daemon" || path == "internal/gateway" ||
		strings.HasSuffix(path, "/internal/daemon") || strings.HasSuffix(path, "/internal/gateway")
}

func runLock(pass *analysis.Pass) (any, error) {
	if !inLockScope(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name := funcName(d)
				if d.Body != nil {
					newLockFlow(pass, name).stmts(d.Body.List, lockSet{})
				}
				checkLiterals(pass, name, d)
			case *ast.GenDecl:
				// A package-level variable can hold a function literal.
				checkLiterals(pass, "package variable", d)
			}
		}
	}
	return nil, nil
}

// checkLiterals checks each function literal under node as a flow of its own,
// with no lock held at its start. The enclosing function's name labels it.
func checkLiterals(pass *analysis.Pass, enclosing string, node ast.Node) {
	ast.Inspect(node, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			newLockFlow(pass, enclosing).stmts(lit.Body.List, lockSet{})
		}
		return true
	})
}

// funcName names a function for a baseline key: Type.Method for a method.
func funcName(d *ast.FuncDecl) string {
	if d.Recv != nil && len(d.Recv.List) == 1 {
		return types.ExprString(d.Recv.List[0].Type) + "." + d.Name.Name
	}
	return d.Name.Name
}

// lockSet holds the locks that may be held at one point. A lock is named by the
// text of the receiver that locks it, such as "d.mu".
type lockSet map[string]bool

func (s lockSet) with(key string) lockSet {
	out := s.clone()
	out[key] = true
	return out
}

func (s lockSet) without(key string) lockSet {
	if !s[key] {
		return s
	}
	out := s.clone()
	delete(out, key)
	return out
}

func (s lockSet) clone() lockSet {
	out := make(lockSet, len(s)+1)
	for k := range s {
		out[k] = true
	}
	return out
}

func (s lockSet) equal(t lockSet) bool {
	if len(s) != len(t) {
		return false
	}
	for k := range s {
		if !t[k] {
			return false
		}
	}
	return true
}

func (s lockSet) names() string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// union returns the locks that may be held on any of the paths.
func union(sets ...lockSet) lockSet {
	out := lockSet{}
	for _, s := range sets {
		for k := range s {
			out[k] = true
		}
	}
	return out
}

// outcome is the lock state at the end of a statement. done means the statement
// always leaves the flow, by return, break, continue or panic, so no state
// flows on from it.
type outcome struct {
	state lockSet
	done  bool
}

func live(s lockSet) outcome {
	return outcome{state: s}
}

// merge joins the states that flow on from a branch point. The result is done
// when no branch flows on.
func merge(outs ...outcome) outcome {
	var flowing []lockSet
	for _, o := range outs {
		if !o.done {
			flowing = append(flowing, o.state)
		}
	}
	if len(flowing) == 0 {
		return outcome{state: lockSet{}, done: true}
	}
	return live(union(flowing...))
}

// jumpFrame collects the states that leave a loop, switch or select by break,
// or that go round a loop by continue.
type jumpFrame struct {
	loop     bool
	breaks   lockSet
	hasBreak bool
	conts    lockSet
}

type lockFlow struct {
	pass     *analysis.Pass
	funcName string
	frames   []*jumpFrame
	// seen keeps one report per call, since a loop body is walked more than once.
	seen map[token.Pos]bool
}

func newLockFlow(pass *analysis.Pass, funcName string) *lockFlow {
	return &lockFlow{pass: pass, funcName: funcName, seen: map[token.Pos]bool{}}
}

func (f *lockFlow) stmts(list []ast.Stmt, in lockSet) outcome {
	cur := live(in)
	for _, s := range list {
		cur = f.stmt(s, cur.state)
		if cur.done {
			return cur
		}
	}
	return cur
}

func (f *lockFlow) stmt(s ast.Stmt, in lockSet) outcome {
	switch s := s.(type) {
	case nil:
		return live(in)
	case *ast.BlockStmt:
		return f.stmts(s.List, in)
	case *ast.LabeledStmt:
		return f.stmt(s.Stmt, in)
	case *ast.ExprStmt:
		return outcome{state: f.expr(s.X, in), done: isPanic(f.pass.TypesInfo, s.X)}
	case *ast.AssignStmt:
		return live(f.exprs(in, s.Rhs...))
	case *ast.DeclStmt:
		return live(f.declExprs(in, s.Decl))
	case *ast.SendStmt:
		return live(f.exprs(in, s.Chan, s.Value))
	case *ast.IncDecStmt:
		return live(f.expr(s.X, in))
	case *ast.ReturnStmt:
		return outcome{state: f.exprs(in, s.Results...), done: true}
	case *ast.BranchStmt:
		f.jump(s, in)
		return outcome{state: in, done: true}
	case *ast.GoStmt:
		// The call runs on another goroutine, so this flow's locks are not held
		// there. Its arguments are evaluated here.
		return live(f.exprs(in, s.Call.Args...))
	case *ast.DeferStmt:
		// The call runs at return. A deferred Unlock keeps its lock held until
		// then, so the state does not change here. Its arguments are evaluated
		// here.
		return live(f.exprs(in, s.Call.Args...))
	case *ast.IfStmt:
		cur := f.stmt(s.Init, in).state
		cur = f.expr(s.Cond, cur)
		then := f.stmt(s.Body, cur)
		els := live(cur)
		if s.Else != nil {
			els = f.stmt(s.Else, cur)
		}
		return merge(then, els)
	case *ast.ForStmt:
		entry := f.stmt(s.Init, in).state
		return f.loop(entry, s.Cond, s.Post, s.Body, s.Cond != nil)
	case *ast.RangeStmt:
		entry := f.expr(s.X, in)
		return f.loop(entry, nil, nil, s.Body, true)
	case *ast.SwitchStmt:
		cur := f.stmt(s.Init, in).state
		cur = f.expr(s.Tag, cur)
		return f.clauses(s.Body, cur, false)
	case *ast.TypeSwitchStmt:
		cur := f.stmt(s.Init, in).state
		cur = f.stmt(s.Assign, cur).state
		return f.clauses(s.Body, cur, false)
	case *ast.SelectStmt:
		// A select always runs one of its clauses.
		return f.clauses(s.Body, in, true)
	}
	return live(in)
}

// loop walks a for or range statement. The body can run again from its own end,
// so the state at its top is the union of the entry state and the states that
// come back round. A loop with no condition leaves only by break.
func (f *lockFlow) loop(entry lockSet, cond ast.Expr, post ast.Stmt, body *ast.BlockStmt, mayExit bool) outcome {
	head := entry
	var frame *jumpFrame
	for pass := 0; pass < maxLoopPasses; pass++ {
		frame = &jumpFrame{loop: true, breaks: lockSet{}, conts: lockSet{}}
		f.frames = append(f.frames, frame)
		top := f.expr(cond, head)
		end := f.stmts(body.List, top)
		f.frames = f.frames[:len(f.frames)-1]

		back := frame.conts
		if !end.done {
			back = union(back, end.state)
		}
		back = f.stmt(post, back).state
		next := union(entry, back)
		if next.equal(head) {
			break
		}
		head = next
	}

	var outs []outcome
	if mayExit {
		outs = append(outs, live(head))
	}
	if frame.hasBreak {
		outs = append(outs, live(frame.breaks))
	}
	return merge(outs...)
}

// clauses walks the clauses of a switch, type switch or select. Each clause
// starts from the state before the statement. A switch with no default may
// match none of its clauses, so the state before it flows on too.
func (f *lockFlow) clauses(body *ast.BlockStmt, in lockSet, exhaustive bool) outcome {
	frame := &jumpFrame{breaks: lockSet{}}
	f.frames = append(f.frames, frame)
	var outs []outcome
	for _, stmt := range body.List {
		start := in
		var list []ast.Stmt
		switch c := stmt.(type) {
		case *ast.CaseClause:
			if c.List == nil {
				exhaustive = true
			}
			start = f.exprs(start, c.List...)
			list = c.Body
		case *ast.CommClause:
			if c.Comm == nil {
				exhaustive = true
			}
			start = f.stmt(c.Comm, start).state
			list = c.Body
		}
		outs = append(outs, f.stmts(list, start))
	}
	f.frames = f.frames[:len(f.frames)-1]
	if !exhaustive {
		outs = append(outs, live(in))
	}
	if frame.hasBreak {
		outs = append(outs, live(frame.breaks))
	}
	return merge(outs...)
}

// jump records the state that leaves a loop or switch by break, or that goes
// round a loop by continue. A labelled jump is not followed.
func (f *lockFlow) jump(s *ast.BranchStmt, in lockSet) {
	if s.Label != nil || len(f.frames) == 0 {
		return
	}
	switch s.Tok {
	case token.BREAK:
		top := f.frames[len(f.frames)-1]
		top.breaks = union(top.breaks, in)
		top.hasBreak = true
	case token.CONTINUE:
		for i := len(f.frames) - 1; i >= 0; i-- {
			if f.frames[i].loop {
				f.frames[i].conts = union(f.frames[i].conts, in)
				return
			}
		}
	}
}

func (f *lockFlow) exprs(in lockSet, list ...ast.Expr) lockSet {
	for _, e := range list {
		in = f.expr(e, in)
	}
	return in
}

func (f *lockFlow) declExprs(in lockSet, decl ast.Decl) lockSet {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return in
	}
	for _, spec := range gen.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok {
			in = f.exprs(in, vs.Values...)
		}
	}
	return in
}

// expr applies the calls in e to the lock state, in source order. A function
// literal is not entered: it is checked as a function of its own.
func (f *lockFlow) expr(e ast.Expr, in lockSet) lockSet {
	if e == nil {
		return in
	}
	cur := in
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			cur = f.call(n, cur)
			return false
		}
		return true
	})
	return cur
}

// call applies one call. Its function value and arguments run first. A Lock or
// Unlock changes the state. Any other call that waits on the gateway or a
// process is reported when a lock may be held.
func (f *lockFlow) call(call *ast.CallExpr, in lockSet) lockSet {
	cur := in
	if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
		cur = f.expr(sel.X, cur)
	} else {
		cur = f.expr(call.Fun, cur)
	}
	cur = f.exprs(cur, call.Args...)

	fn := calledFunc(f.pass.TypesInfo, call)
	if fn == nil {
		return cur
	}
	if key, locks, ok := mutexOp(call, fn); ok {
		if locks {
			return cur.with(key)
		}
		return cur.without(key)
	}
	if len(cur) > 0 && waitsOnOther(fn) && !f.seen[call.Pos()] {
		f.seen[call.Pos()] = true
		callee := types.ExprString(call.Fun)
		f.pass.Report(analysis.Diagnostic{
			Pos:      call.Pos(),
			Category: f.siteKey(call.Pos(), callee, cur),
			Message:  fmt.Sprintf("call to %s while %s is held (B12): unlock first; a call to the gateway or another process must not run under a lock", callee, cur.names()),
		})
	}
	return cur
}

// siteKey names a lock-across-call site for lockcall.baseline. It leaves out the
// line, so an edit above the site does not change its key.
func (f *lockFlow) siteKey(pos token.Pos, callee string, held lockSet) string {
	file := filepath.Base(f.pass.Fset.Position(pos).Filename)
	return fmt.Sprintf("%s/%s %s %s while %s", f.pass.Pkg.Path(), file, f.funcName, callee, held.names())
}

// mutexOp reports a Lock or Unlock call on a sync.Mutex or sync.RWMutex, and the
// receiver text that names the lock.
func mutexOp(call *ast.CallExpr, fn *types.Func) (key string, locks, ok bool) {
	if fn.Pkg() == nil || fn.Pkg().Path() != "sync" {
		return "", false, false
	}
	if t := receiverTypeName(fn); t != "Mutex" && t != "RWMutex" {
		return "", false, false
	}
	switch fn.Name() {
	case "Lock", "RLock":
		locks = true
	case "Unlock", "RUnlock":
	default:
		return "", false, false
	}
	sel, isSel := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !isSel {
		return "", false, false
	}
	return types.ExprString(sel.X), locks, true
}

// waitsOnOther reports a function that waits on the gateway, another daemon, or
// a process. These are the calls B12 keeps out of a locked section.
func waitsOnOther(fn *types.Func) bool {
	pkg := fn.Pkg()
	if pkg == nil {
		return false
	}
	name := fn.Name()
	recv := receiverTypeName(fn)
	switch pkg.Path() {
	case "os/exec":
		if recv == "" {
			return name == "Command" || name == "CommandContext"
		}
		return recv == "Cmd" && (name == "Run" || name == "Start" || name == "Output" || name == "CombinedOutput" || name == "Wait")
	case "net/http":
		if recv == "" {
			return name == "Get" || name == "Head" || name == "Post" || name == "PostForm"
		}
		return recv == "Client" && (name == "Do" || name == "Get" || name == "Head" || name == "Post" || name == "PostForm")
	}
	// The daemon's requests to the gateway: Daemon.call and Daemon.send, and the
	// link's call over the open connection. Both are named call or send.
	return inLockScope(pkg.Path()) && (name == "call" || name == "send")
}

// isPanic reports a call to the panic builtin, which never returns.
func isPanic(info *types.Info, e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok {
		return false
	}
	_, builtin := info.Uses[id].(*types.Builtin)
	return builtin && id.Name == "panic"
}
