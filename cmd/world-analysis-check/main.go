// Offline observation planning/auditing. This tool does not submit inference,
// create projects/assets, or attest that caller-supplied receipts are authentic.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
		input, err := readNativeInput(os.Stdin)
		if err != nil {
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

type nativeInput struct {
	Step     analysis.Step
	Prepared analysis.Prepared
	Binding  analysis.Binding
	Result   json.RawMessage
}

func readNativeInput(reader io.Reader) (nativeInput, error) {
	var input nativeInput
	// Channel envelopes legitimately contain nullable prices/reasons. Keep that
	// opaque body intact while enforcing exact fields for the local audit inputs.
	data, err := strictjson.ReadValue(reader, 8*1024*1024)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &fields) != nil || len(fields) != 4 || fields["result"] == nil {
		return input, strictjson.ErrInvalid
	}
	for key, target := range map[string]any{"step": &input.Step, "prepared": &input.Prepared, "binding": &input.Binding} {
		if strictjson.Decode(bytes.NewReader(fields[key]), 65536, target) != nil {
			return input, strictjson.ErrInvalid
		}
	}
	input.Result = fields["result"]
	return input, nil
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
