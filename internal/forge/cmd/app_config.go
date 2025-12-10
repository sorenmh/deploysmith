package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"gopkg.in/yaml.v3"
)

// AppConfig represents the app configuration stored in .deploysmith/app.yaml
type AppConfig struct {
	AppID   string `yaml:"appId"`
	AppName string `yaml:"appName"`
}

// LoadAppConfig loads app configuration from .deploysmith/app.yaml
func LoadAppConfig() (*AppConfig, error) {
	configFile := filepath.Join(".deploysmith", "app.yaml")

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("app config file not found (run 'forge app-bind' or specify --app)")
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read app config: %w", err)
	}

	var config AppConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse app config: %w", err)
	}

	return &config, nil
}

// SaveAppConfig saves app configuration to .deploysmith/app.yaml
func SaveAppConfig(appID, appName string) error {
	configDir := ".deploysmith"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create .deploysmith directory: %w", err)
	}

	config := AppConfig{
		AppID:   appID,
		AppName: appName,
	}

	data, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("failed to marshal app config: %w", err)
	}

	configFile := filepath.Join(configDir, "app.yaml")
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write app config: %w", err)
	}

	return nil
}

// ResolveAppID resolves the app ID, either from config file or by app name lookup
func ResolveAppID(appName string) (string, string, error) {
	// If app name is provided, look it up
	if appName != "" {
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
		appID, err := c.GetAppIDByName(appName)
		if err != nil {
			return "", "", fmt.Errorf("failed to resolve app '%s': %w", appName, err)
		}
		return appID, appName, nil
	}

	// Try to load from config file
	config, err := LoadAppConfig()
	if err != nil {
		return "", "", err
	}

	return config.AppID, config.AppName, nil
}