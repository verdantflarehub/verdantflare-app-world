// Offline observation planning/auditing. This tool does not submit inference,
// create projects/assets, or attest that caller-supplied receipts are authentic.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: world-analysis-check plan|verify|verify-native < input.json", 2)
	}
	var output any
	switch os.Args[1] {
	case "plan":
		var source analysis.Source
		if strictjson.Decode(os.Stdin, 4096, &source) != nil {
			fail("invalid source metadata", 2)
		}
		plan, err := analysis.PlanVideo(source)
		if err != nil {
			fail("invalid source metadata", 2)
		}
		payloads := map[string]any{}
		for _, step := range plan.Steps {
			payload, err := step.Payload()
			if err != nil {
				fail("invalid observation plan", 2)
			}
			payloads[step.Key] = payload
		}
		output = struct {
			Plan     analysis.Plan  `json:"plan"`
			Payloads map[string]any `json:"payloads"`
		}{plan, payloads}
	case "verify-native":
		var input struct {
			Step     analysis.Step     `json:"step"`
			Prepared analysis.Prepared `json:"prepared"`
			Binding  analysis.Binding  `json:"binding"`
			Result   json.RawMessage   `json:"result"`
		}
		if strictjson.Decode(os.Stdin, 8*1024*1024, &input) != nil {
			fail("invalid native audit input", 2)
		}
		report := analysis.VerifyNative(input.Step, input.Prepared, input.Binding, input.Result)
		write(report)
		if !report.Passed {
			os.Exit(1)
		}
		return
	case "verify":
		var input struct {
			Step     analysis.Step     `json:"step"`
			Prepared analysis.Prepared `json:"prepared"`
			Receipt  analysis.Receipt  `json:"receipt"`
			Text     string            `json:"text"`
		}
		if strictjson.Decode(os.Stdin, 8*1024*1024, &input) != nil {
			fail("invalid audit input", 2)
		}
		report := analysis.Verify(input.Step, input.Prepared, input.Receipt, []byte(input.Text))
		write(report)
		if !report.Passed {
			os.Exit(1)
		}
		return
	default:
		fail("unknown offline command", 2)
	}
	write(output)
}

func write(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if encoder.Encode(value) != nil {
		fail("cannot write audit output", 2)
	}
}

func fail(message string, code int) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(code)
}
