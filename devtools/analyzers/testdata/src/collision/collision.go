// Package collision shows why the receiver filter exists: a type that is not the client has methods named
// like a reader and a writer, and without a filter a function using them is reported.
package collision

type Config struct{}

func (Config) Info() int { return 0 }

func (Config) SetThing(int) {}

func Both(c Config) { // want `Both reads`
	_ = c.Info()
	c.SetThing(1)
}
