package cli

import (
	"github.com/spf13/cobra/doc"
)

// GenManPages generates man pages for the entire dfmsctl command tree into dir.
// The directory must exist. Called by the dfmsctl-docs helper and usable from
// any tooling that needs to produce documentation from the live command tree.
func GenManPages(dir string) error {
	root := NewRootCommand(BuildInfo{Version: "dev"})
	header := &doc.GenManHeader{
		Title:   "DFMSCTL",
		Section: "1",
		Source:  "DFMS",
		Manual:  "DFMS CLI Manual",
	}
	return doc.GenManTree(root, header, dir)
}
