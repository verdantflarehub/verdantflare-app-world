package main

import (
	"strings"
	"testing"
)

func TestAnalysisConfigurationKeepsRecoveryWithoutDispatch(t *testing.T) {
	token := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name                          string
		values                        map[string]string
		tokens                        map[string]bool
		invalid, configured, dispatch bool
	}{
		{name: "absent", values: map[string]string{}},
		{name: "recover", values: map[string]string{"WORLD_ANALYSIS_URL": "https://analysis.internal", "GOOGLE_ANALYSIS_TOKEN": token}, configured: true},
		{name: "dispatch", values: map[string]string{"WORLD_ANALYSIS_URL": "https://analysis.internal", "GOOGLE_ANALYSIS_TOKEN": token, "WORLD_ANALYSIS_DISPATCH_ENABLED": "true"}, configured: true, dispatch: true},
		{name: "missing token", values: map[string]string{"WORLD_ANALYSIS_URL": "https://analysis.internal"}, invalid: true},
		{name: "enabled without channel", values: map[string]string{"WORLD_ANALYSIS_DISPATCH_ENABLED": "true"}, invalid: true},
		{name: "reused credential", values: map[string]string{"WORLD_ANALYSIS_URL": "https://analysis.internal", "GOOGLE_ANALYSIS_TOKEN": token}, tokens: map[string]bool{token: true}, invalid: true},
		{name: "invalid flag", values: map[string]string{"WORLD_ANALYSIS_DISPATCH_ENABLED": "yes"}, invalid: true},
		{name: "unprotected origin", values: map[string]string{"WORLD_ANALYSIS_URL": "http://analysis.internal", "GOOGLE_ANALYSIS_TOKEN": token}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := readAnalysisConfig(func(key string) string { return tc.values[key] }, tc.tokens)
			if (err != nil) != tc.invalid {
				t.Fatal("unexpected configuration acceptance", err)
			}
			if err == nil && ((cfg.channel != nil) != tc.configured || cfg.dispatch != tc.dispatch) {
				t.Fatal("wrong recovery/dispatch mode")
			}
		})
	}
}
