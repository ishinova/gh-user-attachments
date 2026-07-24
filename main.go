package main

import (
	"os"

	"github.com/ishinova/gh-user-attachments/internal/userattachments"
)

func main() {
	os.Exit(userattachments.Run(os.Args[1:], os.Stdout, os.Stderr))
}
