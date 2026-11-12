package provider

import "errors"

// errNoClientBuilt is what a client constructor that must never run returns after it has failed the test:
// t.Fatal stops the test goroutine, so the value is never seen, but the constructor needs a non-nil error
// to return (a nil client together with a nil error is what the nilnil linter refuses).
var errNoClientBuilt = errors.New("no client may be built")
