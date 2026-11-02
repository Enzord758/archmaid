package engine

import (
	"fmt"
	"strings"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// aurQuestion is the AUR sheet's question in words (the review itself, the
// PKGBUILD and the diff, is Plan.AUR; the page shows it). Cancel is the
// default; the consent text is the contract's: "this runs the package
// author's code as root", and the number of password prompts is stated.
func aurQuestion(p steps.PkgPlan) (title, body, yes string) {
	title, yes = fmt.Sprintf("Build and install %s from the AUR?", count(len(p.Targets), "package")), "Build and install"
	var names, high []string
	first := 0
	for _, a := range p.AUR {
		names = append(names, a.Base)
		if a.Prev == "" {
			first++
		}
		for _, f := range a.Audit {
			if f.High {
				high = append(high, sanitize.Field(f.Name, 64)+": "+f.Reason)
			}
		}
	}
	body = fmt.Sprintf("%s will be built as you with makepkg: %s.", count(len(p.AUR), "AUR package base"), strings.Join(names, ", "))
	prompts := 1
	if len(p.Rows) > 0 {
		prompts = 2
		body += fmt.Sprintf(" First, %s from the repositories will be installed as dependencies.", count(len(p.Rows), "package"))
	}
	body += fmt.Sprintf("\n\nReview the PKGBUILD and the other files first: installing runs the package author's code as root. It asks for your password %s, and once an install has started it can't be stopped.", countTimes(prompts))
	if first < len(p.AUR) {
		body += "\nPackages you have installed before show what changed since you last reviewed them."
	}
	for _, h := range high {
		body += "\nAudit, high: " + h
	}
	for _, w := range p.Warnings {
		body += "\n" + sanitize.OneLine(w)
	}
	return title, body, yes
}

func countTimes(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times (one for each root step)", n)
}
