package domainrules

import (
	"go/ast"
	"strings"
)

// itoa is n in decimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// atoi is the decimal number s, and whether s is one.
func atoi(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

// sortStrings sorts s in place.
func sortStrings(s []string) { sortSlice(s, func(a, b string) bool { return a < b }) }

// sortSlice sorts s in place by less, stably: a merge sort, since domainrules imports no sort package.
func sortSlice[T any](s []T, less func(a, b T) bool) {
	if len(s) < 2 {
		return
	}
	mergeSort(s, make([]T, len(s)), less)
}

func mergeSort[T any](s, buf []T, less func(a, b T) bool) {
	if len(s) <= 12 {
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && less(s[j], s[j-1]); j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
		return
	}
	m := len(s) / 2
	mergeSort(s[:m], buf[:m], less)
	mergeSort(s[m:], buf[m:], less)
	copy(buf, s)
	i, j, k := 0, m, 0
	for i < m && j < len(s) {
		if less(buf[j], buf[i]) {
			s[k] = buf[j]
			j++
		} else {
			s[k] = buf[i]
			i++
		}
		k++
	}
	k += copy(s[k:], buf[i:m])
	copy(s[k:], buf[j:])
}

// lowerCamel is name with its leading capitals lowered as Go spells a field named for a type: MachineID ->
// machineID, URLPath -> urlPath, State -> state.
func lowerCamel(name string) string {
	r := []rune(name)
	i := 0
	for i < len(r) && r[i] >= 'A' && r[i] <= 'Z' {
		i++
	}
	switch {
	case i == 0:
		return name
	case i == 1 || i == len(r):
		for k := 0; k < i; k++ {
			r[k] += 'a' - 'A'
		}
	default: // URLPath: lower all but the capital that starts the next word
		for k := 0; k < i-1; k++ {
			r[k] += 'a' - 'A'
		}
	}
	return string(r)
}

// contains says whether list holds s.
func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// exported says whether a Go name is exported.
func exported(name string) bool { return name != "" && name[0] >= 'A' && name[0] <= 'Z' }

// exprString is a type expression's source form, enough to compare field lists and to name types in messages.
func exprString(e ast.Expr) string {
	var b strings.Builder
	writeExpr(&b, e)
	return b.String()
}

func writeExpr(b *strings.Builder, e ast.Expr) {
	switch e := e.(type) {
	case nil:
	case *ast.Ident:
		b.WriteString(e.Name)
	case *ast.SelectorExpr:
		writeExpr(b, e.X)
		b.WriteString(".")
		b.WriteString(e.Sel.Name)
	case *ast.StarExpr:
		b.WriteString("*")
		writeExpr(b, e.X)
	case *ast.ParenExpr:
		b.WriteString("(")
		writeExpr(b, e.X)
		b.WriteString(")")
	case *ast.ArrayType:
		b.WriteString("[")
		if e.Len != nil {
			writeExpr(b, e.Len)
		}
		b.WriteString("]")
		writeExpr(b, e.Elt)
	case *ast.Ellipsis:
		b.WriteString("...")
		writeExpr(b, e.Elt)
	case *ast.MapType:
		b.WriteString("map[")
		writeExpr(b, e.Key)
		b.WriteString("]")
		writeExpr(b, e.Value)
	case *ast.ChanType:
		switch e.Dir {
		case ast.SEND:
			b.WriteString("chan<- ")
		case ast.RECV:
			b.WriteString("<-chan ")
		default:
			b.WriteString("chan ")
		}
		writeExpr(b, e.Value)
	case *ast.FuncType:
		b.WriteString("func(")
		writeFields(b, e.Params, ", ")
		b.WriteString(")")
		if e.Results != nil && len(e.Results.List) > 0 {
			b.WriteString(" (")
			writeFields(b, e.Results, ", ")
			b.WriteString(")")
		}
	case *ast.InterfaceType:
		b.WriteString("interface{")
		writeFields(b, e.Methods, "; ")
		b.WriteString("}")
	case *ast.StructType:
		b.WriteString("struct{")
		writeFields(b, e.Fields, "; ")
		b.WriteString("}")
	case *ast.IndexExpr:
		writeExpr(b, e.X)
		b.WriteString("[")
		writeExpr(b, e.Index)
		b.WriteString("]")
	case *ast.IndexListExpr:
		writeExpr(b, e.X)
		b.WriteString("[")
		for i, x := range e.Indices {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExpr(b, x)
		}
		b.WriteString("]")
	case *ast.BasicLit:
		b.WriteString(e.Value)
	case *ast.UnaryExpr:
		b.WriteString(e.Op.String())
		writeExpr(b, e.X)
	case *ast.BinaryExpr:
		writeExpr(b, e.X)
		b.WriteString(" " + e.Op.String() + " ")
		writeExpr(b, e.Y)
	case *ast.CallExpr:
		writeExpr(b, e.Fun)
		b.WriteString("(...)")
	case *ast.CompositeLit:
		writeExpr(b, e.Type)
		b.WriteString("{...}")
	case *ast.FuncLit:
		b.WriteString("func literal")
	default:
		b.WriteString("?")
	}
}

func writeFields(b *strings.Builder, fl *ast.FieldList, sep string) {
	if fl == nil {
		return
	}
	for i, f := range fl.List {
		if i > 0 {
			b.WriteString(sep)
		}
		for j, n := range f.Names {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(n.Name)
		}
		if len(f.Names) > 0 {
			b.WriteString(" ")
		}
		writeExpr(b, f.Type)
	}
}
