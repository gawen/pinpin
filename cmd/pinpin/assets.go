package main

import (
	"embed"
	"hash/crc32"
	"io"
	"io/fs"
)

//go:embed *.jpg
var assetFS embed.FS

var entries []fs.DirEntry

func init() {
	var err error
	entries, err = assetFS.ReadDir(".")
	if err != nil {
		panic(err)
	}
}

func pickAssetJpegRaw(digest []byte) []byte {
	idx := crc32.ChecksumIEEE(digest) % uint32(len(entries))
	entryName := entries[idx].Name()
	fh, err := assetFS.Open(entryName)
	if err != nil {
		panic(err)
	}
	defer fh.Close()

	raw, err := io.ReadAll(fh)
	if err != nil {
		panic(err)
	}

	return raw
}
