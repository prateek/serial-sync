package config

import (
	"strings"
	"testing"
)

func TestPublicationIdentitySourceValidation(t *testing.T) {
	for _, location := range []string{"series", "book"} {
		for _, value := range []string{"", "embedded", "release", "automatic", "Release"} {
			t.Run(location+"/"+value, func(t *testing.T) {
				cfg, _, err := Load("../../examples/config.demo.toml")
				if err != nil {
					t.Fatal(err)
				}
				metadata := PublicationConfig{IdentitySource: value}
				if location == "series" {
					cfg.Series[0].Metadata = metadata
				} else {
					cfg.Series[0].Books = []BookConfig{{ID: "first", Number: 1, Title: "First", Metadata: metadata}}
				}
				err = cfg.Validate()
				valid := value == "" || value == "embedded" || value == "release"
				if valid && err != nil || !valid && (err == nil || !strings.Contains(err.Error(), "identity_source")) {
					t.Fatalf("identity_source %q: %v", value, err)
				}
			})
		}
	}
}
