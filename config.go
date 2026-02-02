package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds application-wide configuration settings.
type Config struct {
	LastSelectedChannelID string `json:"last_selected_channel_id"`
}

const (
	configFileName   = "somatui_config.json"
	appConfigDirName = "somatui"
)

// getConfigFilePath returns the absolute path to the configuration file.
func getConfigFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	appConfigDir := filepath.Join(configDir, appConfigDirName)
	if err := os.MkdirAll(appConfigDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create app config directory: %w", err)
	}
	return filepath.Join(appConfigDir, configFileName), nil
}

// LoadConfig reads the application configuration from the config file.
// If the file does not exist, it returns a default empty Config.
func LoadConfig() (*Config, error) {
	configPath, err := getConfigFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		// If file doesn't exist, return a default config
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config data: %w", err)
	}

	return &config, nil
}

// SaveConfig writes the given application configuration to the config file.
func SaveConfig(config *Config) error {
	configPath, err := getConfigFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config for saving: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config to file: %w", err)
	}

	return nil
}