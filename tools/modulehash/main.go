package main

import (
	"fmt"
	"os"

	"golang.org/x/mod/sumdb/dirhash"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: modulehash DIR MODULE@VERSION")
		os.Exit(2)
	}
	sum, err := dirhash.HashDir(os.Args[1], os.Args[2], dirhash.Hash1)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(sum)
}
