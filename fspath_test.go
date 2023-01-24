package fspath_test

import (
	"io/fs"
	"reflect"
	"testing"

	"github.com/stealthrocket/fspath"
	"github.com/stealthrocket/fstest"
)

func TestWalk(t *testing.T) {
	for _, test := range [...]struct {
		name string
		walk []string
	}{
		{
			name: ".",
			walk: []string{"."},
		},

		{
			name: "a",
			walk: []string{"a"},
		},

		{
			name: "a/b",
			walk: []string{"a", "a/b"},
		},

		{
			name: "a/b/c",
			walk: []string{"a", "a/b", "a/b/c"},
		},
	} {
		var walk []string
		if err := fspath.Walk(test.name, func(path string) error {
			walk = append(walk, path)
			return nil
		}); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(walk, test.walk) {
			t.Errorf("mismatch: want=%q got=%q", walk, test.walk)
		}
	}
}

func TestLookup(t *testing.T) {
	fsys := fstest.MapFS{
		"a":   &fstest.MapFile{Mode: 0755 | fs.ModeDir},
		"a/b": &fstest.MapFile{Mode: 0666 | fs.ModeSymlink, Data: []byte("../../c")},
		"a/c": &fstest.MapFile{Mode: 0755 | fs.ModeDir},
		"c/d": &fstest.MapFile{Mode: 0644, Data: []byte("Hello World!")},
	}

	b, err := fspath.ReadFile(fsys, "a/b/d")
	if err != nil {
		t.Error(err)
	}
	if string(b) != "Hello World!" {
		t.Errorf("wong file content: %q", b)
	}
}

func TestRootFS(t *testing.T) {
	fsys := fstest.MapFS{
		"a":   &fstest.MapFile{Mode: 0755 | fs.ModeDir},
		"a/b": &fstest.MapFile{Mode: 0666 | fs.ModeSymlink, Data: []byte("../c/d")},
		"a/c": &fstest.MapFile{Mode: 0755 | fs.ModeDir},
		"c/d": &fstest.MapFile{Mode: 0644, Data: []byte("Hello World!")},
	}

	if err := fstest.TestFS(fspath.RootFS(fsys), "a", "a/b", "a/c", "c/d"); err != nil {
		t.Error(err)
	}
}
