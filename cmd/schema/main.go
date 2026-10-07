// Command schema generates or checks committed editor schemas from Go contracts.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/octacian/backlot/internal/schema"
)

func main() {
	check := flag.Bool("check", false, "check committed artifacts without writing")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	artifacts, err := schema.Generate(".")
	if err != nil {
		return err
	}
	for name, data := range artifacts {
		path := filepath.Join("schemas", name)
		if check {
			current, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, data) {
				return fmt.Errorf("%s is stale; run make schema-generate", path)
			}
		} else if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
