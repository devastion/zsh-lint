//go:build !unix

package main

import "io/fs"

func replaceable(fs.FileInfo) bool {
	return true
}
