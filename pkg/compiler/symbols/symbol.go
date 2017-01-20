// Copyright 2016 Marapongo, Inc. All rights reserved.

package symbols

import (
	"github.com/marapongo/mu/pkg/diag"
	"github.com/marapongo/mu/pkg/tokens"
)

// Symbol is the base interface for all MuIL symbol types.
type Symbol interface {
	symbol()
	Name() tokens.Name   // the simple name for this symbol.
	Token() tokens.Token // the unique qualified name token for this symbol.
	Tree() diag.Diagable // the diagnosable tree associated with this symbol.
}
