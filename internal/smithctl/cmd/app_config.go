package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
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
		return nil, fmt.Errorf("app config file not found (run 'forge app-bind' or specify app name/ID)")
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

// ResolveAppID resolves the app ID, either from provided name/ID or from config files
// Returns (appID, appName, error)
func ResolveAppID(appIdentifier string) (string, string, error) {
	// If app identifier is provided, determine if it's a name or ID
	if appIdentifier != "" {
		// Try to load from app config first to check if it matches
		config, err := LoadAppConfig()
		if err == nil {
			if config.AppID == appIdentifier {
				return config.AppID, config.AppName, nil
			}
			if config.AppName == appIdentifier {
				return config.AppID, config.AppName, nil
			}
		}

		// Try to load from version info
		versionInfo, err := LoadVersionInfo()
		if err == nil {
			if versionInfo.AppID == appIdentifier {
				return versionInfo.AppID, versionInfo.App, nil
			}
			if versionInfo.App == appIdentifier {
				return versionInfo.AppID, versionInfo.App, nil
			}
		}

		// If not found in config files, treat as app name and resolve via API
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
		appID, err := c.GetAppIDByName(appIdentifier)
		if err != nil {
			return "", "", fmt.Errorf("failed to resolve app '%s': %w", appIdentifier, err)
		}
		return appID, appIdentifier, nil
	}

	// No app identifier provided, try to load from config files
	// First try app config
	config, err := LoadAppConfig()
	if err == nil {
		return config.AppID, config.AppName, nil
	}

	// Then try version info
	versionInfo, err := LoadVersionInfo()
	if err == nil {
		return versionInfo.AppID, versionInfo.App, nil
	}

	return "", "", fmt.Errorf("no app specified and no app binding found (run 'forge app-bind' or specify app name/ID)")
}