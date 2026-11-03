package main

import (
	"os"
	"testing"
)

func TestPackageConfiguration(t *testing.T) {
	message, err := os.ReadFile("message.txt")
	if err != nil || string(message) != "package ready\n" {
		t.Fatalf("package configuration: %q, %v", message, err)
	}
}
