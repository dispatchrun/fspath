package fspath

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// ReadLinkFS is an extension of the fs.FS interface implemented by file systems
// which support symbolic links.
//
// TODO: replace with fs.ReadLinkFS (https://github.com/golang/go/issues/49580)
type ReadLinkFS interface {
	fs.FS
	ReadLink(string) (string, error)
}

var (
	// ErrLoop is returned when attempting to resolve paths that have followed
	// too many symbolic links.
	ErrLoop = errors.New("loop")
)

func Open(fsys fs.FS, name string) (fs.File, error) {
	return lookup(fsys, name, fs.FS.Open)
}

func Stat(fsys fs.FS, name string) (fs.FileInfo, error) {
	return lookup(fsys, name, fs.Stat)
}

func Sub(fsys fs.FS, name string) (fs.FS, error) {
	return lookup(fsys, name, sub)
}

func ReadDir(fsys fs.FS, name string) ([]fs.DirEntry, error) {
	return lookup(fsys, name, fs.ReadDir)
}

func ReadFile(fsys fs.FS, name string) ([]byte, error) {
	return lookup(fsys, name, fs.ReadFile)
}

func ReadLink(fsys fs.FS, name string) (string, error) {
	return lookup(fsys, name, func(fsys fs.FS, name string) (string, error) {
		if f, ok := fsys.(ReadLinkFS); ok {
			return f.ReadLink(name)
		}
		err := fmt.Errorf("symlink found in file system which does not implement fs.ReadLinkFS: %t", fsys)
		return "", &fs.PathError{"readlink", name, err}
	})
}

func lookup[F func(fs.FS, string) (R, error), R any](fsys fs.FS, name string, fn F) (ret R, err error) {
	sub, base, err := Lookup(fsys, name)
	if err != nil {
		return ret, err
	}
	return fn(sub, base)
}

// Sentinel error used to stop walking through paths when encountering symoblic
// links.
var symlink = errors.New("symlink")

// Lookup looks for the name if fsys, following symbolic link that are
// encountered along the path.
//
// The function returns a view of fsys positioned on the last directory, and the
// base name of the file to look for in this directory. The name is guaranteed
// not to refer to a symbolic link.
//
// The functional guarantees that links never escape the file system root.
// If a link pointing above the root is encountered, it is rebased off of the
// root similarly to how "/.." resolves to "/" on posix systems. Lookup can
// therefore be used as a sandboxing mechanism to prevent escaping the bounds
// of a read-only file system; beware that if the underlying file system can
// be modified concurrently, these guarantees do no apply anymore!
func Lookup(fsys fs.FS, name string) (fs.FS, string, error) {
	if !fs.ValidPath(name) {
		return nil, "", &fs.PathError{"lookup", name, fs.ErrNotExist}
	}

	walk := make([]fs.FS, 0, 16)
	loop := 0

	for {
		// 40 is the maximum number of symbolic link lookups allowed by Linux,
		// assume there was a valid reason behind picking this value and do the
		// same so at least we are not changing the behavior of applications
		// that would have worked when using an os.DirFS directly.
		if loop++; loop == 40 {
			return fsys, name, &fs.PathError{"lookup", name, ErrLoop}
		}
		if name == "." {
			return fsys, name, nil
		}

		err := Walk(name, func(prefix string) error {
			base := path.Base(prefix)
			// There is no way to determine if the path is a symbolic link since
			// both Open and Stat will follow links, so we opportunistically try
			// to read the path as a link and assume that if it fails we are not
			// in the presence of a symbolic link.
			if f, ok := fsys.(ReadLinkFS); ok {
				link, err := f.ReadLink(base)
				switch {
				case err == nil:
					link = path.Clean(link)
					// Note: the current proposal from #49580 states that the
					// ReadLink method should error if the link being read is
					// absolute.
					switch {
					case link == "..":
					case strings.HasPrefix(link, "../"):
					case fs.ValidPath(link):
					default:
						return &fs.PathError{"lookup", link, fs.ErrNotExist}
					}

					// When the path is relative, we turn it into an absolute
					// path relative to the path of the file system root.
					// This might result in pointing above the root, which is
					// collapsed as it would when resolving a path like "/.."
					// on posix file systems.
					for len(walk) > 0 && (link == ".." || strings.HasPrefix(link, "../")) {
						i := len(walk) - 1
						fsys = walk[i]
						walk = walk[:i]
						link = strings.TrimPrefix(link, "..")
						link = strings.TrimPrefix(link, "/")
					}

					for link == ".." || strings.HasPrefix(link, "../") {
						link = strings.TrimPrefix(link, "..")
						link = strings.TrimPrefix(link, "/")
					}

					name = strings.TrimPrefix(name, prefix)
					name = strings.TrimPrefix(name, "/")
					name = path.Join(link, name)
					return symlink
				case errors.Is(err, fs.ErrInvalid):
				case errors.Is(err, fs.ErrNotExist):
				default:
					return err
				}
			}

			if len(prefix) < len(name) {
				sub, err := sub(fsys, base)
				if err != nil {
					return err
				}
				walk = append(walk, fsys)
				fsys = sub
			}

			return nil
		})

		if err != symlink {
			return fsys, path.Base(name), err
		}
	}
}

// Walk calls fn for each path prefix of name up to the full name.
//
// For a path such as "a/b/c", calling Walk("a/b/c", fn) will invoke fn with
// fn("a"), fn("a/b"), then fn("a/b/c"). If any of these calls returns an error,
// the walk is aborted and the error is returned.
func Walk(name string, fn func(path string) error) error {
	seek := 0
	for {
		if i := strings.IndexByte(name[seek:], '/'); i < 0 {
			return fn(name)
		} else {
			seek += i
		}
		if err := fn(name[:seek]); err != nil {
			return err
		}
		seek++
	}
}

// RooFS returns a fs.FS wrapping fsys and using the Lookup function when
// accesing files (e.g. calling Open, Stat, etc...).
func RootFS(fsys fs.FS) fs.FS { return rootFS{fsys} }

type rootFS struct{ fs.FS }

func (fsys rootFS) Open(name string) (fs.File, error) {
	return Open(fsys.FS, name)
}

func (fsys rootFS) Stat(name string) (fs.FileInfo, error) {
	return Stat(fsys.FS, name)
}

func (fsys rootFS) Sub(name string) (fs.FS, error) {
	return sub(noSubRootFS{fsys}, name)
}

func (fsys rootFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return ReadDir(fsys.FS, name)
}

func (fsys rootFS) ReadFile(name string) ([]byte, error) {
	return ReadFile(fsys.FS, name)
}

func (fsys rootFS) ReadLink(name string) (string, error) {
	return ReadLink(fsys.FS, name)
}

type noSubRootFS struct{ rootFS }

func (noSubRootFS) Sub() {} // wrong signature, does not match fs.SubFS

var (
	_ fs.StatFS     = rootFS{}
	_ fs.ReadDirFS  = rootFS{}
	_ fs.ReadFileFS = rootFS{}
	_ ReadLinkFS    = rootFS{}
)

// TODO: the code below is copied from the Go standard library to add the
// ReadLink method to subFS. We should remove it when ReadLinkFS has been added.

func (f *subFS) ReadLink(name string) (string, error) {
	full, err := f.fullName("readlink", name)
	if err != nil {
		return "", err
	}
	if r, ok := f.fsys.(ReadLinkFS); ok {
		return r.ReadLink(full)
	}
	return "", &fs.PathError{"readlink", name, fmt.Errorf("ReadLink called on file system which does not implement fs.ReadLinkFS: %T", f.fsys)}
}

var (
	_ ReadLinkFS = (*subFS)(nil)
)

// =============================================================================
// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// =============================================================================

func sub(fsys fs.FS, dir string) (fs.FS, error) {
	if !fs.ValidPath(dir) {
		return nil, &fs.PathError{Op: "sub", Path: dir, Err: errors.New("invalid name")}
	}
	if dir == "." {
		return fsys, nil
	}
	if fsys, ok := fsys.(fs.SubFS); ok {
		return fsys.Sub(dir)
	}
	return &subFS{fsys, dir}, nil
}

type subFS struct {
	fsys fs.FS
	dir  string
}

// fullName maps name to the fully-qualified name dir/name.
func (f *subFS) fullName(op string, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: errors.New("invalid name")}
	}
	return path.Join(f.dir, name), nil
}

// shorten maps name, which should start with f.dir, back to the suffix after f.dir.
func (f *subFS) shorten(name string) (rel string, ok bool) {
	if name == f.dir {
		return ".", true
	}
	if len(name) >= len(f.dir)+2 && name[len(f.dir)] == '/' && name[:len(f.dir)] == f.dir {
		return name[len(f.dir)+1:], true
	}
	return "", false
}

// fixErr shortens any reported names in PathErrors by stripping f.dir.
func (f *subFS) fixErr(err error) error {
	if e, ok := err.(*fs.PathError); ok {
		if short, ok := f.shorten(e.Path); ok {
			e.Path = short
		}
	}
	return err
}

func (f *subFS) Open(name string) (fs.File, error) {
	full, err := f.fullName("open", name)
	if err != nil {
		return nil, err
	}
	file, err := f.fsys.Open(full)
	return file, f.fixErr(err)
}

func (f *subFS) ReadDir(name string) ([]fs.DirEntry, error) {
	full, err := f.fullName("read", name)
	if err != nil {
		return nil, err
	}
	dir, err := fs.ReadDir(f.fsys, full)
	return dir, f.fixErr(err)
}

func (f *subFS) ReadFile(name string) ([]byte, error) {
	full, err := f.fullName("read", name)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(f.fsys, full)
	return data, f.fixErr(err)
}

func (f *subFS) Sub(dir string) (fs.FS, error) {
	if dir == "." {
		return f, nil
	}
	full, err := f.fullName("sub", dir)
	if err != nil {
		return nil, err
	}
	return &subFS{f.fsys, full}, nil
}
