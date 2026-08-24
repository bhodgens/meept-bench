package main

import (
	"fmt"
	"os"

	"github.com/bhodgens/meept-bench/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.String())
		return
	}
	fmt.Println("meept-bench: benchmark harness for the meept agent daemon")
	fmt.Println("status: scaffold — see docs/PLAN.md for the implementation plan")
}
