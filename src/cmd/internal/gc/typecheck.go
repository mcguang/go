// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gc

import (
	"cmd/internal/obj"
	"fmt"
	"math"
	"strings"
)

/*
 * type check the whole tree of an expression.
 * calculates expression types.
 * evaluates compile time constants.
 * marks variables that escape the local frame.
 * rewrites n->op to be more specific in some cases.
 */
var typecheckdefstack *NodeList

/*
 * resolve ONONAME to definition, if any.
 */
func resolve(n *Node) *Node {
	var r *Node

	if n != nil && n.Op == ONONAME && n.Sym != nil {
		r = n.Sym.Def
		if r != nil {
			if r.Op != OIOTA {
				n = r
			} else if n.Iota >= 0 {
				n = Nodintconst(int64(n.Iota))
			}
		}
	}

	return n
}

func typechecklist(l *NodeList, top int) {
	for ; l != nil; l = l.Next {
		typecheck(&l.N, top)
	}
}

var _typekind = []string{
	TINT:        "int",
	TUINT:       "uint",
	TINT8:       "int8",
	TUINT8:      "uint8",
	TINT16:      "int16",
	TUINT16:     "uint16",
	TINT32:      "int32",
	TUINT32:     "uint32",
	TINT64:      "int64",
	TUINT64:     "uint64",
	TUINTPTR:    "uintptr",
	TCOMPLEX64:  "complex64",
	TCOMPLEX128: "complex128",
	TFLOAT32:    "float32",
	TFLOAT64:    "float64",
	TBOOL:       "bool",
	TSTRING:     "string",
	TPTR32:      "pointer",
	TPTR64:      "pointer",
	TUNSAFEPTR:  "unsafe.Pointer",
	TSTRUCT:     "struct",
	TINTER:      "interface",
	TCHAN:       "chan",
	TMAP:        "map",
	TARRAY:      "array",
	TFUNC:       "func",
	TNIL:        "nil",
	TIDEAL:      "untyped number",
}

var typekind_buf string

func typekind(t *Type) string {
	var et int
	var s string

	if Isslice(t) != 0 {
		return "slice"
	}
	et = int(t.Etype)
	if 0 <= et && et < len(_typekind) {
		s = _typekind[et]
		if s != "" {
			return s
		}
	}
	typekind_buf = fmt.Sprintf("etype=%d", et)
	return typekind_buf
}

/*
 * sprint_depchain prints a dependency chain
 * of nodes into fmt.
 * It is used by typecheck in the case of OLITERAL nodes
 * to print constant definition loops.
 */
func sprint_depchain(fmt_ *string, stack *NodeList, cur *Node, first *Node) {
	var l *NodeList

	for l = stack; l != nil; l = l.Next {
		if l.N.Op == cur.Op {
			if l.N != first {
				sprint_depchain(fmt_, l.Next, l.N, first)
			}
			*fmt_ += fmt.Sprintf("\n\t%v: %v uses %v", l.N.Line(), Nconv(l.N, 0), Nconv(cur, 0))
			return
		}
	}
}

/*
 * type check node *np.
 * replaces *np with a new pointer in some cases.
 * returns the final value of *np as a convenience.
 */

var typecheck_tcstack *NodeList
var typecheck_tcfree *NodeList

func typecheck(np **Node, top int) *Node {
	var n *Node
	var lno int
	var fmt_ string
	var l *NodeList

	// cannot type check until all the source has been parsed
	if !(typecheckok != 0) {
		Fatal("early typecheck")
	}

	n = *np
	if n == nil {
		return nil
	}

	lno = int(setlineno(n))

	// Skip over parens.
	for n.Op == OPAREN {
		n = n.Left
	}

	// Resolve definition of name and value of iota lazily.
	n = resolve(n)

	*np = n

	// Skip typecheck if already done.
	// But re-typecheck ONAME/OTYPE/OLITERAL/OPACK node in case context has changed.
	if n.Typecheck == 1 {
		switch n.Op {
		case ONAME,
			OTYPE,
			OLITERAL,
			OPACK:
			break

		default:
			lineno = int32(lno)
			return n
		}
	}

	if n.Typecheck == 2 {
		// Typechecking loop. Trying printing a meaningful message,
		// otherwise a stack trace of typechecking.
		switch n.Op {
		// We can already diagnose variables used as types.
		case ONAME:
			if top&(Erv|Etype) == Etype {
				Yyerror("%v is not a type", Nconv(n, 0))
			}

		case OLITERAL:
			if top&(Erv|Etype) == Etype {
				Yyerror("%v is not a type", Nconv(n, 0))
				break
			}

			fmt_ = ""
			sprint_depchain(&fmt_, typecheck_tcstack, n, n)
			yyerrorl(int(n.Lineno), "constant definition loop%s", fmt_)
		}

		if nsavederrors+nerrors == 0 {
			fmt_ = ""
			for l = typecheck_tcstack; l != nil; l = l.Next {
				fmt_ += fmt.Sprintf("\n\t%v %v", l.N.Line(), Nconv(l.N, 0))
			}
			Yyerror("typechecking loop involving %v%s", Nconv(n, 0), fmt_)
		}

		lineno = int32(lno)
		return n
	}

	n.Typecheck = 2

	if typecheck_tcfree != nil {
		l = typecheck_tcfree
		typecheck_tcfree = l.Next
	} else {
		l = new(NodeList)
	}
	l.Next = typecheck_tcstack
	l.N = n
	typecheck_tcstack = l

	typecheck1(&n, top)
	*np = n
	n.Typecheck = 1

	if typecheck_tcstack != l {
		Fatal("typecheck stack out of sync")
	}
	typecheck_tcstack = l.Next
	l.Next = typecheck_tcfree
	typecheck_tcfree = l

	lineno = int32(lno)
	return n
}

/*
 * does n contain a call or receive operation?
 */
func callrecv(n *Node) int {
	if n == nil {
		return 0
	}

	switch n.Op {
	case OCALL,
		OCALLMETH,
		OCALLINTER,
		OCALLFUNC,
		ORECV,
		OCAP,
		OLEN,
		OCOPY,
		ONEW,
		OAPPEND,
		ODELETE:
		return 1
	}

	return bool2int(callrecv(n.Left) != 0 || callrecv(n.Right) != 0 || callrecv(n.Ntest) != 0 || callrecv(n.Nincr) != 0 || callrecvlist(n.Ninit) != 0 || callrecvlist(n.Nbody) != 0 || callrecvlist(n.Nelse) != 0 || callrecvlist(n.List) != 0 || callrecvlist(n.Rlist) != 0)
}

func callrecvlist(l *NodeList) int {
	for ; l != nil; l = l.Next {
		if callrecv(l.N) != 0 {
			return 1
		}
	}
	return 0
}

// indexlit implements typechecking of untyped values as
// array/slice indexes. It is equivalent to defaultlit
// except for constants of numerical kind, which are acceptable
// whenever they can be represented by a value of type int.
func indexlit(np **Node) {
	var n *Node

	n = *np
	if n == nil || !(isideal(n.Type) != 0) {
		return
	}
	switch consttype(n) {
	case CTINT,
		CTRUNE,
		CTFLT,
		CTCPLX:
		defaultlit(np, Types[TINT])
	}

	defaultlit(np, nil)
}

func typecheck1(np **Node, top int) {
	var et int
	var aop int
	var op int
	var ptr int
	var n *Node
	var l *Node
	var r *Node
	var lo *Node
	var mid *Node
	var hi *Node
	var args *NodeList
	var ok int
	var ntop int
	var t *Type
	var tp *Type
	var missing *Type
	var have *Type
	var badtype *Type
	var v Val
	var why string
	var desc string
	var descbuf string
	var x int64

	n = *np

	if n.Sym != nil {
		if n.Op == ONAME && n.Etype != 0 && !(top&Ecall != 0) {
			Yyerror("use of builtin %v not in function call", Sconv(n.Sym, 0))
			goto error
		}

		typecheckdef(n)
		if n.Op == ONONAME {
			goto error
		}
	}

	*np = n

reswitch:
	ok = 0
	switch n.Op {
	// until typecheck is complete, do nothing.
	default:
		Dump("typecheck", n)

		Fatal("typecheck %v", Oconv(int(n.Op), 0))
		fallthrough

		/*
		 * names
		 */
	case OLITERAL:
		ok |= Erv

		if n.Type == nil && n.Val.Ctype == CTSTR {
			n.Type = idealstring
		}
		goto ret

	case ONONAME:
		ok |= Erv
		goto ret

	case ONAME:
		if n.Decldepth == 0 {
			n.Decldepth = decldepth
		}
		if n.Etype != 0 {
			ok |= Ecall
			goto ret
		}

		if !(top&Easgn != 0) {
			// not a write to the variable
			if isblank(n) {
				Yyerror("cannot use _ as value")
				goto error
			}

			n.Used = 1
		}

		if !(top&Ecall != 0) && isunsafebuiltin(n) != 0 {
			Yyerror("%v is not an expression, must be called", Nconv(n, 0))
			goto error
		}

		ok |= Erv
		goto ret

	case OPACK:
		Yyerror("use of package %v without selector", Sconv(n.Sym, 0))
		goto error

	case ODDD:
		break

		/*
		 * types (OIND is with exprs)
		 */
	case OTYPE:
		ok |= Etype

		if n.Type == nil {
			goto error
		}

	case OTARRAY:
		ok |= Etype
		t = typ(TARRAY)
		l = n.Left
		r = n.Right
		if l == nil {
			t.Bound = -1 // slice
		} else if l.Op == ODDD {
			t.Bound = -100 // to be filled in
			if !(top&Ecomplit != 0) && !(n.Diag != 0) {
				t.Broke = 1
				n.Diag = 1
				Yyerror("use of [...] array outside of array literal")
			}
		} else {
			l = typecheck(&n.Left, Erv)
			switch consttype(l) {
			case CTINT,
				CTRUNE:
				v = l.Val

			case CTFLT:
				v = toint(l.Val)

			default:
				if l.Type != nil && Isint[l.Type.Etype] != 0 && l.Op != OLITERAL {
					Yyerror("non-constant array bound %v", Nconv(l, 0))
				} else {
					Yyerror("invalid array bound %v", Nconv(l, 0))
				}
				goto error
			}

			t.Bound = Mpgetfix(v.U.Xval)
			if doesoverflow(v, Types[TINT]) != 0 {
				Yyerror("array bound is too large")
				goto error
			} else if t.Bound < 0 {
				Yyerror("array bound must be non-negative")
				goto error
			}
		}

		typecheck(&r, Etype)
		if r.Type == nil {
			goto error
		}
		t.Type = r.Type
		n.Op = OTYPE
		n.Type = t
		n.Left = nil
		n.Right = nil
		if t.Bound != -100 {
			checkwidth(t)
		}

	case OTMAP:
		ok |= Etype
		l = typecheck(&n.Left, Etype)
		r = typecheck(&n.Right, Etype)
		if l.Type == nil || r.Type == nil {
			goto error
		}
		n.Op = OTYPE
		n.Type = maptype(l.Type, r.Type)
		n.Left = nil
		n.Right = nil

	case OTCHAN:
		ok |= Etype
		l = typecheck(&n.Left, Etype)
		if l.Type == nil {
			goto error
		}
		t = typ(TCHAN)
		t.Type = l.Type
		t.Chan = n.Etype
		n.Op = OTYPE
		n.Type = t
		n.Left = nil
		n.Etype = 0

	case OTSTRUCT:
		ok |= Etype
		n.Op = OTYPE
		n.Type = tostruct(n.List)
		if n.Type == nil || n.Type.Broke != 0 {
			goto error
		}
		n.List = nil

	case OTINTER:
		ok |= Etype
		n.Op = OTYPE
		n.Type = tointerface(n.List)
		if n.Type == nil {
			goto error
		}

	case OTFUNC:
		ok |= Etype
		n.Op = OTYPE
		n.Type = functype(n.Left, n.List, n.Rlist)
		if n.Type == nil {
			goto error
		}

		/*
		 * type or expr
		 */
	case OIND:
		ntop = Erv | Etype

		if !(top&Eaddr != 0) { // The *x in &*x is not an indirect.
			ntop |= Eindir
		}
		ntop |= top & Ecomplit
		l = typecheck(&n.Left, ntop)
		t = l.Type
		if t == nil {
			goto error
		}
		if l.Op == OTYPE {
			ok |= Etype
			n.Op = OTYPE
			n.Type = Ptrto(l.Type)
			n.Left = nil
			goto ret
		}

		if !(Isptr[t.Etype] != 0) {
			if top&(Erv|Etop) != 0 {
				Yyerror("invalid indirect of %v", Nconv(n.Left, obj.FmtLong))
				goto error
			}

			goto ret
		}

		ok |= Erv
		n.Type = t.Type
		goto ret

		/*
		 * arithmetic exprs
		 */
	case OASOP:
		ok |= Etop

		l = typecheck(&n.Left, Erv)
		r = typecheck(&n.Right, Erv)
		checkassign(n, n.Left)
		if l.Type == nil || r.Type == nil {
			goto error
		}
		op = int(n.Etype)
		goto arith

	case OADD,
		OAND,
		OANDAND,
		OANDNOT,
		ODIV,
		OEQ,
		OGE,
		OGT,
		OLE,
		OLT,
		OLSH,
		ORSH,
		OMOD,
		OMUL,
		ONE,
		OOR,
		OOROR,
		OSUB,
		OXOR:
		ok |= Erv
		l = typecheck(&n.Left, Erv|top&Eiota)
		r = typecheck(&n.Right, Erv|top&Eiota)
		if l.Type == nil || r.Type == nil {
			goto error
		}
		op = int(n.Op)
		goto arith

	case OCOM,
		OMINUS,
		ONOT,
		OPLUS:
		ok |= Erv
		l = typecheck(&n.Left, Erv|top&Eiota)
		t = l.Type
		if t == nil {
			goto error
		}
		if !(okfor[n.Op][t.Etype] != 0) {
			Yyerror("invalid operation: %v %v", Oconv(int(n.Op), 0), Tconv(t, 0))
			goto error
		}

		n.Type = t
		goto ret

		/*
		 * exprs
		 */
	case OADDR:
		ok |= Erv

		typecheck(&n.Left, Erv|Eaddr)
		if n.Left.Type == nil {
			goto error
		}
		checklvalue(n.Left, "take the address of")
		r = outervalue(n.Left)
		for l = n.Left; l != r; l = l.Left {
			l.Addrtaken = 1
			if l.Closure != nil {
				l.Closure.Addrtaken = 1
			}
		}

		if l.Orig != l && l.Op == ONAME {
			Fatal("found non-orig name node %v", Nconv(l, 0))
		}
		l.Addrtaken = 1
		if l.Closure != nil {
			l.Closure.Addrtaken = 1
		}
		defaultlit(&n.Left, nil)
		l = n.Left
		t = l.Type
		if t == nil {
			goto error
		}
		n.Type = Ptrto(t)
		goto ret

	case OCOMPLIT:
		ok |= Erv
		typecheckcomplit(&n)
		if n.Type == nil {
			goto error
		}
		goto ret

	case OXDOT:
		n = adddot(n)
		n.Op = ODOT
		if n.Left == nil {
			goto error
		}
		fallthrough

		// fall through
	case ODOT:
		typecheck(&n.Left, Erv|Etype)

		defaultlit(&n.Left, nil)
		if n.Right.Op != ONAME {
			Yyerror("rhs of . must be a name") // impossible
			goto error
		}

		t = n.Left.Type
		if t == nil {
			adderrorname(n)
			goto error
		}

		r = n.Right

		if n.Left.Op == OTYPE {
			if !(looktypedot(n, t, 0) != 0) {
				if looktypedot(n, t, 1) != 0 {
					Yyerror("%v undefined (cannot refer to unexported method %v)", Nconv(n, 0), Sconv(n.Right.Sym, 0))
				} else {
					Yyerror("%v undefined (type %v has no method %v)", Nconv(n, 0), Tconv(t, 0), Sconv(n.Right.Sym, 0))
				}
				goto error
			}

			if n.Type.Etype != TFUNC || n.Type.Thistuple != 1 {
				Yyerror("type %v has no method %v", Tconv(n.Left.Type, 0), Sconv(n.Right.Sym, obj.FmtShort))
				n.Type = nil
				goto error
			}

			n.Op = ONAME
			n.Sym = n.Right.Sym
			n.Type = methodfunc(n.Type, n.Left.Type)
			n.Xoffset = 0
			n.Class = PFUNC
			ok = Erv
			goto ret
		}

		if Isptr[t.Etype] != 0 && t.Type.Etype != TINTER {
			t = t.Type
			if t == nil {
				goto error
			}
			n.Op = ODOTPTR
			checkwidth(t)
		}

		if isblank(n.Right) {
			Yyerror("cannot refer to blank field or method")
			goto error
		}

		if !(lookdot(n, t, 0) != 0) {
			if lookdot(n, t, 1) != 0 {
				Yyerror("%v undefined (cannot refer to unexported field or method %v)", Nconv(n, 0), Sconv(n.Right.Sym, 0))
			} else {
				Yyerror("%v undefined (type %v has no field or method %v)", Nconv(n, 0), Tconv(n.Left.Type, 0), Sconv(n.Right.Sym, 0))
			}
			goto error
		}

		switch n.Op {
		case ODOTINTER,
			ODOTMETH:
			if top&Ecall != 0 {
				ok |= Ecall
			} else {
				typecheckpartialcall(n, r)
				ok |= Erv
			}

		default:
			ok |= Erv
		}

		goto ret

	case ODOTTYPE:
		ok |= Erv
		typecheck(&n.Left, Erv)
		defaultlit(&n.Left, nil)
		l = n.Left
		t = l.Type
		if t == nil {
			goto error
		}
		if !(Isinter(t) != 0) {
			Yyerror("invalid type assertion: %v (non-interface type %v on left)", Nconv(n, 0), Tconv(t, 0))
			goto error
		}

		if n.Right != nil {
			typecheck(&n.Right, Etype)
			n.Type = n.Right.Type
			n.Right = nil
			if n.Type == nil {
				goto error
			}
		}

		if n.Type != nil && n.Type.Etype != TINTER {
			if !(implements(n.Type, t, &missing, &have, &ptr) != 0) {
				if have != nil && have.Sym == missing.Sym {
					Yyerror("impossible type assertion:\n\t%v does not implement %v (wrong type for %v method)\n"+"\t\thave %v%v\n\t\twant %v%v", Tconv(n.Type, 0), Tconv(t, 0), Sconv(missing.Sym, 0), Sconv(have.Sym, 0), Tconv(have.Type, obj.FmtShort|obj.FmtByte), Sconv(missing.Sym, 0), Tconv(missing.Type, obj.FmtShort|obj.FmtByte))
				} else if ptr != 0 {
					Yyerror("impossible type assertion:\n\t%v does not implement %v (%v method has pointer receiver)", Tconv(n.Type, 0), Tconv(t, 0), Sconv(missing.Sym, 0))
				} else if have != nil {
					Yyerror("impossible type assertion:\n\t%v does not implement %v (missing %v method)\n"+"\t\thave %v%v\n\t\twant %v%v", Tconv(n.Type, 0), Tconv(t, 0), Sconv(missing.Sym, 0), Sconv(have.Sym, 0), Tconv(have.Type, obj.FmtShort|obj.FmtByte), Sconv(missing.Sym, 0), Tconv(missing.Type, obj.FmtShort|obj.FmtByte))
				} else {
					Yyerror("impossible type assertion:\n\t%v does not implement %v (missing %v method)", Tconv(n.Type, 0), Tconv(t, 0), Sconv(missing.Sym, 0))
				}
				goto error
			}
		}

		goto ret

	case OINDEX:
		ok |= Erv
		typecheck(&n.Left, Erv)
		defaultlit(&n.Left, nil)
		implicitstar(&n.Left)
		l = n.Left
		typecheck(&n.Right, Erv)
		r = n.Right
		t = l.Type
		if t == nil || r.Type == nil {
			goto error
		}
		switch t.Etype {
		default:
			Yyerror("invalid operation: %v (type %v does not support indexing)", Nconv(n, 0), Tconv(t, 0))
			goto error

		case TSTRING,
			TARRAY:
			indexlit(&n.Right)
			if t.Etype == TSTRING {
				n.Type = Types[TUINT8]
			} else {
				n.Type = t.Type
			}
			why = "string"
			if t.Etype == TARRAY {
				if Isfixedarray(t) != 0 {
					why = "array"
				} else {
					why = "slice"
				}
			}

			if n.Right.Type != nil && !(Isint[n.Right.Type.Etype] != 0) {
				Yyerror("non-integer %s index %v", why, Nconv(n.Right, 0))
				break
			}

			if Isconst(n.Right, CTINT) != 0 {
				x = Mpgetfix(n.Right.Val.U.Xval)
				if x < 0 {
					Yyerror("invalid %s index %v (index must be non-negative)", why, Nconv(n.Right, 0))
				} else if Isfixedarray(t) != 0 && t.Bound > 0 && x >= t.Bound {
					Yyerror("invalid array index %v (out of bounds for %d-element array)", Nconv(n.Right, 0), t.Bound)
				} else if Isconst(n.Left, CTSTR) != 0 && x >= int64(len(n.Left.Val.U.Sval.S)) {
					Yyerror("invalid string index %v (out of bounds for %d-byte string)", Nconv(n.Right, 0), len(n.Left.Val.U.Sval.S))
				} else if Mpcmpfixfix(n.Right.Val.U.Xval, Maxintval[TINT]) > 0 {
					Yyerror("invalid %s index %v (index too large)", why, Nconv(n.Right, 0))
				}
			}

		case TMAP:
			n.Etype = 0
			defaultlit(&n.Right, t.Down)
			if n.Right.Type != nil {
				n.Right = assignconv(n.Right, t.Down, "map index")
			}
			n.Type = t.Type
			n.Op = OINDEXMAP
		}

		goto ret

	case ORECV:
		ok |= Etop | Erv
		typecheck(&n.Left, Erv)
		defaultlit(&n.Left, nil)
		l = n.Left
		t = l.Type
		if t == nil {
			goto error
		}
		if t.Etype != TCHAN {
			Yyerror("invalid operation: %v (receive from non-chan type %v)", Nconv(n, 0), Tconv(t, 0))
			goto error
		}

		if !(t.Chan&Crecv != 0) {
			Yyerror("invalid operation: %v (receive from send-only type %v)", Nconv(n, 0), Tconv(t, 0))
			goto error
		}

		n.Type = t.Type
		goto ret

	case OSEND:
		ok |= Etop
		l = typecheck(&n.Left, Erv)
		typecheck(&n.Right, Erv)
		defaultlit(&n.Left, nil)
		l = n.Left
		t = l.Type
		if t == nil {
			goto error
		}
		if t.Etype != TCHAN {
			Yyerror("invalid operation: %v (send to non-chan type %v)", Nconv(n, 0), Tconv(t, 0))
			goto error
		}

		if !(t.Chan&Csend != 0) {
			Yyerror("invalid operation: %v (send to receive-only type %v)", Nconv(n, 0), Tconv(t, 0))
			goto error
		}

		defaultlit(&n.Right, t.Type)
		r = n.Right
		if r.Type == nil {
			goto error
		}
		n.Right = assignconv(r, l.Type.Type, "send")

		// TODO: more aggressive
		n.Etype = 0

		n.Type = nil
		goto ret

	case OSLICE:
		ok |= Erv
		typecheck(&n.Left, top)
		typecheck(&n.Right.Left, Erv)
		typecheck(&n.Right.Right, Erv)
		defaultlit(&n.Left, nil)
		indexlit(&n.Right.Left)
		indexlit(&n.Right.Right)
		l = n.Left
		if Isfixedarray(l.Type) != 0 {
			if !(islvalue(n.Left) != 0) {
				Yyerror("invalid operation %v (slice of unaddressable value)", Nconv(n, 0))
				goto error
			}

			n.Left = Nod(OADDR, n.Left, nil)
			n.Left.Implicit = 1
			typecheck(&n.Left, Erv)
			l = n.Left
		}

		t = l.Type
		if t == nil {
			goto error
		}
		tp = nil
		if Istype(t, TSTRING) != 0 {
			n.Type = t
			n.Op = OSLICESTR
		} else if Isptr[t.Etype] != 0 && Isfixedarray(t.Type) != 0 {
			tp = t.Type
			n.Type = typ(TARRAY)
			n.Type.Type = tp.Type
			n.Type.Bound = -1
			dowidth(n.Type)
			n.Op = OSLICEARR
		} else if Isslice(t) != 0 {
			n.Type = t
		} else {
			Yyerror("cannot slice %v (type %v)", Nconv(l, 0), Tconv(t, 0))
			goto error
		}

		lo = n.Right.Left
		if lo != nil && checksliceindex(l, lo, tp) < 0 {
			goto error
		}
		hi = n.Right.Right
		if hi != nil && checksliceindex(l, hi, tp) < 0 {
			goto error
		}
		if checksliceconst(lo, hi) < 0 {
			goto error
		}
		goto ret

	case OSLICE3:
		ok |= Erv
		typecheck(&n.Left, top)
		typecheck(&n.Right.Left, Erv)
		typecheck(&n.Right.Right.Left, Erv)
		typecheck(&n.Right.Right.Right, Erv)
		defaultlit(&n.Left, nil)
		indexlit(&n.Right.Left)
		indexlit(&n.Right.Right.Left)
		indexlit(&n.Right.Right.Right)
		l = n.Left
		if Isfixedarray(l.Type) != 0 {
			if !(islvalue(n.Left) != 0) {
				Yyerror("invalid operation %v (slice of unaddressable value)", Nconv(n, 0))
				goto error
			}

			n.Left = Nod(OADDR, n.Left, nil)
			n.Left.Implicit = 1
			typecheck(&n.Left, Erv)
			l = n.Left
		}

		t = l.Type
		if t == nil {
			goto error
		}
		tp = nil
		if Istype(t, TSTRING) != 0 {
			Yyerror("invalid operation %v (3-index slice of string)", Nconv(n, 0))
			goto error
		}

		if Isptr[t.Etype] != 0 && Isfixedarray(t.Type) != 0 {
			tp = t.Type
			n.Type = typ(TARRAY)
			n.Type.Type = tp.Type
			n.Type.Bound = -1
			dowidth(n.Type)
			n.Op = OSLICE3ARR
		} else if Isslice(t) != 0 {
			n.Type = t
		} else {
			Yyerror("cannot slice %v (type %v)", Nconv(l, 0), Tconv(t, 0))
			goto error
		}

		lo = n.Right.Left
		if lo != nil && checksliceindex(l, lo, tp) < 0 {
			goto error
		}
		mid = n.Right.Right.Left
		if mid != nil && checksliceindex(l, mid, tp) < 0 {
			goto error
		}
		hi = n.Right.Right.Right
		if hi != nil && checksliceindex(l, hi, tp) < 0 {
			goto error
		}
		if checksliceconst(lo, hi) < 0 || checksliceconst(lo, mid) < 0 || checksliceconst(mid, hi) < 0 {
			goto error
		}
		goto ret

		/*
		 * call and call like
		 */
	case OCALL:
		l = n.Left

		if l.Op == ONAME {
			r = unsafenmagic(n)
			if r != nil {
				if n.Isddd != 0 {
					Yyerror("invalid use of ... with builtin %v", Nconv(l, 0))
				}
				n = r
				goto reswitch
			}
		}

		typecheck(&n.Left, Erv|Etype|Ecall|top&Eproc)
		n.Diag |= n.Left.Diag
		l = n.Left
		if l.Op == ONAME && l.Etype != 0 {
			if n.Isddd != 0 && l.Etype != OAPPEND {
				Yyerror("invalid use of ... with builtin %v", Nconv(l, 0))
			}

			// builtin: OLEN, OCAP, etc.
			n.Op = l.Etype

			n.Left = n.Right
			n.Right = nil
			goto reswitch
		}

		defaultlit(&n.Left, nil)
		l = n.Left
		if l.Op == OTYPE {
			if n.Isddd != 0 || l.Type.Bound == -100 {
				if !(l.Type.Broke != 0) {
					Yyerror("invalid use of ... in type conversion", l)
				}
				n.Diag = 1
			}

			// pick off before type-checking arguments
			ok |= Erv

			// turn CALL(type, arg) into CONV(arg) w/ type
			n.Left = nil

			n.Op = OCONV
			n.Type = l.Type
			if onearg(n, "conversion to %v", Tconv(l.Type, 0)) < 0 {
				goto error
			}
			goto doconv
		}

		if count(n.List) == 1 && !(n.Isddd != 0) {
			typecheck(&n.List.N, Erv|Efnstruct)
		} else {
			typechecklist(n.List, Erv)
		}
		t = l.Type
		if t == nil {
			goto error
		}
		checkwidth(t)

		switch l.Op {
		case ODOTINTER:
			n.Op = OCALLINTER

		case ODOTMETH:
			n.Op = OCALLMETH

			// typecheckaste was used here but there wasn't enough
			// information further down the call chain to know if we
			// were testing a method receiver for unexported fields.
			// It isn't necessary, so just do a sanity check.
			tp = getthisx(t).Type.Type

			if l.Left == nil || !Eqtype(l.Left.Type, tp) {
				Fatal("method receiver")
			}

		default:
			n.Op = OCALLFUNC
			if t.Etype != TFUNC {
				Yyerror("cannot call non-function %v (type %v)", Nconv(l, 0), Tconv(t, 0))
				goto error
			}
		}

		descbuf = fmt.Sprintf("argument to %v", Nconv(n.Left, 0))
		desc = descbuf
		typecheckaste(OCALL, n.Left, int(n.Isddd), getinargx(t), n.List, desc)
		ok |= Etop
		if t.Outtuple == 0 {
			goto ret
		}
		ok |= Erv
		if t.Outtuple == 1 {
			t = getoutargx(l.Type).Type
			if t == nil {
				goto error
			}
			if t.Etype == TFIELD {
				t = t.Type
			}
			n.Type = t
			goto ret
		}

		// multiple return
		if !(top&(Efnstruct|Etop) != 0) {
			Yyerror("multiple-value %v() in single-value context", Nconv(l, 0))
			goto ret
		}

		n.Type = getoutargx(l.Type)
		goto ret

	case OCAP,
		OLEN,
		OREAL,
		OIMAG:
		ok |= Erv
		if onearg(n, "%v", Oconv(int(n.Op), 0)) < 0 {
			goto error
		}
		typecheck(&n.Left, Erv)
		defaultlit(&n.Left, nil)
		implicitstar(&n.Left)
		l = n.Left
		t = l.Type
		if t == nil {
			goto error
		}
		switch n.Op {
		case OCAP:
			if !(okforcap[t.Etype] != 0) {
				goto badcall1
			}

		case OLEN:
			if !(okforlen[t.Etype] != 0) {
				goto badcall1
			}

		case OREAL,
			OIMAG:
			if !(Iscomplex[t.Etype] != 0) {
				goto badcall1
			}
			if Isconst(l, CTCPLX) != 0 {
				r = n
				if n.Op == OREAL {
					n = nodfltconst(&l.Val.U.Cval.Real)
				} else {
					n = nodfltconst(&l.Val.U.Cval.Imag)
				}
				n.Orig = r
			}

			n.Type = Types[cplxsubtype(int(t.Etype))]
			goto ret
		}

		// might be constant
		switch t.Etype {
		case TSTRING:
			if Isconst(l, CTSTR) != 0 {
				r = Nod(OXXX, nil, nil)
				Nodconst(r, Types[TINT], int64(len(l.Val.U.Sval.S)))
				r.Orig = n
				n = r
			}

		case TARRAY:
			if t.Bound < 0 { // slice
				break
			}
			if callrecv(l) != 0 { // has call or receive
				break
			}
			r = Nod(OXXX, nil, nil)
			Nodconst(r, Types[TINT], t.Bound)
			r.Orig = n
			n = r
		}

		n.Type = Types[TINT]
		goto ret

	case OCOMPLEX:
		ok |= Erv
		if count(n.List) == 1 {
			typechecklist(n.List, Efnstruct)
			if n.List.N.Op != OCALLFUNC && n.List.N.Op != OCALLMETH {
				Yyerror("invalid operation: complex expects two arguments")
				goto error
			}

			t = n.List.N.Left.Type
			if t.Outtuple != 2 {
				Yyerror("invalid operation: complex expects two arguments, %v returns %d results", Nconv(n.List.N, 0), t.Outtuple)
				goto error
			}

			t = n.List.N.Type.Type
			l = t.Nname
			r = t.Down.Nname
		} else {
			if twoarg(n) < 0 {
				goto error
			}
			l = typecheck(&n.Left, Erv|top&Eiota)
			r = typecheck(&n.Right, Erv|top&Eiota)
			if l.Type == nil || r.Type == nil {
				goto error
			}
			defaultlit2(&l, &r, 0)
			if l.Type == nil || r.Type == nil {
				goto error
			}
			n.Left = l
			n.Right = r
		}

		if !Eqtype(l.Type, r.Type) {
			Yyerror("invalid operation: %v (mismatched types %v and %v)", Nconv(n, 0), Tconv(l.Type, 0), Tconv(r.Type, 0))
			goto error
		}

		switch l.Type.Etype {
		default:
			Yyerror("invalid operation: %v (arguments have type %v, expected floating-point)", Nconv(n, 0), Tconv(l.Type, 0), r.Type)
			goto error

		case TIDEAL:
			t = Types[TIDEAL]

		case TFLOAT32:
			t = Types[TCOMPLEX64]

		case TFLOAT64:
			t = Types[TCOMPLEX128]
		}

		if l.Op == OLITERAL && r.Op == OLITERAL {
			// make it a complex literal
			r = nodcplxlit(l.Val, r.Val)

			r.Orig = n
			n = r
		}

		n.Type = t
		goto ret

	case OCLOSE:
		if onearg(n, "%v", Oconv(int(n.Op), 0)) < 0 {
			goto error
		}
		typecheck(&n.Left, Erv)
		defaultlit(&n.Left, nil)
		l = n.Left
		t = l.Type
		if t == nil {
			goto error
		}
		if t.Etype != TCHAN {
			Yyerror("invalid operation: %v (non-chan type %v)", Nconv(n, 0), Tconv(t, 0))
			goto error
		}

		if !(t.Chan&Csend != 0) {
			Yyerror("invalid operation: %v (cannot close receive-only channel)", Nconv(n, 0))
			goto error
		}

		ok |= Etop
		goto ret

	case ODELETE:
		args = n.List
		if args == nil {
			Yyerror("missing arguments to delete")
			goto error
		}

		if args.Next == nil {
			Yyerror("missing second (key) argument to delete")
			goto error
		}

		if args.Next.Next != nil {
			Yyerror("too many arguments to delete")
			goto error
		}

		ok |= Etop
		typechecklist(args, Erv)
		l = args.N
		r = args.Next.N
		if l.Type != nil && l.Type.Etype != TMAP {
			Yyerror("first argument to delete must be map; have %v", Tconv(l.Type, obj.FmtLong))
			goto error
		}

		args.Next.N = assignconv(r, l.Type.Down, "delete")
		goto ret

	case OAPPEND:
		ok |= Erv
		args = n.List
		if args == nil {
			Yyerror("missing arguments to append")
			goto error
		}

		if count(args) == 1 && !(n.Isddd != 0) {
			typecheck(&args.N, Erv|Efnstruct)
		} else {
			typechecklist(args, Erv)
		}

		t = args.N.Type
		if t == nil {
			goto error
		}

		// Unpack multiple-return result before type-checking.
		if Istype(t, TSTRUCT) != 0 && t.Funarg != 0 {
			t = t.Type
			if Istype(t, TFIELD) != 0 {
				t = t.Type
			}
		}

		n.Type = t
		if !(Isslice(t) != 0) {
			if Isconst(args.N, CTNIL) != 0 {
				Yyerror("first argument to append must be typed slice; have untyped nil", t)
				goto error
			}

			Yyerror("first argument to append must be slice; have %v", Tconv(t, obj.FmtLong))
			goto error
		}

		if n.Isddd != 0 {
			if args.Next == nil {
				Yyerror("cannot use ... on first argument to append")
				goto error
			}

			if args.Next.Next != nil {
				Yyerror("too many arguments to append")
				goto error
			}

			if Istype(t.Type, TUINT8) != 0 && Istype(args.Next.N.Type, TSTRING) != 0 {
				defaultlit(&args.Next.N, Types[TSTRING])
				goto ret
			}

			args.Next.N = assignconv(args.Next.N, t.Orig, "append")
			goto ret
		}

		for args = args.Next; args != nil; args = args.Next {
			if args.N.Type == nil {
				continue
			}
			args.N = assignconv(args.N, t.Type, "append")
		}

		goto ret

	case OCOPY:
		ok |= Etop | Erv
		args = n.List
		if args == nil || args.Next == nil {
			Yyerror("missing arguments to copy")
			goto error
		}

		if args.Next.Next != nil {
			Yyerror("too many arguments to copy")
			goto error
		}

		n.Left = args.N
		n.Right = args.Next.N
		n.List = nil
		n.Type = Types[TINT]
		typecheck(&n.Left, Erv)
		typecheck(&n.Right, Erv)
		if n.Left.Type == nil || n.Right.Type == nil {
			goto error
		}
		defaultlit(&n.Left, nil)
		defaultlit(&n.Right, nil)
		if n.Left.Type == nil || n.Right.Type == nil {
			goto error
		}

		// copy([]byte, string)
		if Isslice(n.Left.Type) != 0 && n.Right.Type.Etype == TSTRING {
			if Eqtype(n.Left.Type.Type, bytetype) {
				goto ret
			}
			Yyerror("arguments to copy have different element types: %v and string", Tconv(n.Left.Type, obj.FmtLong))
			goto error
		}

		if !(Isslice(n.Left.Type) != 0) || !(Isslice(n.Right.Type) != 0) {
			if !(Isslice(n.Left.Type) != 0) && !(Isslice(n.Right.Type) != 0) {
				Yyerror("arguments to copy must be slices; have %v, %v", Tconv(n.Left.Type, obj.FmtLong), Tconv(n.Right.Type, obj.FmtLong))
			} else if !(Isslice(n.Left.Type) != 0) {
				Yyerror("first argument to copy should be slice; have %v", Tconv(n.Left.Type, obj.FmtLong))
			} else {
				Yyerror("second argument to copy should be slice or string; have %v", Tconv(n.Right.Type, obj.FmtLong))
			}
			goto error
		}

		if !Eqtype(n.Left.Type.Type, n.Right.Type.Type) {
			Yyerror("arguments to copy have different element types: %v and %v", Tconv(n.Left.Type, obj.FmtLong), Tconv(n.Right.Type, obj.FmtLong))
			goto error
		}

		goto ret

	case OCONV:
		goto doconv

	case OMAKE:
		ok |= Erv
		args = n.List
		if args == nil {
			Yyerror("missing argument to make")
			goto error
		}

		n.List = nil
		l = args.N
		args = args.Next
		typecheck(&l, Etype)
		t = l.Type
		if t == nil {
			goto error
		}

		switch t.Etype {
		default:
			Yyerror("cannot make type %v", Tconv(t, 0))
			goto error

		case TARRAY:
			if !(Isslice(t) != 0) {
				Yyerror("cannot make type %v", Tconv(t, 0))
				goto error
			}

			if args == nil {
				Yyerror("missing len argument to make(%v)", Tconv(t, 0))
				goto error
			}

			l = args.N
			args = args.Next
			typecheck(&l, Erv)
			r = nil
			if args != nil {
				r = args.N
				args = args.Next
				typecheck(&r, Erv)
			}

			if l.Type == nil || (r != nil && r.Type == nil) {
				goto error
			}
			et = bool2int(checkmake(t, "len", l) < 0)
			et |= bool2int(r != nil && checkmake(t, "cap", r) < 0)
			if et != 0 {
				goto error
			}
			if Isconst(l, CTINT) != 0 && r != nil && Isconst(r, CTINT) != 0 && Mpcmpfixfix(l.Val.U.Xval, r.Val.U.Xval) > 0 {
				Yyerror("len larger than cap in make(%v)", Tconv(t, 0))
				goto error
			}

			n.Left = l
			n.Right = r
			n.Op = OMAKESLICE

		case TMAP:
			if args != nil {
				l = args.N
				args = args.Next
				typecheck(&l, Erv)
				defaultlit(&l, Types[TINT])
				if l.Type == nil {
					goto error
				}
				if checkmake(t, "size", l) < 0 {
					goto error
				}
				n.Left = l
			} else {
				n.Left = Nodintconst(0)
			}
			n.Op = OMAKEMAP

		case TCHAN:
			l = nil
			if args != nil {
				l = args.N
				args = args.Next
				typecheck(&l, Erv)
				defaultlit(&l, Types[TINT])
				if l.Type == nil {
					goto error
				}
				if checkmake(t, "buffer", l) < 0 {
					goto error
				}
				n.Left = l
			} else {
				n.Left = Nodintconst(0)
			}
			n.Op = OMAKECHAN
		}

		if args != nil {
			Yyerror("too many arguments to make(%v)", Tconv(t, 0))
			n.Op = OMAKE
			goto error
		}

		n.Type = t
		goto ret

	case ONEW:
		ok |= Erv
		args = n.List
		if args == nil {
			Yyerror("missing argument to new")
			goto error
		}

		l = args.N
		typecheck(&l, Etype)
		t = l.Type
		if t == nil {
			goto error
		}
		if args.Next != nil {
			Yyerror("too many arguments to new(%v)", Tconv(t, 0))
			goto error
		}

		n.Left = l
		n.Type = Ptrto(t)
		goto ret

	case OPRINT,
		OPRINTN:
		ok |= Etop
		typechecklist(n.List, Erv|Eindir) // Eindir: address does not escape
		for args = n.List; args != nil; args = args.Next {
			// Special case for print: int constant is int64, not int.
			if Isconst(args.N, CTINT) != 0 {
				defaultlit(&args.N, Types[TINT64])
			} else {
				defaultlit(&args.N, nil)
			}
		}

		goto ret

	case OPANIC:
		ok |= Etop
		if onearg(n, "panic") < 0 {
			goto error
		}
		typecheck(&n.Left, Erv)
		defaultlit(&n.Left, Types[TINTER])
		if n.Left.Type == nil {
			goto error
		}
		goto ret

	case ORECOVER:
		ok |= Erv | Etop
		if n.List != nil {
			Yyerror("too many arguments to recover")
			goto error
		}

		n.Type = Types[TINTER]
		goto ret

	case OCLOSURE:
		ok |= Erv
		typecheckclosure(n, top)
		if n.Type == nil {
			goto error
		}
		goto ret

	case OITAB:
		ok |= Erv
		typecheck(&n.Left, Erv)
		t = n.Left.Type
		if t == nil {
			goto error
		}
		if t.Etype != TINTER {
			Fatal("OITAB of %v", Tconv(t, 0))
		}
		n.Type = Ptrto(Types[TUINTPTR])
		goto ret

	case OSPTR:
		ok |= Erv
		typecheck(&n.Left, Erv)
		t = n.Left.Type
		if t == nil {
			goto error
		}
		if !(Isslice(t) != 0) && t.Etype != TSTRING {
			Fatal("OSPTR of %v", Tconv(t, 0))
		}
		if t.Etype == TSTRING {
			n.Type = Ptrto(Types[TUINT8])
		} else {
			n.Type = Ptrto(t.Type)
		}
		goto ret

	case OCLOSUREVAR:
		ok |= Erv
		goto ret

	case OCFUNC:
		ok |= Erv
		typecheck(&n.Left, Erv)
		n.Type = Types[TUINTPTR]
		goto ret

	case OCONVNOP:
		ok |= Erv
		typecheck(&n.Left, Erv)
		goto ret

		/*
		 * statements
		 */
	case OAS:
		ok |= Etop

		typecheckas(n)

		// Code that creates temps does not bother to set defn, so do it here.
		if n.Left.Op == ONAME && strings.HasPrefix(n.Left.Sym.Name, "autotmp_") {
			n.Left.Defn = n
		}
		goto ret

	case OAS2:
		ok |= Etop
		typecheckas2(n)
		goto ret

	case OBREAK,
		OCONTINUE,
		ODCL,
		OEMPTY,
		OGOTO,
		OXFALL,
		OVARKILL:
		ok |= Etop
		goto ret

	case OLABEL:
		ok |= Etop
		decldepth++
		goto ret

	case ODEFER:
		ok |= Etop
		typecheck(&n.Left, Etop|Erv)
		if !(n.Left.Diag != 0) {
			checkdefergo(n)
		}
		goto ret

	case OPROC:
		ok |= Etop
		typecheck(&n.Left, Etop|Eproc|Erv)
		checkdefergo(n)
		goto ret

	case OFOR:
		ok |= Etop
		typechecklist(n.Ninit, Etop)
		decldepth++
		typecheck(&n.Ntest, Erv)
		if n.Ntest != nil {
			t = n.Ntest.Type
			if t != nil && t.Etype != TBOOL {
				Yyerror("non-bool %v used as for condition", Nconv(n.Ntest, obj.FmtLong))
			}
		}
		typecheck(&n.Nincr, Etop)
		typechecklist(n.Nbody, Etop)
		decldepth--
		goto ret

	case OIF:
		ok |= Etop
		typechecklist(n.Ninit, Etop)
		typecheck(&n.Ntest, Erv)
		if n.Ntest != nil {
			t = n.Ntest.Type
			if t != nil && t.Etype != TBOOL {
				Yyerror("non-bool %v used as if condition", Nconv(n.Ntest, obj.FmtLong))
			}
		}
		typechecklist(n.Nbody, Etop)
		typechecklist(n.Nelse, Etop)
		goto ret

	case ORETURN:
		ok |= Etop
		if count(n.List) == 1 {
			typechecklist(n.List, Erv|Efnstruct)
		} else {
			typechecklist(n.List, Erv)
		}
		if Curfn == nil {
			Yyerror("return outside function")
			goto error
		}

		if Curfn.Type.Outnamed != 0 && n.List == nil {
			goto ret
		}
		typecheckaste(ORETURN, nil, 0, getoutargx(Curfn.Type), n.List, "return argument")
		goto ret

	case ORETJMP:
		ok |= Etop
		goto ret

	case OSELECT:
		ok |= Etop
		typecheckselect(n)
		goto ret

	case OSWITCH:
		ok |= Etop
		typecheckswitch(n)
		goto ret

	case ORANGE:
		ok |= Etop
		typecheckrange(n)
		goto ret

	case OTYPESW:
		Yyerror("use of .(type) outside type switch")
		goto error

	case OXCASE:
		ok |= Etop
		typechecklist(n.List, Erv)
		typechecklist(n.Nbody, Etop)
		goto ret

	case ODCLFUNC:
		ok |= Etop
		typecheckfunc(n)
		goto ret

	case ODCLCONST:
		ok |= Etop
		typecheck(&n.Left, Erv)
		goto ret

	case ODCLTYPE:
		ok |= Etop
		typecheck(&n.Left, Etype)
		if !(incannedimport != 0) {
			checkwidth(n.Left.Type)
		}
		goto ret
	}

	goto ret

arith:
	if op == OLSH || op == ORSH {
		goto shift
	}

	// ideal mixed with non-ideal
	defaultlit2(&l, &r, 0)

	n.Left = l
	n.Right = r
	if l.Type == nil || r.Type == nil {
		goto error
	}
	t = l.Type
	if t.Etype == TIDEAL {
		t = r.Type
	}
	et = int(t.Etype)
	if et == TIDEAL {
		et = TINT
	}
	aop = 0
	if iscmp[n.Op] != 0 && t.Etype != TIDEAL && !Eqtype(l.Type, r.Type) {
		// comparison is okay as long as one side is
		// assignable to the other.  convert so they have
		// the same type.
		//
		// the only conversion that isn't a no-op is concrete == interface.
		// in that case, check comparability of the concrete type.
		// The conversion allocates, so only do it if the concrete type is huge.
		if r.Type.Etype != TBLANK {
			aop = assignop(l.Type, r.Type, nil)
			if aop != 0 {
				if Isinter(r.Type) != 0 && !(Isinter(l.Type) != 0) && algtype1(l.Type, nil) == ANOEQ {
					Yyerror("invalid operation: %v (operator %v not defined on %s)", Nconv(n, 0), Oconv(int(op), 0), typekind(l.Type))
					goto error
				}

				dowidth(l.Type)
				if Isinter(r.Type) == Isinter(l.Type) || l.Type.Width >= 1<<16 {
					l = Nod(aop, l, nil)
					l.Type = r.Type
					l.Typecheck = 1
					n.Left = l
				}

				t = r.Type
				goto converted
			}
		}

		if l.Type.Etype != TBLANK {
			aop = assignop(r.Type, l.Type, nil)
			if aop != 0 {
				if Isinter(l.Type) != 0 && !(Isinter(r.Type) != 0) && algtype1(r.Type, nil) == ANOEQ {
					Yyerror("invalid operation: %v (operator %v not defined on %s)", Nconv(n, 0), Oconv(int(op), 0), typekind(r.Type))
					goto error
				}

				dowidth(r.Type)
				if Isinter(r.Type) == Isinter(l.Type) || r.Type.Width >= 1<<16 {
					r = Nod(aop, r, nil)
					r.Type = l.Type
					r.Typecheck = 1
					n.Right = r
				}

				t = l.Type
			}
		}

	converted:
		et = int(t.Etype)
	}

	if t.Etype != TIDEAL && !Eqtype(l.Type, r.Type) {
		defaultlit2(&l, &r, 1)
		if n.Op == OASOP && n.Implicit != 0 {
			Yyerror("invalid operation: %v (non-numeric type %v)", Nconv(n, 0), Tconv(l.Type, 0))
			goto error
		}

		if Isinter(r.Type) == Isinter(l.Type) || aop == 0 {
			Yyerror("invalid operation: %v (mismatched types %v and %v)", Nconv(n, 0), Tconv(l.Type, 0), Tconv(r.Type, 0))
			goto error
		}
	}

	if !(okfor[op][et] != 0) {
		Yyerror("invalid operation: %v (operator %v not defined on %s)", Nconv(n, 0), Oconv(int(op), 0), typekind(t))
		goto error
	}

	// okfor allows any array == array, map == map, func == func.
	// restrict to slice/map/func == nil and nil == slice/map/func.
	if Isfixedarray(l.Type) != 0 && algtype1(l.Type, nil) == ANOEQ {
		Yyerror("invalid operation: %v (%v cannot be compared)", Nconv(n, 0), Tconv(l.Type, 0))
		goto error
	}

	if Isslice(l.Type) != 0 && !(isnil(l) != 0) && !(isnil(r) != 0) {
		Yyerror("invalid operation: %v (slice can only be compared to nil)", Nconv(n, 0))
		goto error
	}

	if l.Type.Etype == TMAP && !(isnil(l) != 0) && !(isnil(r) != 0) {
		Yyerror("invalid operation: %v (map can only be compared to nil)", Nconv(n, 0))
		goto error
	}

	if l.Type.Etype == TFUNC && !(isnil(l) != 0) && !(isnil(r) != 0) {
		Yyerror("invalid operation: %v (func can only be compared to nil)", Nconv(n, 0))
		goto error
	}

	if l.Type.Etype == TSTRUCT && algtype1(l.Type, &badtype) == ANOEQ {
		Yyerror("invalid operation: %v (struct containing %v cannot be compared)", Nconv(n, 0), Tconv(badtype, 0))
		goto error
	}

	t = l.Type
	if iscmp[n.Op] != 0 {
		evconst(n)
		t = idealbool
		if n.Op != OLITERAL {
			defaultlit2(&l, &r, 1)
			n.Left = l
			n.Right = r
		}
	} else if n.Op == OANDAND || n.Op == OOROR {
		if l.Type == r.Type {
			t = l.Type
		} else if l.Type == idealbool {
			t = r.Type
		} else if r.Type == idealbool {
			t = l.Type
		}
	} else
	// non-comparison operators on ideal bools should make them lose their ideal-ness
	if t == idealbool {
		t = Types[TBOOL]
	}

	if et == TSTRING {
		if iscmp[n.Op] != 0 {
			n.Etype = n.Op
			n.Op = OCMPSTR
		} else if n.Op == OADD {
			// create OADDSTR node with list of strings in x + y + z + (w + v) + ...
			n.Op = OADDSTR

			if l.Op == OADDSTR {
				n.List = l.List
			} else {
				n.List = list1(l)
			}
			if r.Op == OADDSTR {
				n.List = concat(n.List, r.List)
			} else {
				n.List = list(n.List, r)
			}
			n.Left = nil
			n.Right = nil
		}
	}

	if et == TINTER {
		if l.Op == OLITERAL && l.Val.Ctype == CTNIL {
			// swap for back end
			n.Left = r

			n.Right = l
		} else if r.Op == OLITERAL && r.Val.Ctype == CTNIL {
		} else // leave alone for back end
		if Isinter(r.Type) == Isinter(l.Type) {
			n.Etype = n.Op
			n.Op = OCMPIFACE
		}
	}

	if (op == ODIV || op == OMOD) && Isconst(r, CTINT) != 0 {
		if mpcmpfixc(r.Val.U.Xval, 0) == 0 {
			Yyerror("division by zero")
			goto error
		}
	}

	n.Type = t
	goto ret

shift:
	defaultlit(&r, Types[TUINT])
	n.Right = r
	t = r.Type
	if !(Isint[t.Etype] != 0) || Issigned[t.Etype] != 0 {
		Yyerror("invalid operation: %v (shift count type %v, must be unsigned integer)", Nconv(n, 0), Tconv(r.Type, 0))
		goto error
	}

	t = l.Type
	if t != nil && t.Etype != TIDEAL && !(Isint[t.Etype] != 0) {
		Yyerror("invalid operation: %v (shift of type %v)", Nconv(n, 0), Tconv(t, 0))
		goto error
	}

	// no defaultlit for left
	// the outer context gives the type
	n.Type = l.Type

	goto ret

doconv:
	ok |= Erv
	saveorignode(n)
	typecheck(&n.Left, Erv|top&(Eindir|Eiota))
	convlit1(&n.Left, n.Type, 1)
	t = n.Left.Type
	if t == nil || n.Type == nil {
		goto error
	}
	n.Op = uint8(convertop(t, n.Type, &why))
	if (n.Op) == 0 {
		if !(n.Diag != 0) && !(n.Type.Broke != 0) {
			Yyerror("cannot convert %v to type %v%s", Nconv(n.Left, obj.FmtLong), Tconv(n.Type, 0), why)
			n.Diag = 1
		}

		n.Op = OCONV
	}

	switch n.Op {
	case OCONVNOP:
		if n.Left.Op == OLITERAL && n.Type != Types[TBOOL] {
			r = Nod(OXXX, nil, nil)
			n.Op = OCONV
			n.Orig = r
			*r = *n
			n.Op = OLITERAL
			n.Val = n.Left.Val
		}

		// do not use stringtoarraylit.
	// generated code and compiler memory footprint is better without it.
	case OSTRARRAYBYTE:
		break

	case OSTRARRAYRUNE:
		if n.Left.Op == OLITERAL {
			stringtoarraylit(&n)
		}
	}

	goto ret

ret:
	t = n.Type
	if t != nil && !(t.Funarg != 0) && n.Op != OTYPE {
		switch t.Etype {
		case TFUNC, // might have TANY; wait until its called
			TANY,
			TFORW,
			TIDEAL,
			TNIL,
			TBLANK:
			break

		default:
			checkwidth(t)
		}
	}

	if safemode != 0 && !(incannedimport != 0) && !(importpkg != nil) && !(compiling_wrappers != 0) && t != nil && t.Etype == TUNSAFEPTR {
		Yyerror("cannot use unsafe.Pointer")
	}

	evconst(n)
	if n.Op == OTYPE && !(top&Etype != 0) {
		Yyerror("type %v is not an expression", Tconv(n.Type, 0))
		goto error
	}

	if top&(Erv|Etype) == Etype && n.Op != OTYPE {
		Yyerror("%v is not a type", Nconv(n, 0))
		goto error
	}

	// TODO(rsc): simplify
	if (top&(Ecall|Erv|Etype) != 0) && !(top&Etop != 0) && !(ok&(Erv|Etype|Ecall) != 0) {
		Yyerror("%v used as value", Nconv(n, 0))
		goto error
	}

	if (top&Etop != 0) && !(top&(Ecall|Erv|Etype) != 0) && !(ok&Etop != 0) {
		if n.Diag == 0 {
			Yyerror("%v evaluated but not used", Nconv(n, 0))
			n.Diag = 1
		}

		goto error
	}

	/* TODO
	if(n->type == T)
		fatal("typecheck nil type");
	*/
	goto out

badcall1:
	Yyerror("invalid argument %v for %v", Nconv(n.Left, obj.FmtLong), Oconv(int(n.Op), 0))
	goto error

error:
	n.Type = nil

out:
	*np = n
}

func checksliceindex(l *Node, r *Node, tp *Type) int {
	var t *Type

	t = r.Type
	if t == nil {
		return -1
	}
	if !(Isint[t.Etype] != 0) {
		Yyerror("invalid slice index %v (type %v)", Nconv(r, 0), Tconv(t, 0))
		return -1
	}

	if r.Op == OLITERAL {
		if Mpgetfix(r.Val.U.Xval) < 0 {
			Yyerror("invalid slice index %v (index must be non-negative)", Nconv(r, 0))
			return -1
		} else if tp != nil && tp.Bound > 0 && Mpgetfix(r.Val.U.Xval) > tp.Bound {
			Yyerror("invalid slice index %v (out of bounds for %d-element array)", Nconv(r, 0), tp.Bound)
			return -1
		} else if Isconst(l, CTSTR) != 0 && Mpgetfix(r.Val.U.Xval) > int64(len(l.Val.U.Sval.S)) {
			Yyerror("invalid slice index %v (out of bounds for %d-byte string)", Nconv(r, 0), len(l.Val.U.Sval.S))
			return -1
		} else if Mpcmpfixfix(r.Val.U.Xval, Maxintval[TINT]) > 0 {
			Yyerror("invalid slice index %v (index too large)", Nconv(r, 0))
			return -1
		}
	}

	return 0
}

func checksliceconst(lo *Node, hi *Node) int {
	if lo != nil && hi != nil && lo.Op == OLITERAL && hi.Op == OLITERAL && Mpcmpfixfix(lo.Val.U.Xval, hi.Val.U.Xval) > 0 {
		Yyerror("invalid slice index: %v > %v", Nconv(lo, 0), Nconv(hi, 0))
		return -1
	}

	return 0
}

func checkdefergo(n *Node) {
	var what string

	what = "defer"
	if n.Op == OPROC {
		what = "go"
	}

	switch n.Left.Op {
	// ok
	case OCALLINTER,
		OCALLMETH,
		OCALLFUNC,
		OCLOSE,
		OCOPY,
		ODELETE,
		OPANIC,
		OPRINT,
		OPRINTN,
		ORECOVER:
		return

	case OAPPEND,
		OCAP,
		OCOMPLEX,
		OIMAG,
		OLEN,
		OMAKE,
		OMAKESLICE,
		OMAKECHAN,
		OMAKEMAP,
		ONEW,
		OREAL,
		OLITERAL: // conversion or unsafe.Alignof, Offsetof, Sizeof
		if n.Left.Orig != nil && n.Left.Orig.Op == OCONV {
			break
		}
		Yyerror("%s discards result of %v", what, Nconv(n.Left, 0))
		return
	}

	// type is broken or missing, most likely a method call on a broken type
	// we will warn about the broken type elsewhere. no need to emit a potentially confusing error
	if n.Left.Type == nil || n.Left.Type.Broke != 0 {
		return
	}

	if !(n.Diag != 0) {
		// The syntax made sure it was a call, so this must be
		// a conversion.
		n.Diag = 1

		Yyerror("%s requires function call, not conversion", what)
	}
}

func implicitstar(nn **Node) {
	var t *Type
	var n *Node

	// insert implicit * if needed for fixed array
	n = *nn

	t = n.Type
	if t == nil || !(Isptr[t.Etype] != 0) {
		return
	}
	t = t.Type
	if t == nil {
		return
	}
	if !(Isfixedarray(t) != 0) {
		return
	}
	n = Nod(OIND, n, nil)
	n.Implicit = 1
	typecheck(&n, Erv)
	*nn = n
}

func onearg(n *Node, f string, args ...interface{}) int {
	var p string

	if n.Left != nil {
		return 0
	}
	if n.List == nil {
		p = fmt.Sprintf(f, args...)
		Yyerror("missing argument to %s: %v", p, Nconv(n, 0))
		return -1
	}

	if n.List.Next != nil {
		p = fmt.Sprintf(f, args...)
		Yyerror("too many arguments to %s: %v", p, Nconv(n, 0))
		n.Left = n.List.N
		n.List = nil
		return -1
	}

	n.Left = n.List.N
	n.List = nil
	return 0
}

func twoarg(n *Node) int {
	if n.Left != nil {
		return 0
	}
	if n.List == nil {
		Yyerror("missing argument to %v - %v", Oconv(int(n.Op), 0), Nconv(n, 0))
		return -1
	}

	n.Left = n.List.N
	if n.List.Next == nil {
		Yyerror("missing argument to %v - %v", Oconv(int(n.Op), 0), Nconv(n, 0))
		n.List = nil
		return -1
	}

	if n.List.Next.Next != nil {
		Yyerror("too many arguments to %v - %v", Oconv(int(n.Op), 0), Nconv(n, 0))
		n.List = nil
		return -1
	}

	n.Right = n.List.Next.N
	n.List = nil
	return 0
}

func lookdot1(errnode *Node, s *Sym, t *Type, f *Type, dostrcmp int) *Type {
	var r *Type

	r = nil
	for ; f != nil; f = f.Down {
		if dostrcmp != 0 && f.Sym.Name == s.Name {
			return f
		}
		if f.Sym != s {
			continue
		}
		if r != nil {
			if errnode != nil {
				Yyerror("ambiguous selector %v", Nconv(errnode, 0))
			} else if Isptr[t.Etype] != 0 {
				Yyerror("ambiguous selector (%v).%v", Tconv(t, 0), Sconv(s, 0))
			} else {
				Yyerror("ambiguous selector %v.%v", Tconv(t, 0), Sconv(s, 0))
			}
			break
		}

		r = f
	}

	return r
}

func looktypedot(n *Node, t *Type, dostrcmp int) int {
	var f1 *Type
	var f2 *Type
	var s *Sym

	s = n.Right.Sym

	if t.Etype == TINTER {
		f1 = lookdot1(n, s, t, t.Type, dostrcmp)
		if f1 == nil {
			return 0
		}

		n.Right = methodname(n.Right, t)
		n.Xoffset = f1.Width
		n.Type = f1.Type
		n.Op = ODOTINTER
		return 1
	}

	// Find the base type: methtype will fail if t
	// is not of the form T or *T.
	f2 = methtype(t, 0)

	if f2 == nil {
		return 0
	}

	expandmeth(f2)
	f2 = lookdot1(n, s, f2, f2.Xmethod, dostrcmp)
	if f2 == nil {
		return 0
	}

	// disallow T.m if m requires *T receiver
	if Isptr[getthisx(f2.Type).Type.Type.Etype] != 0 && !(Isptr[t.Etype] != 0) && f2.Embedded != 2 && !(isifacemethod(f2.Type) != 0) {
		Yyerror("invalid method expression %v (needs pointer receiver: (*%v).%v)", Nconv(n, 0), Tconv(t, 0), Sconv(f2.Sym, obj.FmtShort))
		return 0
	}

	n.Right = methodname(n.Right, t)
	n.Xoffset = f2.Width
	n.Type = f2.Type
	n.Op = ODOTMETH
	return 1
}

func derefall(t *Type) *Type {
	for t != nil && int(t.Etype) == Tptr {
		t = t.Type
	}
	return t
}

func lookdot(n *Node, t *Type, dostrcmp int) int {
	var f1 *Type
	var f2 *Type
	var tt *Type
	var rcvr *Type
	var s *Sym

	s = n.Right.Sym

	dowidth(t)
	f1 = nil
	if t.Etype == TSTRUCT || t.Etype == TINTER {
		f1 = lookdot1(n, s, t, t.Type, dostrcmp)
	}

	f2 = nil
	if n.Left.Type == t || n.Left.Type.Sym == nil {
		f2 = methtype(t, 0)
		if f2 != nil {
			// Use f2->method, not f2->xmethod: adddot has
			// already inserted all the necessary embedded dots.
			f2 = lookdot1(n, s, f2, f2.Method, dostrcmp)
		}
	}

	if f1 != nil {
		if f2 != nil {
			Yyerror("%v is both field and method", Sconv(n.Right.Sym, 0))
		}
		if f1.Width == BADWIDTH {
			Fatal("lookdot badwidth %v %p", Tconv(f1, 0), f1)
		}
		n.Xoffset = f1.Width
		n.Type = f1.Type
		n.Paramfld = f1
		if t.Etype == TINTER {
			if Isptr[n.Left.Type.Etype] != 0 {
				n.Left = Nod(OIND, n.Left, nil) // implicitstar
				n.Left.Implicit = 1
				typecheck(&n.Left, Erv)
			}

			n.Op = ODOTINTER
		}

		return 1
	}

	if f2 != nil {
		tt = n.Left.Type
		dowidth(tt)
		rcvr = getthisx(f2.Type).Type.Type
		if !Eqtype(rcvr, tt) {
			if int(rcvr.Etype) == Tptr && Eqtype(rcvr.Type, tt) {
				checklvalue(n.Left, "call pointer method on")
				n.Left = Nod(OADDR, n.Left, nil)
				n.Left.Implicit = 1
				typecheck(&n.Left, Etype|Erv)
			} else if int(tt.Etype) == Tptr && int(rcvr.Etype) != Tptr && Eqtype(tt.Type, rcvr) {
				n.Left = Nod(OIND, n.Left, nil)
				n.Left.Implicit = 1
				typecheck(&n.Left, Etype|Erv)
			} else if int(tt.Etype) == Tptr && int(tt.Type.Etype) == Tptr && Eqtype(derefall(tt), derefall(rcvr)) {
				Yyerror("calling method %v with receiver %v requires explicit dereference", Nconv(n.Right, 0), Nconv(n.Left, obj.FmtLong))
				for int(tt.Etype) == Tptr {
					// Stop one level early for method with pointer receiver.
					if int(rcvr.Etype) == Tptr && int(tt.Type.Etype) != Tptr {
						break
					}
					n.Left = Nod(OIND, n.Left, nil)
					n.Left.Implicit = 1
					typecheck(&n.Left, Etype|Erv)
					tt = tt.Type
				}
			} else {
				Fatal("method mismatch: %v for %v", Tconv(rcvr, 0), Tconv(tt, 0))
			}
		}

		n.Right = methodname(n.Right, n.Left.Type)
		n.Xoffset = f2.Width
		n.Type = f2.Type

		//		print("lookdot found [%p] %T\n", f2->type, f2->type);
		n.Op = ODOTMETH

		return 1
	}

	return 0
}

func nokeys(l *NodeList) int {
	for ; l != nil; l = l.Next {
		if l.N.Op == OKEY {
			return 0
		}
	}
	return 1
}

func hasddd(t *Type) int {
	var tl *Type

	for tl = t.Type; tl != nil; tl = tl.Down {
		if tl.Isddd != 0 {
			return 1
		}
	}

	return 0
}

func downcount(t *Type) int {
	var tl *Type
	var n int

	n = 0
	for tl = t.Type; tl != nil; tl = tl.Down {
		n++
	}

	return n
}

/*
 * typecheck assignment: type list = expression list
 */
func typecheckaste(op int, call *Node, isddd int, tstruct *Type, nl *NodeList, desc string) {
	var t *Type
	var tl *Type
	var tn *Type
	var n *Node
	var lno int
	var why string
	var n1 int
	var n2 int

	lno = int(lineno)

	if tstruct.Broke != 0 {
		goto out
	}

	n = nil
	if nl != nil && nl.Next == nil {
		n = nl.N
		if n.Type != nil {
			if n.Type.Etype == TSTRUCT && n.Type.Funarg != 0 {
				if !(hasddd(tstruct) != 0) {
					n1 = downcount(tstruct)
					n2 = downcount(n.Type)
					if n2 > n1 {
						goto toomany
					}
					if n2 < n1 {
						goto notenough
					}
				}

				tn = n.Type.Type
				for tl = tstruct.Type; tl != nil; tl = tl.Down {
					if tl.Isddd != 0 {
						for ; tn != nil; tn = tn.Down {
							if assignop(tn.Type, tl.Type.Type, &why) == 0 {
								if call != nil {
									Yyerror("cannot use %v as type %v in argument to %v%s", Tconv(tn.Type, 0), Tconv(tl.Type.Type, 0), Nconv(call, 0), why)
								} else {
									Yyerror("cannot use %v as type %v in %s%s", Tconv(tn.Type, 0), Tconv(tl.Type.Type, 0), desc, why)
								}
							}
						}

						goto out
					}

					if tn == nil {
						goto notenough
					}
					if assignop(tn.Type, tl.Type, &why) == 0 {
						if call != nil {
							Yyerror("cannot use %v as type %v in argument to %v%s", Tconv(tn.Type, 0), Tconv(tl.Type, 0), Nconv(call, 0), why)
						} else {
							Yyerror("cannot use %v as type %v in %s%s", Tconv(tn.Type, 0), Tconv(tl.Type, 0), desc, why)
						}
					}

					tn = tn.Down
				}

				if tn != nil {
					goto toomany
				}
				goto out
			}
		}
	}

	n1 = downcount(tstruct)
	n2 = count(nl)
	if !(hasddd(tstruct) != 0) {
		if n2 > n1 {
			goto toomany
		}
		if n2 < n1 {
			goto notenough
		}
	} else {
		if !(isddd != 0) {
			if n2 < n1-1 {
				goto notenough
			}
		} else {
			if n2 > n1 {
				goto toomany
			}
			if n2 < n1 {
				goto notenough
			}
		}
	}

	for tl = tstruct.Type; tl != nil; tl = tl.Down {
		t = tl.Type
		if tl.Isddd != 0 {
			if isddd != 0 {
				if nl == nil {
					goto notenough
				}
				if nl.Next != nil {
					goto toomany
				}
				n = nl.N
				setlineno(n)
				if n.Type != nil {
					nl.N = assignconv(n, t, desc)
				}
				goto out
			}

			for ; nl != nil; nl = nl.Next {
				n = nl.N
				setlineno(nl.N)
				if n.Type != nil {
					nl.N = assignconv(n, t.Type, desc)
				}
			}

			goto out
		}

		if nl == nil {
			goto notenough
		}
		n = nl.N
		setlineno(n)
		if n.Type != nil {
			nl.N = assignconv(n, t, desc)
		}
		nl = nl.Next
	}

	if nl != nil {
		goto toomany
	}
	if isddd != 0 {
		if call != nil {
			Yyerror("invalid use of ... in call to %v", Nconv(call, 0))
		} else {
			Yyerror("invalid use of ... in %v", Oconv(int(op), 0))
		}
	}

out:
	lineno = int32(lno)
	return

notenough:
	if n == nil || !(n.Diag != 0) {
		if call != nil {
			Yyerror("not enough arguments in call to %v", Nconv(call, 0))
		} else {
			Yyerror("not enough arguments to %v", Oconv(int(op), 0))
		}
		if n != nil {
			n.Diag = 1
		}
	}

	goto out

toomany:
	if call != nil {
		Yyerror("too many arguments in call to %v", Nconv(call, 0))
	} else {
		Yyerror("too many arguments to %v", Oconv(int(op), 0))
	}
	goto out
}

/*
 * type check composite
 */
func fielddup(n *Node, hash []*Node) {
	var h uint
	var s string
	var a *Node

	if n.Op != ONAME {
		Fatal("fielddup: not ONAME")
	}
	s = n.Sym.Name
	h = uint(stringhash(s) % uint32(len(hash)))
	for a = hash[h]; a != nil; a = a.Ntest {
		if a.Sym.Name == s {
			Yyerror("duplicate field name in struct literal: %s", s)
			return
		}
	}

	n.Ntest = hash[h]
	hash[h] = n
}

func keydup(n *Node, hash []*Node) {
	var h uint
	var b uint32
	var d float64
	var i int
	var a *Node
	var orign *Node
	var cmp Node
	var s string

	orign = n
	if n.Op == OCONVIFACE {
		n = n.Left
	}
	evconst(n)
	if n.Op != OLITERAL {
		return // we dont check variables
	}

	switch n.Val.Ctype {
	default: // unknown, bool, nil
		b = 23

	case CTINT,
		CTRUNE:
		b = uint32(Mpgetfix(n.Val.U.Xval))

	case CTFLT:
		d = mpgetflt(n.Val.U.Fval)
		x := math.Float64bits(d)
		for i := 0; i < 8; i++ {
			b = b*PRIME1 + uint32(x&0xFF)
			x >>= 8
		}

	case CTSTR:
		b = 0
		s = n.Val.U.Sval.S
		for i = len(n.Val.U.Sval.S); i > 0; i-- {
			b = b*PRIME1 + uint32(s[0])
			s = s[1:]
		}
	}

	h = uint(b % uint32(len(hash)))
	cmp = Node{}
	for a = hash[h]; a != nil; a = a.Ntest {
		cmp.Op = OEQ
		cmp.Left = n
		b = 0
		if a.Op == OCONVIFACE && orign.Op == OCONVIFACE {
			if Eqtype(a.Left.Type, n.Type) {
				cmp.Right = a.Left
				evconst(&cmp)
				b = uint32(cmp.Val.U.Bval)
			}
		} else if Eqtype(a.Type, n.Type) {
			cmp.Right = a
			evconst(&cmp)
			b = uint32(cmp.Val.U.Bval)
		}

		if b != 0 {
			Yyerror("duplicate key %v in map literal", Nconv(n, 0))
			return
		}
	}

	orign.Ntest = hash[h]
	hash[h] = orign
}

func indexdup(n *Node, hash []*Node) {
	var h uint
	var a *Node
	var b uint32
	var c uint32

	if n.Op != OLITERAL {
		Fatal("indexdup: not OLITERAL")
	}

	b = uint32(Mpgetfix(n.Val.U.Xval))
	h = uint(b % uint32(len(hash)))
	for a = hash[h]; a != nil; a = a.Ntest {
		c = uint32(Mpgetfix(a.Val.U.Xval))
		if b == c {
			Yyerror("duplicate index in array literal: %d", b)
			return
		}
	}

	n.Ntest = hash[h]
	hash[h] = n
}

func prime(h uint32, sr uint32) int {
	var n uint32

	for n = 3; n <= sr; n += 2 {
		if h%n == 0 {
			return 0
		}
	}
	return 1
}

func inithash(n *Node, autohash []*Node) []*Node {
	var h uint32
	var sr uint32
	var ll *NodeList
	var i int

	// count the number of entries
	h = 0

	for ll = n.List; ll != nil; ll = ll.Next {
		h++
	}

	// if the auto hash table is
	// large enough use it.
	if h <= uint32(len(autohash)) {
		for i := range autohash {
			autohash[i] = nil
		}
		return autohash
	}

	// make hash size odd and 12% larger than entries
	h += h / 8

	h |= 1

	// calculate sqrt of h
	sr = h / 2

	for i = 0; i < 5; i++ {
		sr = (sr + h/sr) / 2
	}

	// check for primeality
	for !(prime(h, sr) != 0) {
		h += 2
	}

	// build and return a throw-away hash table
	return make([]*Node, h)
}

func iscomptype(t *Type) int {
	switch t.Etype {
	case TARRAY,
		TSTRUCT,
		TMAP:
		return 1

	case TPTR32,
		TPTR64:
		switch t.Type.Etype {
		case TARRAY,
			TSTRUCT,
			TMAP:
			return 1
		}
	}

	return 0
}

func pushtype(n *Node, t *Type) {
	if n == nil || n.Op != OCOMPLIT || !(iscomptype(t) != 0) {
		return
	}

	if n.Right == nil {
		n.Right = typenod(t)
		n.Implicit = 1       // don't print
		n.Right.Implicit = 1 // * is okay
	} else if Debug['s'] != 0 {
		typecheck(&n.Right, Etype)
		if n.Right.Type != nil && Eqtype(n.Right.Type, t) {
			fmt.Printf("%v: redundant type: %v\n", n.Line(), Tconv(t, 0))
		}
	}
}

func typecheckcomplit(np **Node) {
	var bad int
	var i int
	var nerr int
	var length int64
	var l *Node
	var n *Node
	var norig *Node
	var r *Node
	var hash []*Node
	var ll *NodeList
	var t *Type
	var f *Type
	var s *Sym
	var s1 *Sym
	var lno int32
	var autohash [101]*Node

	n = *np
	lno = lineno

	if n.Right == nil {
		if n.List != nil {
			setlineno(n.List.N)
		}
		Yyerror("missing type in composite literal")
		goto error
	}

	// Save original node (including n->right)
	norig = Nod(int(n.Op), nil, nil)

	*norig = *n

	setlineno(n.Right)
	l = typecheck(&n.Right, Etype|Ecomplit) /* sic */
	t = l.Type
	if t == nil {
		goto error
	}
	nerr = nerrors
	n.Type = t

	if Isptr[t.Etype] != 0 {
		// For better or worse, we don't allow pointers as the composite literal type,
		// except when using the &T syntax, which sets implicit on the OIND.
		if !(n.Right.Implicit != 0) {
			Yyerror("invalid pointer type %v for composite literal (use &%v instead)", Tconv(t, 0), Tconv(t.Type, 0))
			goto error
		}

		// Also, the underlying type must be a struct, map, slice, or array.
		if !(iscomptype(t) != 0) {
			Yyerror("invalid pointer type %v for composite literal", Tconv(t, 0))
			goto error
		}

		t = t.Type
	}

	switch t.Etype {
	default:
		Yyerror("invalid type for composite literal: %v", Tconv(t, 0))
		n.Type = nil

	case TARRAY:
		hash = inithash(n, autohash[:])

		length = 0
		i = 0
		for ll = n.List; ll != nil; ll = ll.Next {
			l = ll.N
			setlineno(l)
			if l.Op != OKEY {
				l = Nod(OKEY, Nodintconst(int64(i)), l)
				l.Left.Type = Types[TINT]
				l.Left.Typecheck = 1
				ll.N = l
			}

			typecheck(&l.Left, Erv)
			evconst(l.Left)
			i = nonnegconst(l.Left)
			if i < 0 && !(l.Left.Diag != 0) {
				Yyerror("array index must be non-negative integer constant")
				l.Left.Diag = 1
				i = -(1 << 30) // stay negative for a while
			}

			if i >= 0 {
				indexdup(l.Left, hash)
			}
			i++
			if int64(i) > length {
				length = int64(i)
				if t.Bound >= 0 && length > t.Bound {
					setlineno(l)
					Yyerror("array index %d out of bounds [0:%d]", length-1, t.Bound)
					t.Bound = -1 // no more errors
				}
			}

			r = l.Right
			pushtype(r, t.Type)
			typecheck(&r, Erv)
			defaultlit(&r, t.Type)
			l.Right = assignconv(r, t.Type, "array element")
		}

		if t.Bound == -100 {
			t.Bound = length
		}
		if t.Bound < 0 {
			n.Right = Nodintconst(length)
		}
		n.Op = OARRAYLIT

	case TMAP:
		hash = inithash(n, autohash[:])

		for ll = n.List; ll != nil; ll = ll.Next {
			l = ll.N
			setlineno(l)
			if l.Op != OKEY {
				typecheck(&ll.N, Erv)
				Yyerror("missing key in map literal")
				continue
			}

			typecheck(&l.Left, Erv)
			defaultlit(&l.Left, t.Down)
			l.Left = assignconv(l.Left, t.Down, "map key")
			if l.Left.Op != OCONV {
				keydup(l.Left, hash)
			}

			r = l.Right
			pushtype(r, t.Type)
			typecheck(&r, Erv)
			defaultlit(&r, t.Type)
			l.Right = assignconv(r, t.Type, "map value")
		}

		n.Op = OMAPLIT

	case TSTRUCT:
		bad = 0
		if n.List != nil && nokeys(n.List) != 0 {
			// simple list of variables
			f = t.Type

			for ll = n.List; ll != nil; ll = ll.Next {
				setlineno(ll.N)
				typecheck(&ll.N, Erv)
				if f == nil {
					tmp12 := bad
					bad++
					if !(tmp12 != 0) {
						Yyerror("too many values in struct initializer")
					}
					continue
				}

				s = f.Sym
				if s != nil && !exportname(s.Name) && s.Pkg != localpkg {
					Yyerror("implicit assignment of unexported field '%s' in %v literal", s.Name, Tconv(t, 0))
				}

				// No pushtype allowed here.  Must name fields for that.
				ll.N = assignconv(ll.N, f.Type, "field value")

				ll.N = Nod(OKEY, newname(f.Sym), ll.N)
				ll.N.Left.Type = f
				ll.N.Left.Typecheck = 1
				f = f.Down
			}

			if f != nil {
				Yyerror("too few values in struct initializer")
			}
		} else {
			hash = inithash(n, autohash[:])

			// keyed list
			for ll = n.List; ll != nil; ll = ll.Next {
				l = ll.N
				setlineno(l)
				if l.Op != OKEY {
					tmp13 := bad
					bad++
					if !(tmp13 != 0) {
						Yyerror("mixture of field:value and value initializers")
					}
					typecheck(&ll.N, Erv)
					continue
				}

				s = l.Left.Sym
				if s == nil {
					Yyerror("invalid field name %v in struct initializer", Nconv(l.Left, 0))
					typecheck(&l.Right, Erv)
					continue
				}

				// Sym might have resolved to name in other top-level
				// package, because of import dot.  Redirect to correct sym
				// before we do the lookup.
				if s.Pkg != localpkg && exportname(s.Name) {
					s1 = Lookup(s.Name)
					if s1.Origpkg == s.Pkg {
						s = s1
					}
				}

				f = lookdot1(nil, s, t, t.Type, 0)
				if f == nil {
					Yyerror("unknown %v field '%v' in struct literal", Tconv(t, 0), Sconv(s, 0))
					continue
				}

				l.Left = newname(s)
				l.Left.Typecheck = 1
				l.Left.Type = f
				s = f.Sym
				fielddup(newname(s), hash)
				r = l.Right

				// No pushtype allowed here.  Tried and rejected.
				typecheck(&r, Erv)

				l.Right = assignconv(r, f.Type, "field value")
			}
		}

		n.Op = OSTRUCTLIT
	}

	if nerr != nerrors {
		goto error
	}

	n.Orig = norig
	if Isptr[n.Type.Etype] != 0 {
		n = Nod(OPTRLIT, n, nil)
		n.Typecheck = 1
		n.Type = n.Left.Type
		n.Left.Type = t
		n.Left.Typecheck = 1
	}

	n.Orig = norig
	*np = n
	lineno = lno
	return

error:
	n.Type = nil
	*np = n
	lineno = lno
}

/*
 * lvalue etc
 */
func islvalue(n *Node) int {
	switch n.Op {
	case OINDEX:
		if Isfixedarray(n.Left.Type) != 0 {
			return islvalue(n.Left)
		}
		if n.Left.Type != nil && n.Left.Type.Etype == TSTRING {
			return 0
		}
		fallthrough

		// fall through
	case OIND,
		ODOTPTR,
		OCLOSUREVAR,
		OPARAM:
		return 1

	case ODOT:
		return islvalue(n.Left)

	case ONAME:
		if n.Class == PFUNC {
			return 0
		}
		return 1
	}

	return 0
}

func checklvalue(n *Node, verb string) {
	if !(islvalue(n) != 0) {
		Yyerror("cannot %s %v", verb, Nconv(n, 0))
	}
}

func checkassign(stmt *Node, n *Node) {
	var r *Node
	var l *Node

	// Variables declared in ORANGE are assigned on every iteration.
	if n.Defn != stmt || stmt.Op == ORANGE {
		r = outervalue(n)
		for l = n; l != r; l = l.Left {
			l.Assigned = 1
			if l.Closure != nil {
				l.Closure.Assigned = 1
			}
		}

		l.Assigned = 1
		if l.Closure != nil {
			l.Closure.Assigned = 1
		}
	}

	if islvalue(n) != 0 {
		return
	}
	if n.Op == OINDEXMAP {
		n.Etype = 1
		return
	}

	// have already complained about n being undefined
	if n.Op == ONONAME {
		return
	}

	Yyerror("cannot assign to %v", Nconv(n, 0))
}

func checkassignlist(stmt *Node, l *NodeList) {
	for ; l != nil; l = l.Next {
		checkassign(stmt, l.N)
	}
}

// Check whether l and r are the same side effect-free expression,
// so that it is safe to reuse one instead of computing both.
func samesafeexpr(l *Node, r *Node) int {
	if l.Op != r.Op || !Eqtype(l.Type, r.Type) {
		return 0
	}

	switch l.Op {
	case ONAME,
		OCLOSUREVAR:
		return bool2int(l == r)

	case ODOT,
		ODOTPTR:
		return bool2int(l.Right != nil && r.Right != nil && l.Right.Sym == r.Right.Sym && samesafeexpr(l.Left, r.Left) != 0)

	case OIND:
		return samesafeexpr(l.Left, r.Left)

	case OINDEX:
		return bool2int(samesafeexpr(l.Left, r.Left) != 0 && samesafeexpr(l.Right, r.Right) != 0)
	}

	return 0
}

/*
 * type check assignment.
 * if this assignment is the definition of a var on the left side,
 * fill in the var's type.
 */
func typecheckas(n *Node) {
	// delicate little dance.
	// the definition of n may refer to this assignment
	// as its definition, in which case it will call typecheckas.
	// in that case, do not call typecheck back, or it will cycle.
	// if the variable has a type (ntype) then typechecking
	// will not look at defn, so it is okay (and desirable,
	// so that the conversion below happens).
	n.Left = resolve(n.Left)

	if n.Left.Defn != n || n.Left.Ntype != nil {
		typecheck(&n.Left, Erv|Easgn)
	}

	typecheck(&n.Right, Erv)
	checkassign(n, n.Left)
	if n.Right != nil && n.Right.Type != nil {
		if n.Left.Type != nil {
			n.Right = assignconv(n.Right, n.Left.Type, "assignment")
		}
	}

	if n.Left.Defn == n && n.Left.Ntype == nil {
		defaultlit(&n.Right, nil)
		n.Left.Type = n.Right.Type
	}

	// second half of dance.
	// now that right is done, typecheck the left
	// just to get it over with.  see dance above.
	n.Typecheck = 1

	if n.Left.Typecheck == 0 {
		typecheck(&n.Left, Erv|Easgn)
	}

	// Recognize slices being updated in place, for better code generation later.
	// Don't rewrite if using race detector, to avoid needing to teach race detector
	// about this optimization.
	if n.Left != nil && n.Left.Op != OINDEXMAP && n.Right != nil && !(flag_race != 0) {
		switch n.Right.Op {
		// For x = x[0:y], x can be updated in place, without touching pointer.
		// TODO(rsc): Reenable once it is actually updated in place without touching the pointer.
		case OSLICE,
			OSLICE3,
			OSLICESTR:
			if false && samesafeexpr(n.Left, n.Right.Left) != 0 && (n.Right.Right.Left == nil || iszero(n.Right.Right.Left) != 0) {
				n.Right.Reslice = 1
			}

			// For x = append(x, ...), x can be updated in place when there is capacity,
		// without touching the pointer; otherwise the emitted code to growslice
		// can take care of updating the pointer, and only in that case.
		// TODO(rsc): Reenable once the emitted code does update the pointer.
		case OAPPEND:
			if false && n.Right.List != nil && samesafeexpr(n.Left, n.Right.List.N) != 0 {
				n.Right.Reslice = 1
			}
		}
	}
}

func checkassignto(src *Type, dst *Node) {
	var why string

	if assignop(src, dst.Type, &why) == 0 {
		Yyerror("cannot assign %v to %v in multiple assignment%s", Tconv(src, 0), Nconv(dst, obj.FmtLong), why)
		return
	}
}

func typecheckas2(n *Node) {
	var cl int
	var cr int
	var ll *NodeList
	var lr *NodeList
	var l *Node
	var r *Node
	var s Iter
	var t *Type

	for ll = n.List; ll != nil; ll = ll.Next {
		// delicate little dance.
		ll.N = resolve(ll.N)

		if ll.N.Defn != n || ll.N.Ntype != nil {
			typecheck(&ll.N, Erv|Easgn)
		}
	}

	cl = count(n.List)
	cr = count(n.Rlist)
	if cl > 1 && cr == 1 {
		typecheck(&n.Rlist.N, Erv|Efnstruct)
	} else {
		typechecklist(n.Rlist, Erv)
	}
	checkassignlist(n, n.List)

	if cl == cr {
		// easy
		ll = n.List
		lr = n.Rlist
		for ; ll != nil; (func() { ll = ll.Next; lr = lr.Next })() {
			if ll.N.Type != nil && lr.N.Type != nil {
				lr.N = assignconv(lr.N, ll.N.Type, "assignment")
			}
			if ll.N.Defn == n && ll.N.Ntype == nil {
				defaultlit(&lr.N, nil)
				ll.N.Type = lr.N.Type
			}
		}

		goto out
	}

	l = n.List.N
	r = n.Rlist.N

	// x,y,z = f()
	if cr == 1 {
		if r.Type == nil {
			goto out
		}
		switch r.Op {
		case OCALLMETH,
			OCALLINTER,
			OCALLFUNC:
			if r.Type.Etype != TSTRUCT || r.Type.Funarg == 0 {
				break
			}
			cr = structcount(r.Type)
			if cr != cl {
				goto mismatch
			}
			n.Op = OAS2FUNC
			t = Structfirst(&s, &r.Type)
			for ll = n.List; ll != nil; ll = ll.Next {
				if t.Type != nil && ll.N.Type != nil {
					checkassignto(t.Type, ll.N)
				}
				if ll.N.Defn == n && ll.N.Ntype == nil {
					ll.N.Type = t.Type
				}
				t = structnext(&s)
			}

			goto out
		}
	}

	// x, ok = y
	if cl == 2 && cr == 1 {
		if r.Type == nil {
			goto out
		}
		switch r.Op {
		case OINDEXMAP,
			ORECV,
			ODOTTYPE:
			switch r.Op {
			case OINDEXMAP:
				n.Op = OAS2MAPR

			case ORECV:
				n.Op = OAS2RECV

			case ODOTTYPE:
				n.Op = OAS2DOTTYPE
				r.Op = ODOTTYPE2
			}

			if l.Type != nil {
				checkassignto(r.Type, l)
			}
			if l.Defn == n {
				l.Type = r.Type
			}
			l = n.List.Next.N
			if l.Type != nil && l.Type.Etype != TBOOL {
				checkassignto(Types[TBOOL], l)
			}
			if l.Defn == n && l.Ntype == nil {
				l.Type = Types[TBOOL]
			}
			goto out
		}
	}

mismatch:
	Yyerror("assignment count mismatch: %d = %d", cl, cr)

	// second half of dance
out:
	n.Typecheck = 1

	for ll = n.List; ll != nil; ll = ll.Next {
		if ll.N.Typecheck == 0 {
			typecheck(&ll.N, Erv|Easgn)
		}
	}
}

/*
 * type check function definition
 */
func typecheckfunc(n *Node) {
	var t *Type
	var rcvr *Type
	var l *NodeList

	typecheck(&n.Nname, Erv|Easgn)
	t = n.Nname.Type
	if t == nil {
		return
	}
	n.Type = t
	t.Nname = n.Nname
	rcvr = getthisx(t).Type
	if rcvr != nil && n.Shortname != nil && !isblank(n.Shortname) {
		addmethod(n.Shortname.Sym, t, true, n.Nname.Nointerface)
	}

	for l = n.Dcl; l != nil; l = l.Next {
		if l.N.Op == ONAME && (l.N.Class == PPARAM || l.N.Class == PPARAMOUT) {
			l.N.Decldepth = 1
		}
	}
}

func stringtoarraylit(np **Node) {
	n := *np
	if n.Left.Op != OLITERAL || n.Left.Val.Ctype != CTSTR {
		Fatal("stringtoarraylit %N", n)
	}

	s := n.Left.Val.U.Sval.S
	var l *NodeList
	if n.Type.Type.Etype == TUINT8 {
		// []byte
		for i := 0; i < len(s); i++ {
			l = list(l, Nod(OKEY, Nodintconst(int64(i)), Nodintconst(int64(s[0]))))
		}
	} else {
		// []rune
		i := 0
		for _, r := range s {
			l = list(l, Nod(OKEY, Nodintconst(int64(i)), Nodintconst(int64(r))))
			i++
		}
	}

	nn := Nod(OCOMPLIT, nil, typenod(n.Type))
	nn.List = l
	typecheck(&nn, Erv)
	*np = nn
}

var ntypecheckdeftype int

var methodqueue *NodeList

func domethod(n *Node) {
	var nt *Node
	var t *Type

	nt = n.Type.Nname
	typecheck(&nt, Etype)
	if nt.Type == nil {
		// type check failed; leave empty func
		n.Type.Etype = TFUNC

		n.Type.Nod = nil
		return
	}

	// If we have
	//	type I interface {
	//		M(_ int)
	//	}
	// then even though I.M looks like it doesn't care about the
	// value of its argument, a specific implementation of I may
	// care.  The _ would suppress the assignment to that argument
	// while generating a call, so remove it.
	for t = getinargx(nt.Type).Type; t != nil; t = t.Down {
		if t.Sym != nil && t.Sym.Name == "_" {
			t.Sym = nil
		}
	}

	*n.Type = *nt.Type
	n.Type.Nod = nil
	checkwidth(n.Type)
}

var mapqueue *NodeList

func copytype(n *Node, t *Type) {
	var maplineno int
	var embedlineno int
	var lno int
	var l *NodeList

	if t.Etype == TFORW {
		// This type isn't computed yet; when it is, update n.
		t.Copyto = list(t.Copyto, n)

		return
	}

	maplineno = int(n.Type.Maplineno)
	embedlineno = int(n.Type.Embedlineno)

	l = n.Type.Copyto
	*n.Type = *t

	t = n.Type
	t.Sym = n.Sym
	t.Local = n.Local
	t.Vargen = n.Vargen
	t.Siggen = 0
	t.Method = nil
	t.Xmethod = nil
	t.Nod = nil
	t.Printed = 0
	t.Deferwidth = 0
	t.Copyto = nil

	// Update nodes waiting on this type.
	for ; l != nil; l = l.Next {
		copytype(l.N, t)
	}

	// Double-check use of type as embedded type.
	lno = int(lineno)

	if embedlineno != 0 {
		lineno = int32(embedlineno)
		if Isptr[t.Etype] != 0 {
			Yyerror("embedded type cannot be a pointer")
		}
	}

	lineno = int32(lno)

	// Queue check for map until all the types are done settling.
	if maplineno != 0 {
		t.Maplineno = int32(maplineno)
		mapqueue = list(mapqueue, n)
	}
}

func typecheckdeftype(n *Node) {
	var lno int
	var t *Type
	var l *NodeList

	ntypecheckdeftype++
	lno = int(lineno)
	setlineno(n)
	n.Type.Sym = n.Sym
	n.Typecheck = 1
	typecheck(&n.Ntype, Etype)
	t = n.Ntype.Type
	if t == nil {
		n.Diag = 1
		n.Type = nil
		goto ret
	}

	if n.Type == nil {
		n.Diag = 1
		goto ret
	}

	// copy new type and clear fields
	// that don't come along.
	// anything zeroed here must be zeroed in
	// typedcl2 too.
	copytype(n, t)

ret:
	lineno = int32(lno)

	// if there are no type definitions going on, it's safe to
	// try to resolve the method types for the interfaces
	// we just read.
	if ntypecheckdeftype == 1 {
		for {
			l = methodqueue
			if !(l != nil) {
				break
			}
			methodqueue = nil
			for ; l != nil; l = l.Next {
				domethod(l.N)
			}
		}

		for l = mapqueue; l != nil; l = l.Next {
			lineno = l.N.Type.Maplineno
			maptype(l.N.Type, Types[TBOOL])
		}

		lineno = int32(lno)
	}

	ntypecheckdeftype--
}

func queuemethod(n *Node) {
	if ntypecheckdeftype == 0 {
		domethod(n)
		return
	}

	methodqueue = list(methodqueue, n)
}

func typecheckdef(n *Node) *Node {
	var lno int
	var nerrors0 int
	var e *Node
	var t *Type
	var l *NodeList

	lno = int(lineno)
	setlineno(n)

	if n.Op == ONONAME {
		if !(n.Diag != 0) {
			n.Diag = 1
			if n.Lineno != 0 {
				lineno = n.Lineno
			}

			// Note: adderrorname looks for this string and
			// adds context about the outer expression
			Yyerror("undefined: %v", Sconv(n.Sym, 0))
		}

		return n
	}

	if n.Walkdef == 1 {
		return n
	}

	l = new(NodeList)
	l.N = n
	l.Next = typecheckdefstack
	typecheckdefstack = l

	if n.Walkdef == 2 {
		Flusherrors()
		fmt.Printf("typecheckdef loop:")
		for l = typecheckdefstack; l != nil; l = l.Next {
			fmt.Printf(" %v", Sconv(l.N.Sym, 0))
		}
		fmt.Printf("\n")
		Fatal("typecheckdef loop")
	}

	n.Walkdef = 2

	if n.Type != nil || n.Sym == nil { // builtin or no name
		goto ret
	}

	switch n.Op {
	default:
		Fatal("typecheckdef %v", Oconv(int(n.Op), 0))
		fallthrough

		// not really syms
	case OGOTO,
		OLABEL:
		break

	case OLITERAL:
		if n.Ntype != nil {
			typecheck(&n.Ntype, Etype)
			n.Type = n.Ntype.Type
			n.Ntype = nil
			if n.Type == nil {
				n.Diag = 1
				goto ret
			}
		}

		e = n.Defn
		n.Defn = nil
		if e == nil {
			lineno = n.Lineno
			Dump("typecheckdef nil defn", n)
			Yyerror("xxx")
		}

		typecheck(&e, Erv|Eiota)
		if Isconst(e, CTNIL) != 0 {
			Yyerror("const initializer cannot be nil")
			goto ret
		}

		if e.Type != nil && e.Op != OLITERAL || !(isgoconst(e) != 0) {
			if !(e.Diag != 0) {
				Yyerror("const initializer %v is not a constant", Nconv(e, 0))
				e.Diag = 1
			}

			goto ret
		}

		t = n.Type
		if t != nil {
			if !(okforconst[t.Etype] != 0) {
				Yyerror("invalid constant type %v", Tconv(t, 0))
				goto ret
			}

			if !(isideal(e.Type) != 0) && !Eqtype(t, e.Type) {
				Yyerror("cannot use %v as type %v in const initializer", Nconv(e, obj.FmtLong), Tconv(t, 0))
				goto ret
			}

			Convlit(&e, t)
		}

		n.Val = e.Val
		n.Type = e.Type

	case ONAME:
		if n.Ntype != nil {
			typecheck(&n.Ntype, Etype)
			n.Type = n.Ntype.Type

			if n.Type == nil {
				n.Diag = 1
				goto ret
			}
		}

		if n.Type != nil {
			break
		}
		if n.Defn == nil {
			if n.Etype != 0 { // like OPRINTN
				break
			}
			if nsavederrors+nerrors > 0 {
				// Can have undefined variables in x := foo
				// that make x have an n->ndefn == nil.
				// If there are other errors anyway, don't
				// bother adding to the noise.
				break
			}

			Fatal("var without type, init: %v", Sconv(n.Sym, 0))
		}

		if n.Defn.Op == ONAME {
			typecheck(&n.Defn, Erv)
			n.Type = n.Defn.Type
			break
		}

		typecheck(&n.Defn, Etop) // fills in n->type

	case OTYPE:
		if Curfn != nil {
			defercheckwidth()
		}
		n.Walkdef = 1
		n.Type = typ(TFORW)
		n.Type.Sym = n.Sym
		nerrors0 = nerrors
		typecheckdeftype(n)
		if n.Type.Etype == TFORW && nerrors > nerrors0 {
			// Something went wrong during type-checking,
			// but it was reported. Silence future errors.
			n.Type.Broke = 1
		}

		if Curfn != nil {
			resumecheckwidth()
		}

		// nothing to see here
	case OPACK:
		break
	}

ret:
	if n.Op != OLITERAL && n.Type != nil && isideal(n.Type) != 0 {
		Fatal("got %v for %v", Tconv(n.Type, 0), Nconv(n, 0))
	}
	if typecheckdefstack.N != n {
		Fatal("typecheckdefstack mismatch")
	}
	l = typecheckdefstack
	typecheckdefstack = l.Next

	lineno = int32(lno)
	n.Walkdef = 1
	return n
}

func checkmake(t *Type, arg string, n *Node) int {
	if n.Op == OLITERAL {
		switch n.Val.Ctype {
		case CTINT,
			CTRUNE,
			CTFLT,
			CTCPLX:
			n.Val = toint(n.Val)
			if mpcmpfixc(n.Val.U.Xval, 0) < 0 {
				Yyerror("negative %s argument in make(%v)", arg, Tconv(t, 0))
				return -1
			}

			if Mpcmpfixfix(n.Val.U.Xval, Maxintval[TINT]) > 0 {
				Yyerror("%s argument too large in make(%v)", arg, Tconv(t, 0))
				return -1
			}

			// Delay defaultlit until after we've checked range, to avoid
			// a redundant "constant NNN overflows int" error.
			defaultlit(&n, Types[TINT])

			return 0

		default:
			break
		}
	}

	if !(Isint[n.Type.Etype] != 0) && n.Type.Etype != TIDEAL {
		Yyerror("non-integer %s argument in make(%v) - %v", arg, Tconv(t, 0), Tconv(n.Type, 0))
		return -1
	}

	// Defaultlit still necessary for non-constant: n might be 1<<k.
	defaultlit(&n, Types[TINT])

	return 0
}

func markbreak(n *Node, implicit *Node) {
	var lab *Label

	if n == nil {
		return
	}

	switch n.Op {
	case OBREAK:
		if n.Left == nil {
			if implicit != nil {
				implicit.Hasbreak = 1
			}
		} else {
			lab = n.Left.Sym.Label
			if lab != nil {
				lab.Def.Hasbreak = 1
			}
		}

	case OFOR,
		OSWITCH,
		OTYPESW,
		OSELECT,
		ORANGE:
		implicit = n
		fallthrough

		// fall through
	default:
		markbreak(n.Left, implicit)

		markbreak(n.Right, implicit)
		markbreak(n.Ntest, implicit)
		markbreak(n.Nincr, implicit)
		markbreaklist(n.Ninit, implicit)
		markbreaklist(n.Nbody, implicit)
		markbreaklist(n.Nelse, implicit)
		markbreaklist(n.List, implicit)
		markbreaklist(n.Rlist, implicit)
	}
}

func markbreaklist(l *NodeList, implicit *Node) {
	var n *Node
	var lab *Label

	for ; l != nil; l = l.Next {
		n = l.N
		if n.Op == OLABEL && l.Next != nil && n.Defn == l.Next.N {
			switch n.Defn.Op {
			case OFOR,
				OSWITCH,
				OTYPESW,
				OSELECT,
				ORANGE:
				lab = new(Label)
				lab.Def = n.Defn
				n.Left.Sym.Label = lab
				markbreak(n.Defn, n.Defn)
				n.Left.Sym.Label = nil
				l = l.Next
				continue
			}
		}

		markbreak(n, implicit)
	}
}

func isterminating(l *NodeList, top int) int {
	var def int
	var n *Node

	if l == nil {
		return 0
	}
	if top != 0 {
		for l.Next != nil && l.N.Op != OLABEL {
			l = l.Next
		}
		markbreaklist(l, nil)
	}

	for l.Next != nil {
		l = l.Next
	}
	n = l.N

	if n == nil {
		return 0
	}

	switch n.Op {
	// NOTE: OLABEL is treated as a separate statement,
	// not a separate prefix, so skipping to the last statement
	// in the block handles the labeled statement case by
	// skipping over the label. No case OLABEL here.

	case OBLOCK:
		return isterminating(n.List, 0)

	case OGOTO,
		ORETURN,
		ORETJMP,
		OPANIC,
		OXFALL:
		return 1

	case OFOR:
		if n.Ntest != nil {
			return 0
		}
		if n.Hasbreak != 0 {
			return 0
		}
		return 1

	case OIF:
		return bool2int(isterminating(n.Nbody, 0) != 0 && isterminating(n.Nelse, 0) != 0)

	case OSWITCH,
		OTYPESW,
		OSELECT:
		if n.Hasbreak != 0 {
			return 0
		}
		def = 0
		for l = n.List; l != nil; l = l.Next {
			if !(isterminating(l.N.Nbody, 0) != 0) {
				return 0
			}
			if l.N.List == nil { // default
				def = 1
			}
		}

		if n.Op != OSELECT && !(def != 0) {
			return 0
		}
		return 1
	}

	return 0
}

func checkreturn(fn *Node) {
	if fn.Type.Outtuple != 0 && fn.Nbody != nil {
		if !(isterminating(fn.Nbody, 1) != 0) {
			yyerrorl(int(fn.Endlineno), "missing return at end of function")
		}
	}
}