package cmd

import (
	"encoding/json"
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

// VersionInfo represents the version information stored in .forge/version-info
type VersionInfo struct {
	App     string `json:"app"`
	AppID   string `json:"appId"`
	Version string `json:"version"`
}

// LoadVersionInfo loads version information from .forge/version-info
func LoadVersionInfo() (*VersionInfo, error) {
	versionFile := filepath.Join(".forge", "version-info")

	if _, err := os.Stat(versionFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("version info file not found (run 'forge init' first)")
	}

	data, err := os.ReadFile(versionFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read version info: %w", err)
	}

	var versionInfo VersionInfo
	if err := json.Unmarshal(data, &versionInfo); err != nil {
		return nil, fmt.Errorf("failed to parse version info: %w", err)
	}

	return &versionInfo, nil
}

// ResolveVersion resolves the version, either from flag or from .forge/version-info
func ResolveVersion(version string) (string, string, string, error) {
	// If version is provided, we still need app info from somewhere
	if version != "" {
		// Try to get app info from version file first, then fall back to app config
		versionInfo, err := LoadVersionInfo()
		if err == nil {
			return versionInfo.AppID, versionInfo.App, version, nil
		}

		// Fall back to app config for app info
		appConfig, err := LoadAppConfig()
		if err != nil {
			return "", "", "", fmt.Errorf("version provided but no app info available: %w", err)
		}
		return appConfig.AppID, appConfig.AppName, version, nil
	}

	// Load from version info file
	versionInfo, err := LoadVersionInfo()
	if err != nil {
		return "", "", "", err
	}

	return versionInfo.AppID, versionInfo.App, versionInfo.Version, nil
}