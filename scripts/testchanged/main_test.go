package main

import (
	"reflect"
	"testing"
)

func TestCommentsOnly(t *testing.T) {
	base := `package p

// Old comment.
func A() int {
	return 1
}
`
	for _, tc := range []struct {
		name string
		next string
		want bool
	}{
		{
			name: "comment changed",
			next: `package p

// New comment, longer than before.
func A() int {
	// And a comment in the body.
	return 1
}
`,
			want: true,
		},
		{
			name: "blank lines only",
			next: `package p


func A() int {

	return 1
}
`,
			want: true,
		},
		{
			name: "code changed",
			next: `package p

// Old comment.
func A() int {
	return 2
}
`,
			want: false,
		},
		{
			name: "build tag changed",
			next: `//go:build linux

package p

// Old comment.
func A() int {
	return 1
}
`,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := commentsOnly(base, tc.next)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("commentsOnly = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestChangedTests(t *testing.T) {
	base := `package p

import "testing"

func TestAlpha(t *testing.T) {
	if 1 != 1 {
		t.Fatal("alpha")
	}
}

func TestBeta(t *testing.T) {
	if 1 != 1 {
		t.Fatal("beta")
	}
}

func helper() int { return 1 }
`
	next := `package p

import "testing"

func TestAlpha(t *testing.T) {
	if 2 != 2 {
		t.Fatal("alpha changed")
	}
}

func TestBeta(t *testing.T) {
	if 1 != 1 {
		t.Fatal("beta")
	}
}

func TestGamma(t *testing.T) {
	if 1 != 1 {
		t.Fatal("gamma")
	}
}

func helper() int { return 1 }
`
	got, err := changedTests(base, next)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TestAlpha", "TestGamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changedTests = %v, want %v", got, want)
	}
}

func TestChangedTestsRunsAllWhenOnlyAHelperChanged(t *testing.T) {
	base := `package p

import "testing"

func TestAlpha(t *testing.T) { helper() }
func TestBeta(t *testing.T) { helper() }
func helper() int { return 1 }
`
	next := `package p

import "testing"

func TestAlpha(t *testing.T) { helper() }
func TestBeta(t *testing.T) { helper() }
func helper() int { return 2 }
`
	got, err := changedTests(base, next)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TestAlpha", "TestBeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changedTests = %v, want %v", got, want)
	}
}

func TestTestNames(t *testing.T) {
	src := `package p

import "testing"

func TestAlpha(t *testing.T) {}
func ExampleBeta() {}
func FuzzGamma(f *testing.F) {}
func TestMain(m *testing.M) {}
func helper() {}
type T struct{}
func (T) TestMethod(t *testing.T) {}
`
	got, err := testNames(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ExampleBeta", "FuzzGamma", "TestAlpha"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("testNames = %v, want %v", got, want)
	}
}
