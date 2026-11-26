package app_test

import (
	"context"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/app"
	"github.com/hongling2511/corekit/log"
)

// The redaction matrix: every placement of a secret mark (on a leaf, on a
// block, on a squashed block, on a leaf inside a squashed block, two levels
// of squash, a self-formatting type) inside every container shape (direct,
// pointer, slice, array, map, interface, map of interfaces), checked in both
// outputs that print the configuration: the start-up log and config print.
// Secret values contain "SECRET"; the plain values next to them must stay.

type Inner struct {
	Value string `mapstructure:"value"`
	More  string `mapstructure:"more"`
}

type LeafSecret struct {
	Plain  string `mapstructure:"plain"`
	Secret string `mapstructure:"secret" secret:"true"`
}

type BlockSecret struct {
	Plain string `mapstructure:"plain"`
	Block Inner  `mapstructure:"block" secret:"true"`
}

type BlockPtrSecret struct {
	Plain string `mapstructure:"plain"`
	Block *Inner `mapstructure:"block" secret:"true"`
}

type SquashBlockSecret struct {
	Plain string `mapstructure:"plain"`
	Inner `mapstructure:",squash" secret:"true"`
}

type SquashLeaf struct {
	Secret string `mapstructure:"secret" secret:"true"`
}

type SquashLeafSecret struct {
	Plain      string `mapstructure:"plain"`
	SquashLeaf `mapstructure:",squash"`
}

type Outer struct {
	Inner `mapstructure:",squash"`
}

type NestedSquashSecret struct {
	Plain string `mapstructure:"plain"`
	Outer `mapstructure:",squash" secret:"true"`
}

type TextSecret struct {
	Plain  string `mapstructure:"plain"`
	Secret string `mapstructure:"secret" secret:"true"`
}

func (s TextSecret) MarshalText() ([]byte, error) { return []byte(s.Plain + s.Secret), nil }

type CollectionsSecret struct {
	Plain  string           `mapstructure:"plain"`
	Items  []Inner          `mapstructure:"items" secret:"true"`
	ByName map[string]Inner `mapstructure:"by_name" secret:"true"`
	Any    any              `mapstructure:"any" secret:"true"`
}

type direct[P any] struct {
	X P `mapstructure:"x"`
}

type pointer[P any] struct {
	X *P `mapstructure:"x"`
}

type slice[P any] struct {
	X []P `mapstructure:"x"`
}

type array[P any] struct {
	X [1]P `mapstructure:"x"`
}

type mapOf[P any] struct {
	X map[string]P `mapstructure:"x"`
}

type iface struct {
	X any `mapstructure:"x"`
}

type mapOfAny struct {
	X map[string]any `mapstructure:"x"`
}

// matrixOptions installs its case in Complete, after loading, so the case
// value is exactly what the pipeline prints.
type matrixOptions[C any] struct {
	Log     log.Options `mapstructure:"log"`
	Sibling string      `mapstructure:"sibling"`
	Case    C           `mapstructure:"case"`
	value   C
}

func (o *matrixOptions[C]) AddFlags(fs *pflag.FlagSet) { o.Log.AddFlags(fs) }
func (o *matrixOptions[C]) Validate() error            { return o.Log.Validate() }
func (o *matrixOptions[C]) Logging() *log.Options      { return &o.Log }
func (o *matrixOptions[C]) Complete() error {
	o.Sibling = "PLAIN-SIBLING"
	o.Case = o.value
	return o.Log.Complete()
}

type matrixRow struct {
	name string
	run  func(t *testing.T) (stdout, stderr string)
}

func runCase[C any](c C) func(t *testing.T) (string, string) {
	return func(t *testing.T) (string, string) {
		opts := &matrixOptions[C]{Log: *log.NewOptions(), value: c}
		var code int
		stdout, stderr := capture(t, func() {
			code = app.New("matrix", opts).Run(context.Background(), []string{"config", "print"})
		})
		require.Equal(t, 0, code, stderr)
		return stdout, stderr
	}
}

// containers puts placement p into every container shape.
func containers[P any](placement string, p P) []matrixRow {
	return []matrixRow{
		{placement + "/direct", runCase(direct[P]{X: p})},
		{placement + "/pointer", runCase(pointer[P]{X: &p})},
		{placement + "/slice", runCase(slice[P]{X: []P{p}})},
		{placement + "/array", runCase(array[P]{X: [1]P{p}})},
		{placement + "/map", runCase(mapOf[P]{X: map[string]P{"k": p}})},
		{placement + "/interface", runCase(iface{X: p})},
		{placement + "/map-of-interface", runCase(mapOfAny{X: map[string]any{"k": p}})},
	}
}

func TestRedactionMatrixInBothOutputs(t *testing.T) {
	secretInner := Inner{Value: "SECRET-value", More: "SECRET-more"}
	var rows []matrixRow
	rows = append(rows, containers("leaf", LeafSecret{Plain: "PLAIN", Secret: "SECRET-leaf"})...)
	rows = append(rows, containers("block", BlockSecret{Plain: "PLAIN", Block: secretInner})...)
	rows = append(rows, containers("block-pointer", BlockPtrSecret{Plain: "PLAIN", Block: &secretInner})...)
	rows = append(rows, containers("squash-block", SquashBlockSecret{Plain: "PLAIN", Inner: secretInner})...)
	rows = append(rows, containers("squash-leaf", SquashLeafSecret{Plain: "PLAIN", SquashLeaf: SquashLeaf{Secret: "SECRET-leaf"}})...)
	rows = append(rows, containers("nested-squash-block", NestedSquashSecret{Plain: "PLAIN", Outer: Outer{Inner: secretInner}})...)
	rows = append(rows, containers("self-formatting", TextSecret{Plain: "PLAIN", Secret: "SECRET-text"})...)
	rows = append(rows, containers("collections", CollectionsSecret{
		Plain:  "PLAIN",
		Items:  []Inner{secretInner},
		ByName: map[string]Inner{"k": secretInner},
		Any:    secretInner,
	})...)

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			stdout, stderr := row.run(t)

			for name, out := range map[string]string{"config print": stdout, "start-up log": stderr} {
				assert.NotContains(t, out, "SECRET", name)
				assert.Contains(t, out, "PLAIN-SIBLING", name)
				assert.Contains(t, out, `"PLAIN"`, name)
				assert.Contains(t, out, "******", name)
			}
		})
	}
}
