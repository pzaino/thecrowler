package main

import (
	"fmt"
	"os"

	"github.com/pzaino/thecrowler/pkg/agent"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 || args[0] != "agents" {
		return fmt.Errorf("usage: crowler-agt agents <lint|validate|convert> ...")
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: crowler-agt agents <lint|validate|convert> ...")
	}
	switch args[1] {
	case "lint":
		if len(args) != 3 {
			return fmt.Errorf("usage: crowler-agt agents lint <file>")
		}
		return agent.LintAgentFile(args[2])
	case "validate":
		return runValidate(args[2:])
	case "convert":
		return runConvert(args[2:])
	default:
		return fmt.Errorf("unknown agents subcommand: %s", args[1])
	}
}

func runValidate(args []string) error {
	strict := false
	if len(args) == 0 {
		return fmt.Errorf("usage: crowler-agt agents validate [--strict] <file>")
	}
	if args[0] == "--strict" {
		strict = true
		args = args[1:]
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: crowler-agt agents validate [--strict] <file>")
	}
	return agent.ValidateAgentFile(args[0], strict, nil)
}

func runConvert(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("usage: crowler-agt agents convert <json2yaml|yaml2json> <input> [output]")
	}
	mode := args[0]
	input := args[1]
	output := ""
	if len(args) == 3 {
		output = args[2]
	}
	return agent.ConvertAgentFile(input, output, mode)
}
