// SPDX-License-Identifier: Apache-2.0

package pg

import "io/fs"

type fsys = fs.FS

func sub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
