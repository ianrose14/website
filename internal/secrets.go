package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type SecretsFile struct {
	Dropbox struct {
		AccessToken string `yaml:"accessToken"`
	}
	Strava struct {
		ClientID     string `yaml:"clientId"`
		ClientSecret string `yaml:"clientSecret"`
	}
}

func ParseSecrets(filename string) (*SecretsFile, error) {
	fp, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer fp.Close()

	var dst SecretsFile
	if err := yaml.NewDecoder(fp).Decode(&dst); err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	return &dst, nil
}
